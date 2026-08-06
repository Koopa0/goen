package media

import (
	"container/list"
	"context"
	"errors"
	"runtime"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// # What this file is defending
//
// GET /media/{digest}/{width} is the most expensive thing an anonymous request
// can ask goen to do, and it was doing it every time. A request without a
// matching If-None-Match read the whole stored image out of PostgreSQL, decoded
// it to as many as forty million pixels, scaled it with CatmullRom — the
// sharpest and slowest scaler in x/image/draw — and re-encoded the result.
//
// Every bound that existed was on the OUTPUT. The width allowlist stops somebody
// asking for 100000px; it says nothing about the input, which is bounded only at
// MaxPixels, and nothing about how many of these may run at once. So a hundred
// concurrent requests for one large product photo were a hundred simultaneous
// decodes of the same image — no login, no cookie, one URL that is public by
// design and appears in the srcset of every listing page.
//
// Three things fix it, and they are three different problems:
//
//   - A CACHE, so the work is done once per (digest, width) in this process
//     rather than once per request. It is safe in a way an image cache normally
//     is not: the URL is the content's own digest, so a cached rendition can
//     never be stale and there is no invalidation to get wrong.
//   - SINGLEFLIGHT, because the cache does not help the FIRST hundred. They all
//     miss together, and without collapsing them the stampede is exactly as
//     expensive as it was before.
//   - A CONCURRENCY BOUND, because a hundred requests for a hundred DIFFERENT
//     images cannot be collapsed and must not all decode at once.

// RenditionCacheBytes is how much memory rendered images may occupy.
//
// 64 MiB, in process. goen's whole architecture is one binary and one
// PostgreSQL, so this is not a cache tier — it is a bounded map that stops the
// same work being repeated. What a restart costs is one render per hot image,
// which is a few hundred milliseconds spread over the first page views.
//
// The persistent half — a media_renditions table keyed on (digest, width),
// written on first render and read before this — needs a migration and is not
// in this change.
const RenditionCacheBytes = 64 << 20

// RenderTimeout bounds one render.
//
// It reaches the stored-bytes read rather than the scaling: draw.CatmullRom does
// not look at a context, and the pixel work is already bounded by MaxPixels and
// by the slot count. What this stops is a stuck database read holding a slot for
// as long as the process lives.
const RenderTimeout = 30 * time.Second

// renderSlots is how many renditions may be produced at the same time.
//
// GOMAXPROCS, because the work is CPU-bound: more decodes in flight than there
// are processors finishes no sooner and costs a pixel buffer each. Requests over
// the bound WAIT rather than fail — a queue is slower and a 503 is a broken
// page — and the wait is bounded by RenderTimeout.
var renderSlots = runtime.GOMAXPROCS(0)

// source reads a stored image's bytes.
//
// A function rather than the *Store itself, because this is the one thing the
// renderer needs from it and a narrower dependency is a shorter list of things
// that can be wrong. It is [Store.Bytes] in every wiring goen has.
type source func(ctx context.Context, digest string) (contentType string, data []byte, err error)

// renderer produces renditions, at most renderSlots at a time, at most once per
// (digest, width) while the answer is still in hand.
type renderer struct {
	read   source
	slots  chan struct{}
	flight singleflight.Group

	// mu guards the three fields below together — an entry, its position in the
	// eviction order and the running byte total are one invariant, which is a
	// mutex and not three atomics.
	mu    sync.Mutex
	order *list.List
	index map[string]*list.Element
	held  int
	limit int
}

// rendition is one rendered image, and its place in the eviction order.
type rendition struct {
	key         string
	contentType string
	data        []byte
}

// newRenderer returns a renderer over read.
func newRenderer(read source, slots, limit int) *renderer {
	if read == nil || slots < 1 || limit < 1 {
		panic("media: newRenderer requires a source, a slot count and a byte limit")
	}
	return &renderer{
		read:  read,
		slots: make(chan struct{}, slots),
		order: list.New(),
		index: make(map[string]*list.Element),
		limit: limit,
	}
}

// rendition answers with the bytes for digest at width.
//
// The cache is read before anything else, which is the whole point: a hit costs
// a map lookup and no database round trip at all, so a listing page of a hundred
// thumbnails is a hundred map lookups rather than a hundred reads and a hundred
// decodes.
func (r *renderer) rendition(ctx context.Context, digest string, width int) (contentType string, data []byte, err error) {
	key := digest + "/" + strconv.Itoa(width)
	if hitType, hitData, ok := r.cached(key); ok {
		return hitType, hitData, nil
	}

	// DoChan and not Do, so the CALLER can leave while the render finishes.
	// Somebody who closed the tab should stop occupying a goroutine; the work
	// they started still lands in the cache and still answers everybody who
	// joined behind them.
	result := r.flight.DoChan(key, func() (any, error) {
		// Checked again inside the flight. The request that rendered this may
		// have finished between the miss above and this call, and re-rendering
		// what is already in hand is the exact cost this exists to avoid.
		if hitType, hitData, ok := r.cached(key); ok {
			return &rendition{key: key, contentType: hitType, data: hitData}, nil
		}
		return r.render(ctx, key, digest, width)
	})

	select {
	case got := <-result:
		if got.Err != nil {
			return "", nil, got.Err
		}
		out, ok := got.Val.(*rendition)
		if !ok {
			return "", nil, errors.New("media: renderer produced something that is not a rendition")
		}
		return out.contentType, out.data, nil
	case <-ctx.Done():
		return "", nil, ctx.Err()
	}
}

// render does the work, once, for whoever asked first.
func (r *renderer) render(ctx context.Context, key, digest string, width int) (*rendition, error) {
	// Detached from the caller's cancellation and bounded on its own.
	//
	// This render answers every request for this URL and not only the one that
	// started it, so binding it to the first caller's context would let a
	// stranger closing their tab fail everybody who joined the flight behind
	// them — and they would all retry, which is the stampede this exists to
	// stop. Finishing an abandoned render costs one slot, paid once, and the
	// bytes go into the cache where the next request will find them.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), RenderTimeout)
	defer cancel()

	select {
	case r.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-r.slots }()

	contentType, data, err := r.read(ctx, digest)
	if err != nil {
		return nil, err
	}
	rendered, err := Resize(data, contentType, width)
	if err != nil {
		return nil, err
	}
	r.put(key, contentType, rendered)
	return &rendition{key: key, contentType: contentType, data: rendered}, nil
}

// cached reads an entry and marks it used.
func (r *renderer) cached(key string) (contentType string, data []byte, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	el, found := r.index[key]
	if !found {
		return "", nil, false
	}
	r.order.MoveToFront(el)
	got, isRendition := el.Value.(*rendition)
	if !isRendition {
		return "", nil, false
	}
	return got.contentType, got.data, true
}

// put stores an entry, evicting the least recently used until the total fits.
//
// The bytes are never copied and never written to after this: a rendition is
// produced once and handed out by reference to every reader, which is what makes
// a hit free. Nothing in the package mutates a stored slice.
func (r *renderer) put(key, contentType string, data []byte) {
	if len(data) > r.limit {
		// One entry larger than the whole cache would evict everything else and
		// then be evicted itself on the next insert — a cache that holds nothing
		// and does the eviction work anyway.
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if el, found := r.index[key]; found {
		r.order.MoveToFront(el)
		return
	}
	r.index[key] = r.order.PushFront(&rendition{key: key, contentType: contentType, data: data})
	r.held += len(data)

	for r.held > r.limit {
		oldest := r.order.Back()
		if oldest == nil {
			return
		}
		r.order.Remove(oldest)
		if evicted, isRendition := oldest.Value.(*rendition); isRendition {
			delete(r.index, evicted.key)
			r.held -= len(evicted.data)
		}
	}
}
