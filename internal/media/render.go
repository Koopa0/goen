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

const RenditionCacheBytes = 64 << 20

// RenderTimeout bounds one render. It reaches the stored-bytes read rather than
// the scaling: draw.CatmullRom does not look at a context.
const RenderTimeout = 30 * time.Second

// maxRenderSlots bounds renderSlots whatever the core count. A render holds a
// decoded source of at most MaxStoredSide square, its width-scaled
// intermediate, the rendition and one band of scaler scratch: about 83 MB at
// the widest rendition of a 16-bit source, which Normalise keeps at 16 bits.
// Anonymous visitors choose when renders run, so the bound is on memory, and
// four is about 330 MB.
const maxRenderSlots = 4

// renderSlots is how many renditions may be produced at the same time. Requests
// over the bound wait rather than fail.
var renderSlots = renderSlotsFor(runtime.GOMAXPROCS(0))

func renderSlotsFor(procs int) int {
	return max(1, min(procs, maxRenderSlots))
}

type source func(ctx context.Context, digest string) (contentType string, data []byte, err error)

// renderer produces renditions, at most renderSlots at a time, at most once per
// (digest, width) while the answer is still in hand.
type renderer struct {
	read   source
	slots  chan struct{}
	flight singleflight.Group

	// mu guards the four fields below together: an entry, its place in the
	// eviction order and the running byte total are one invariant.
	mu    sync.Mutex
	order *list.List
	index map[string]*list.Element
	held  int
	limit int
}

type rendition struct {
	key         string
	contentType string
	data        []byte
}

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

func (r *renderer) rendition(ctx context.Context, digest string, width int) (contentType string, data []byte, err error) {
	key := digest + "/" + strconv.Itoa(width)
	if hitType, hitData, ok := r.cached(key); ok {
		return hitType, hitData, nil
	}

	// DoChan and not Do, so the caller can leave while the render finishes.
	result := r.flight.DoChan(key, func() (any, error) {
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

func (r *renderer) render(ctx context.Context, key, digest string, width int) (*rendition, error) {
	// Detached from the caller: this render answers everybody who joined the
	// flight, so the first caller leaving must not fail the rest.
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
// Stored slices are handed out by reference and never written to again.
func (r *renderer) put(key, contentType string, data []byte) {
	if len(data) > r.limit {
		// It would evict everything else and then be evicted itself.
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
