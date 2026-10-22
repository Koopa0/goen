package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
)

// These tests hold that OpenUpload says why a multipart parse failed. Every
// failure used to be ErrTooLarge, so a slow uplink told staff to shrink a file
// that was never too large.

// multipartBody is a form with one file part of fileBytes bytes.
func multipartBody(t *testing.T, fileBytes int) (body []byte, contentType string) {
	t.Helper()
	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	part, err := form.CreateFormFile("image", "photo.jpg")
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write(bytes.Repeat([]byte{0xff}, fileBytes)); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := form.Close(); err != nil {
		t.Fatalf("close form: %v", err)
	}
	return buf.Bytes(), form.FormDataContentType()
}

// failingReader serves prefix and then fails with err, as a connection does
// when its read deadline passes or the peer resets it.
type failingReader struct {
	prefix []byte
	err    error
}

func (f *failingReader) Read(p []byte) (int, error) {
	if len(f.prefix) == 0 {
		return 0, f.err
	}
	n := copy(p, f.prefix)
	f.prefix = f.prefix[n:]
	return n, nil
}

// timeoutError is what a connection read returns once its deadline has passed.
type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

var _ net.Error = timeoutError{}

func openUpload(t *testing.T, ctx context.Context, body io.Reader, contentType string) (*Upload, error) {
	t.Helper()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/products/x/images", body)
	req.Header.Set("Content-Type", contentType)
	upload, err := (&Handler{}).OpenUpload(httptest.NewRecorder(), req, "image")
	if upload != nil {
		t.Cleanup(upload.Close)
	}
	return upload, err
}

// TestHandler_OpenUploadOverCap checks that an upload over the cap is too large.
func TestHandler_OpenUploadOverCap(t *testing.T) {
	t.Parallel()
	body, contentType := multipartBody(t, MaxUploadBytes+1)

	_, err := openUpload(t, t.Context(), bytes.NewReader(body), contentType)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("OpenUpload of %d bytes = %v, want ErrTooLarge", len(body), err)
	}
	if _, ok := errors.AsType[*http.MaxBytesError](err); !ok {
		t.Errorf("OpenUpload error %v does not carry the *http.MaxBytesError that caused it", err)
	}
	if got := UploadNotice(err); got != i18n.KeyAdminNoticeTooBig {
		t.Errorf("notice = %q, want the too-large notice", got)
	}
}

// TestHandler_OpenUploadStopsArriving checks that a body that stops arriving is
// incomplete, not too large.
func TestHandler_OpenUploadStopsArriving(t *testing.T) {
	t.Parallel()
	body, contentType := multipartBody(t, 64<<10) // 1/128 of the cap
	prefix := body[:len(body)/2]

	for _, tt := range []struct {
		name string
		err  error
	}{
		{name: "server read timeout", err: &net.OpError{Op: "read", Net: "tcp", Err: timeoutError{}}},
		{name: "bare timeout", err: timeoutError{}},
		{name: "connection deadline", err: fmt.Errorf("read tcp: %w", os.ErrDeadlineExceeded)},
		{name: "connection reset", err: &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}},
		{name: "body shorter than its length", err: io.ErrUnexpectedEOF},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := openUpload(t, t.Context(), &failingReader{prefix: prefix, err: tt.err}, contentType)

			if errors.Is(err, ErrTooLarge) {
				t.Fatalf("OpenUpload = %v: a body of %d bytes under a cap of %d is called too large", err, len(body), MaxUploadBytes)
			}
			if !errors.Is(err, ErrUploadIncomplete) {
				t.Fatalf("OpenUpload = %v, want ErrUploadIncomplete", err)
			}
			if !errors.Is(err, tt.err) {
				t.Errorf("OpenUpload = %v, which hides its cause %v from the log", err, tt.err)
			}
			if !IsRefusal(err) {
				t.Error("an incomplete upload is not a refusal, so the back office would answer 500 instead of a notice")
			}
			if got := UploadNotice(err); got != i18n.KeyAdminNoticeUploadIncomplete {
				t.Errorf("notice = %q, want the incomplete-upload notice", got)
			}
		})
	}
}

// TestHandler_OpenUploadCutOff checks that a form cut off inside its file is
// incomplete. The stream ended cleanly part-way through the file part, as when a
// client gives up and the body is short. The multipart reader reports that as an
// unexpected EOF, the same as a connection that dropped, and the remedy is the
// same.
func TestHandler_OpenUploadCutOff(t *testing.T) {
	t.Parallel()
	body, contentType := multipartBody(t, 64<<10)

	_, err := openUpload(t, t.Context(), bytes.NewReader(body[:len(body)/2]), contentType)
	if errors.Is(err, ErrTooLarge) {
		t.Fatalf("OpenUpload = %v: a truncated body is called too large", err)
	}
	if !errors.Is(err, ErrUploadIncomplete) {
		t.Fatalf("OpenUpload = %v, want ErrUploadIncomplete", err)
	}
	if got := UploadNotice(err); got != i18n.KeyAdminNoticeUploadIncomplete {
		t.Errorf("notice = %q, want the incomplete-upload notice", got)
	}
}

// TestHandler_OpenUploadMalformed checks that a malformed body is unreadable, not
// too large.
func TestHandler_OpenUploadMalformed(t *testing.T) {
	t.Parallel()
	body, contentType := multipartBody(t, 64<<10)

	for _, tt := range []struct {
		name        string
		body        []byte
		contentType string
	}{
		{name: "no closing boundary", body: body[:len(body)-len("--\r\n")], contentType: contentType},
		{name: "not multipart", body: []byte("plain text"), contentType: "text/plain"},
		{name: "no boundary", body: body, contentType: "multipart/form-data"},
		{name: "wrong boundary", body: body, contentType: "multipart/form-data; boundary=elsewhere"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := openUpload(t, t.Context(), bytes.NewReader(tt.body), tt.contentType)

			if errors.Is(err, ErrTooLarge) {
				t.Fatalf("OpenUpload = %v: a malformed body is called too large", err)
			}
			if !errors.Is(err, ErrUploadUnreadable) {
				t.Fatalf("OpenUpload = %v, want ErrUploadUnreadable", err)
			}
			if !IsRefusal(err) {
				t.Error("an unreadable upload is not a refusal, so the back office would answer 500 instead of a notice")
			}
			if got := UploadNotice(err); got != i18n.KeyAdminNoticeUploadUnreadable {
				t.Errorf("notice = %q, want the unreadable-upload notice", got)
			}
		})
	}
}

// TestHandler_OpenUploadAbandoned checks that an abandoned upload has no notice.
func TestHandler_OpenUploadAbandoned(t *testing.T) {
	t.Parallel()
	body, contentType := multipartBody(t, 64<<10)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	for _, tt := range []struct {
		name        string
		body        io.Reader
		contentType string
	}{
		{name: "read cancelled", body: &failingReader{prefix: body[:len(body)/2], err: context.Canceled}, contentType: contentType},
		{name: "cancelled before an unreadable body", body: strings.NewReader("plain text"), contentType: "multipart/form-data"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := openUpload(t, ctx, tt.body, tt.contentType)
			if !errors.Is(err, ErrUploadAborted) {
				t.Fatalf("OpenUpload of a cancelled request = %v, want ErrUploadAborted", err)
			}
			if IsRefusal(err) {
				t.Error("an abandoned upload is a refusal, so a notice would be rendered to nobody")
			}
		})
	}
}

// TestHandler_OpenUploadSlowServer checks that a slow small upload through a real
// server is incomplete. It is the reproduction from the report: a real http.Server
// whose ReadTimeout passes before a small body has arrived. The doubles above
// stand in for its errors; this holds that they are the errors a server really
// hands the handler.
func TestHandler_OpenUploadSlowServer(t *testing.T) {
	t.Parallel()
	body, contentType := multipartBody(t, 64<<10)

	got := make(chan error, 1)
	h := &Handler{}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upload, err := h.OpenUpload(w, r, "image")
		upload.Close()
		got <- err
	}))
	srv.Config.ReadTimeout = 200 * time.Millisecond
	srv.Start()
	t.Cleanup(srv.Close)

	pr, pw := io.Pipe()
	t.Cleanup(func() { pr.Close() })
	go func() {
		defer pw.Close()
		for rest := body; len(rest) > 0; {
			n := min(4<<10, len(rest))
			if _, err := pw.Write(rest[:n]); err != nil {
				return
			}
			rest = rest[n:]
			time.Sleep(40 * time.Millisecond) // 16 chunks take over 600 ms
		}
	}()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL, pr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	go func() {
		if resp, err := srv.Client().Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}()

	select {
	case err := <-got:
		if errors.Is(err, ErrTooLarge) {
			t.Fatalf("OpenUpload = %v: a %d-byte upload that was too slow is called too large", err, len(body))
		}
		if !errors.Is(err, ErrUploadIncomplete) {
			t.Fatalf("OpenUpload = %v, want ErrUploadIncomplete", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the handler never saw the read fail")
	}
}

// TestUploadNoticeDistinctCauses checks that each upload cause says something
// different. It holds the copy: only the real size violation mentions a size, and
// each cause reads differently in both languages.
func TestUploadNoticeDistinctCauses(t *testing.T) {
	t.Parallel()
	for _, l := range i18n.Locales() {
		ctx := i18n.WithLocale(t.Context(), l)
		seen := map[string]error{}
		for _, err := range []error{ErrTooLarge, ErrUploadIncomplete, ErrUploadUnreadable} {
			text := i18n.T(ctx, UploadNotice(err))
			if prev, dup := seen[text]; dup {
				t.Errorf("%s: %v and %v show the same notice %q", l, prev, err, text)
			}
			seen[text] = err
			if mentionsSize := strings.Contains(text, "8 MB"); mentionsSize != errors.Is(err, ErrTooLarge) {
				t.Errorf("%s: notice for %v = %q, mentions the size cap = %t", l, err, text, mentionsSize)
			}
		}
	}
}
