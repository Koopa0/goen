package media

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"regexp"
	"strconv"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/web"
)

// digestPath is the only shape a media URL may have, checked before the
// database is touched.
var digestPath = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Handler serves stored images.
type Handler struct {
	store      *Store
	log        *slog.Logger
	renditions *renderer
	uploads    *uploader
}

// NewHandler returns a Handler over store.
func NewHandler(store *Store, log *slog.Logger) *Handler {
	if store == nil || log == nil {
		panic("media: NewHandler requires a store and a logger")
	}
	return &Handler{
		store:      store,
		log:        log,
		renditions: newRenderer(store.Bytes, renderSlots, RenditionCacheBytes),
		uploads:    newUploader(store.Put, uploadSlots),
	}
}

// Serve answers GET /media/{digest}.
func (h *Handler) Serve(w http.ResponseWriter, r *http.Request) {
	digest := r.PathValue("digest")
	if !digestPath.MatchString(digest) {
		http.NotFound(w, r)
		return
	}

	// Off the allowlist is a 404 rather than a fallback to full size: the
	// allowlist is what bounds the CPU an anonymous request can ask for.
	width := 0
	if raw := r.PathValue("width"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || !assets.KnownWidth(parsed) {
			http.NotFound(w, r)
			return
		}
		width = parsed
	}

	// The width is part of the tag: two renditions of one image are different
	// bytes and must not share a validator.
	etag := `"` + digest + renditionTag(width) + `"`
	if match := r.Header.Get("If-None-Match"); match == etag {
		writeCacheHeaders(w, etag)
		w.WriteHeader(http.StatusNotModified)
		return
	}

	var (
		contentType string
		data        []byte
		err         error
	)
	if width > 0 {
		contentType, data, err = h.renditions.rendition(r.Context(), digest, width)
	} else {
		contentType, data, err = h.store.Bytes(r.Context(), digest)
	}
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			http.NotFound(w, r)
		case errors.Is(err, context.Canceled):
			// The caller left; nobody is listening. Canceled ONLY: the render
			// detaches from the caller, so a deadline here is goen's own
			// failure and an empty 200 would be cached as a success.
		case errors.Is(err, context.DeadlineExceeded):
			h.log.ErrorContext(r.Context(), "image render timed out", "digest", digest,
				"width", width, "timeout", RenderTimeout)
			w.Header().Set("Retry-After", "1")
			http.Error(w, "503", http.StatusServiceUnavailable)
		default:
			h.log.ErrorContext(r.Context(), "serve image", "error", err,
				"digest", digest, "width", width)
			http.Error(w, "500", http.StatusInternalServerError)
		}
		return
	}

	writeCacheHeaders(w, etag)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	// No filename: a download dialog must not show anything a user chose.
	w.Header().Set("Content-Disposition", "inline")
	_, _ = w.Write(data)
}

// writeCacheHeaders sets what both the 200 and the 304 need.
func writeCacheHeaders(w http.ResponseWriter, etag string) {
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age="+
		strconv.Itoa(int(CacheTTL.Seconds()))+", immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

// ReadUpload takes one image out of a multipart form and stores it. A form
// with no file in field answers ErrNotAnImage.
func (h *Handler) ReadUpload(w http.ResponseWriter, r *http.Request, field string) (Object, error) {
	upload, err := h.OpenUpload(w, r, field)
	if err != nil {
		return Object{}, err
	}
	if upload == nil {
		return Object{}, ErrNotAnImage
	}
	defer upload.Close()
	return upload.Store(r.Context())
}

// OpenUpload parses a multipart form of at most MaxUploadBytes and hands back
// the file in field, ignoring the client's filename and declared type. Nothing
// is decoded or stored until Store, so a caller can refuse the rest of the form
// first. A form with no file there answers nil and no error. The form's other
// values stay readable after Close, which removes its temporary files.
func (h *Handler) OpenUpload(w http.ResponseWriter, r *http.Request, field string) (*Upload, error) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxUploadBytes)
	if err := r.ParseMultipartForm(1 << 20); err != nil { //nolint:gosec // G120: bounded by MaxBytesReader above
		return nil, ErrTooLarge
	}
	// The caller reads the text fields beside the image from this same parse,
	// and web.ParseForm is not on this path to refuse them.
	if err := web.CheckFormText(r.Form); err != nil {
		_ = r.MultipartForm.RemoveAll() //nolint:errcheck // best-effort temp cleanup
		return nil, err
	}
	file, _, err := r.FormFile(field)
	if err != nil {
		_ = r.MultipartForm.RemoveAll() //nolint:errcheck // best-effort temp cleanup
		return nil, nil
	}
	return &Upload{file: file, form: r.MultipartForm, uploads: h.uploads}, nil
}

// Upload is one file a parsed multipart form carried, not yet decoded.
type Upload struct {
	file    multipart.File
	form    *multipart.Form
	uploads *uploader
}

// Store decodes, re-encodes and stores the upload, or answers ErrBusy when
// every upload slot is decoding.
func (u *Upload) Store(ctx context.Context) (Object, error) {
	return u.uploads.store(ctx, u.file)
}

// Close releases the file and the form's temporary files. A nil Upload holds
// nothing.
func (u *Upload) Close() {
	if u == nil {
		return
	}
	_ = u.file.Close()
	_ = u.form.RemoveAll() //nolint:errcheck // best-effort temp cleanup
}

// uploadSlots is how many uploads may be decoded at once. Each can hold
// MaxDecodedBytes, and the back office has no reason to decode more than a
// couple of images at the same moment.
const uploadSlots = 2

// uploader normalises and stores uploads, at most uploadSlots at a time.
type uploader struct {
	// put is [Store.Put] in every wiring goen has.
	put   func(ctx context.Context, r io.Reader) (Object, error)
	slots chan struct{}
}

func newUploader(put func(context.Context, io.Reader) (Object, error), slots int) *uploader {
	if put == nil || slots < 1 {
		panic("media: newUploader requires a store and a slot count")
	}
	return &uploader{put: put, slots: make(chan struct{}, slots)}
}

// store runs put in a free slot, or answers ErrBusy at once rather than
// queueing: a queue is bounded only by how many requests arrive, and a staff
// member told to try again in a moment loses nothing.
func (u *uploader) store(ctx context.Context, r io.Reader) (Object, error) {
	select {
	case u.slots <- struct{}{}:
	default:
		return Object{}, ErrBusy
	}
	defer func() { <-u.slots }()
	return u.put(ctx, r)
}

// renditionTag distinguishes a rendition's validator from the original's.
func renditionTag(width int) string {
	if width == 0 {
		return ""
	}
	return "-" + strconv.Itoa(width)
}

// Object reads a stored image's metadata, for a caller attaching it somewhere.
func (h *Handler) Object(ctx context.Context, digest string) (Object, error) {
	return h.store.Object(ctx, digest)
}

// Recent is the back office picker's list.
func (h *Handler) Recent(ctx context.Context) ([]Object, error) {
	return h.store.Recent(ctx)
}
