package media

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"

	"github.com/koopa0/goen/assets"
)

// digestPath is the only shape a media URL may have, checked before the
// database is touched.
var digestPath = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Handler serves stored images.
type Handler struct {
	store      *Store
	log        *slog.Logger
	renditions *renderer
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

// ReadUpload takes one image out of a multipart form, ignoring the client's
// filename and declared type.
func (h *Handler) ReadUpload(w http.ResponseWriter, r *http.Request, field string) (Object, error) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxUploadBytes)
	if err := r.ParseMultipartForm(1 << 20); err != nil { //nolint:gosec // G120: bounded by MaxBytesReader above
		return Object{}, ErrTooLarge
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }() //nolint:errcheck // best-effort temp cleanup

	file, _, err := r.FormFile(field)
	if err != nil {
		return Object{}, ErrNotAnImage
	}
	defer func() { _ = file.Close() }()

	obj, err := h.store.Put(r.Context(), file)
	if err != nil {
		return Object{}, err
	}
	return obj, nil
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
