package media

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
)

// TestUploadsBeyondTheSlotsAreRefused holds the bound on decodes at once. Each
// upload can hold MaxDecodedBytes while it decodes, so an unbounded number of
// simultaneous uploads is an unbounded amount of memory. synctest, because a
// sleep would pass on a fast machine and lie on a slow one.
func TestUploadsBeyondTheSlotsAreRefused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const slots = 2

		var inFlight, peak atomic.Int64
		release := make(chan struct{})
		u := newUploader(func(context.Context, io.Reader) (Object, error) {
			now := inFlight.Add(1)
			for {
				was := peak.Load()
				if now <= was || peak.CompareAndSwap(was, now) {
					break
				}
			}
			<-release
			inFlight.Add(-1)
			return Object{Digest: "stored"}, nil
		}, slots)

		var wg sync.WaitGroup
		for range slots {
			wg.Go(func() {
				if _, err := u.store(t.Context(), bytes.NewReader(nil)); err != nil {
					t.Errorf("an upload inside the slots: %v", err)
				}
			})
		}
		synctest.Wait()

		beyond := make(chan error, 1)
		wg.Go(func() {
			_, err := u.store(t.Context(), bytes.NewReader(nil))
			beyond <- err
		})
		synctest.Wait()
		if got := inFlight.Load(); got != slots {
			t.Errorf("%d uploads decoding at once, want %d: one more than the slots "+
				"holds another decode's memory", got, slots)
		}
		select {
		case err := <-beyond:
			if !errors.Is(err, ErrBusy) {
				t.Errorf("an upload beyond the slots = %v, want ErrBusy", err)
			}
		default:
			t.Error("an upload beyond the slots is waiting rather than refused")
		}

		close(release)
		wg.Wait()
		if got := peak.Load(); got > slots {
			t.Errorf("%d uploads decoded at once at the peak, want at most %d", got, slots)
		}
		if _, err := u.store(t.Context(), bytes.NewReader(nil)); err != nil {
			t.Errorf("an upload after the slots emptied: %v", err)
		}
	})
}

// TestAFullUploaderRefusesTheFormAndStoresNothing is the same bound seen from a
// request: StoreUpload goes through the slots, and a refusal names ErrBusy so
// the back office can say "try again" rather than "failed".
func TestAFullUploaderRefusesTheFormAndStoresNothing(t *testing.T) {
	t.Parallel()

	var stored atomic.Int64
	u := newUploader(func(context.Context, io.Reader) (Object, error) {
		stored.Add(1)
		return Object{}, nil
	}, 1)
	u.slots <- struct{}{} // another upload is decoding
	h := &Handler{uploads: u}

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("image", "photo.png")
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write(pngBytes(t, 8, 8)); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := form.Close(); err != nil {
		t.Fatalf("close form: %v", err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/products/x/images", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())

	if _, err := h.StoreUpload(httptest.NewRecorder(), req, "image"); !errors.Is(err, ErrBusy) {
		t.Errorf("StoreUpload with every slot taken = %v, want ErrBusy", err)
	}
	if n := stored.Load(); n != 0 {
		t.Errorf("%d images stored while every slot was taken, want 0", n)
	}
}
