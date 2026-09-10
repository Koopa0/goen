package media

import (
	"bytes"
	"context"
	"image"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
)

// aDigest is a well-formed digest; the route refuses anything else.
const aDigest = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

// storedPNG is a stored image of the given size, as the store hands it back.
func storedPNG(t *testing.T, w, h int) []byte {
	t.Helper()

	_, data, err := Normalise(bytes.NewReader(pngBytes(t, w, h)))
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	return data
}

func serveRendition(t *testing.T, h *Handler, digest, width string) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/media/"+digest+"/"+width, http.NoBody)
	r.SetPathValue("digest", digest)
	r.SetPathValue("width", width)
	w := httptest.NewRecorder()
	h.Serve(w, r)
	return w
}

// Asserted through Serve: a cache the handler does not call does nothing.
func TestARenditionIsRenderedOnceAndThenServedFromMemory(t *testing.T) {
	t.Parallel()

	var reads atomic.Int64
	stored := storedPNG(t, 1200, 900)
	h := &Handler{
		log: slog.New(slog.DiscardHandler),
		renditions: newRenderer(func(context.Context, string) (string, []byte, error) {
			reads.Add(1)
			return "image/png", stored, nil
		}, 4, 1<<20),
	}

	first := serveRendition(t, h, aDigest, "400")
	second := serveRendition(t, h, aDigest, "400")

	for i, w := range []*httptest.ResponseRecorder{first, second} {
		if w.Code != http.StatusOK {
			t.Fatalf("request %d answered %d", i+1, w.Code)
		}
	}
	if got := reads.Load(); got != 1 {
		t.Errorf("the stored image was read %d times for two requests to one URL, "+
			"want 1 — every request is decoding and rescaling again", got)
	}
	if !bytes.Equal(first.Body.Bytes(), second.Body.Bytes()) {
		t.Error("two requests for one rendition returned different bytes")
	}

	// And what was cached is the RENDITION, not the stored image.
	cfg, _, err := image.DecodeConfig(bytes.NewReader(second.Body.Bytes()))
	if err != nil {
		t.Fatalf("decode the served rendition: %v", err)
	}
	if cfg.Width != 400 {
		t.Errorf("the served rendition is %d wide, want 400", cfg.Width)
	}
	if got := second.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type is %q, want image/png — the cached entry lost its type", got)
	}
}

// A cold URL is where the stampede is. synctest, because a sleep would pass on
// a fast machine and lie on a slow one.
func TestConcurrentRequestsForOneRenditionRenderItOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const callers = 20

		var reads atomic.Int64
		release := make(chan struct{})
		stored := storedPNG(t, 1200, 900)
		r := newRenderer(func(context.Context, string) (string, []byte, error) {
			reads.Add(1)
			<-release
			return "image/png", stored, nil
		}, 8, 1<<20)

		got := make([][]byte, callers)
		errs := make([]error, callers)
		var wg sync.WaitGroup
		for i := range callers {
			wg.Go(func() {
				_, data, err := r.rendition(t.Context(), aDigest, 400)
				got[i], errs[i] = data, err
			})
		}

		// Everybody has arrived: one goroutine is inside the source and the rest
		// are waiting on its result.
		synctest.Wait()
		if n := reads.Load(); n != 1 {
			t.Errorf("%d renders started for %d simultaneous requests to one URL, "+
				"want 1 — a cold URL is still a stampede", n, callers)
		}

		close(release)
		wg.Wait()

		for i := range callers {
			if errs[i] != nil {
				t.Fatalf("caller %d: %v", i, errs[i])
			}
			if !bytes.Equal(got[i], got[0]) {
				t.Errorf("caller %d got different bytes from caller 0", i)
			}
		}
		if n := reads.Load(); n != 1 {
			t.Errorf("%d renders in total, want 1", n)
		}
	})
}

// Requests for DIFFERENT images, which singleflight cannot collapse, must not
// all decode at once: each holds a pixel buffer of up to forty million pixels.
func TestRendersAreBoundedInFlight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const slots = 2
		const callers = 8

		var inFlight, peak atomic.Int64
		release := make(chan struct{})
		stored := storedPNG(t, 1200, 900)
		r := newRenderer(func(_ context.Context, digest string) (string, []byte, error) {
			now := inFlight.Add(1)
			for {
				was := peak.Load()
				if now <= was || peak.CompareAndSwap(was, now) {
					break
				}
			}
			<-release
			inFlight.Add(-1)
			return "image/png", stored, nil
		}, slots, 8<<20)

		var wg sync.WaitGroup
		for i := range callers {
			// DISTINCT digests, so singleflight has nothing to collapse and the
			// slot count is the only thing holding them back.
			digest := aDigest[:60] + string(rune('a'+i)) + "bcd"
			wg.Go(func() {
				if _, _, err := r.rendition(t.Context(), digest, 400); err != nil {
					t.Errorf("render %s: %v", digest, err)
				}
			})
		}

		synctest.Wait()
		if got := inFlight.Load(); got != slots {
			t.Errorf("%d renders are in flight at once, want %d — %d requests for "+
				"different images each hold a decoded pixel buffer", got, slots, callers)
		}

		close(release)
		wg.Wait()
		if got := peak.Load(); got > slots {
			t.Errorf("%d renders ran at once at the peak, want at most %d", got, slots)
		}
	})
}

// An unbounded map of rendered images is a denial of service from the other side.
func TestTheCacheIsBoundedAndEvictsTheLeastRecentlyUsed(t *testing.T) {
	t.Parallel()

	const limit = 500
	r := newRenderer(func(context.Context, string) (string, []byte, error) {
		return "", nil, nil
	}, 1, limit)

	r.put("a", "image/png", make([]byte, 200))
	r.put("b", "image/png", make([]byte, 200))

	// Touching "a" makes "b" the least recently used, so the next insert must
	// take "b" — under a first-in policy it would take "a".
	if _, _, ok := r.cached("a"); !ok {
		t.Fatal("a was not stored")
	}
	r.put("c", "image/png", make([]byte, 200))

	for _, key := range []string{"a", "c"} {
		if _, _, ok := r.cached(key); !ok {
			t.Errorf("%q was evicted; the least recently used entry was %q", key, "b")
		}
	}
	if _, _, ok := r.cached("b"); ok {
		t.Error("b survived, so nothing was evicted and the cache is unbounded")
	}

	r.mu.Lock()
	held, entries := r.held, len(r.index)
	r.mu.Unlock()
	if held > limit {
		t.Errorf("the cache holds %d bytes, want at most %d", held, limit)
	}
	if held != 400 || entries != 2 {
		t.Errorf("the cache holds %d bytes in %d entries, want 400 in 2 — the "+
			"running total has drifted from what is actually stored", held, entries)
	}

	// One entry bigger than the whole cache is not stored at all.
	r.put("huge", "image/jpeg", make([]byte, limit+1))
	if _, _, ok := r.cached("huge"); ok {
		t.Error("an entry larger than the cache was stored")
	}
	if _, _, ok := r.cached("a"); !ok {
		t.Error("storing an oversized entry evicted what was already there")
	}
}
