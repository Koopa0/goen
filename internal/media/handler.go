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

// digestPath is the only shape a media URL may have.
//
// Validated before the database is touched, which means the path segment can
// never be anything but 64 hex characters — no traversal to reason about, no
// escaping to get right, and a scan of the table for junk that never runs.
var digestPath = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Handler serves stored images.
type Handler struct {
	store *Store
	log   *slog.Logger
	// renditions is what bounds the CPU a request can ask for. See render.go —
	// without it, serving a rendition is the one anonymous path in goen that
	// decodes and rescales an image on every request.
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
//
// # Why the headers are what they are
//
// Content-Type is the one goen chose when it re-encoded, never one derived from
// the request. nosniff stops a browser second-guessing it, which is what would
// otherwise let a file that is genuinely a JPEG but begins with "<html" be
// rendered as a page. Content-Disposition: inline with no filename means
// nothing attacker-controlled reaches a download dialog.
//
// The cache is immutable for a year because the URL is the content's digest:
// the bytes at this URL cannot change, so there is no invalidation problem to
// have. ETag is the digest for the same reason, which makes a revalidation a
// 304 with no body.
func (h *Handler) Serve(w http.ResponseWriter, r *http.Request) {
	digest := r.PathValue("digest")
	if !digestPath.MatchString(digest) {
		http.NotFound(w, r)
		return
	}

	// The rendition width, when the route carries one. Off the allowlist is a
	// 404 rather than a fallback to full size: silently serving something other
	// than what the URL named would put a 1600px image where the page reserved
	// 400, and the allowlist is what bounds the CPU an anonymous request can
	// ask for.
	width := 0
	if raw := r.PathValue("width"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || !assets.KnownWidth(parsed) {
			http.NotFound(w, r)
			return
		}
		width = parsed
	}

	// A conditional request can be answered without reading the bytes at all,
	// which for a page of thumbnails is the difference between one query and
	// twenty megabyte reads. The width is part of the tag: two renditions of
	// one image are different bytes and must not share a validator.
	etag := `"` + digest + renditionTag(width) + `"`
	if match := r.Header.Get("If-None-Match"); match == etag {
		writeCacheHeaders(w, etag)
		w.WriteHeader(http.StatusNotModified)
		return
	}

	// A rendition goes through the renderer, which caches it, collapses
	// concurrent requests for it and bounds how many may be produced at once.
	// Full size is a read and nothing else — no decode, no scale, no re-encode —
	// so it goes straight to the store and is bounded by the pool.
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
			// The caller left while the render was in flight. There is nobody to
			// answer and nothing went wrong.
			//
			// Canceled ONLY, and never DeadlineExceeded beside it on the same
			// reasoning: the render detaches from the caller with
			// context.WithoutCancel and imposes its own RenderTimeout, so a
			// caller who leaves produces Canceled and a deadline can only be
			// goen's own — every slot busy, or a stuck read of media_objects.
			//
			// Treating that deadline as "nobody is listening" returns having
			// written nothing, so net/http sends 200 with Content-Length: 0 and
			// no log line at all: a broken image, a CACHEABLE success status,
			// and silence. That is the media sweeper's mistake in a second
			// place — a server-side failure classified as the normal case.
		case errors.Is(err, context.DeadlineExceeded):
			// goen ran out of time on its own work. 503 rather than 500 because
			// it is load rather than a defect, and it must not be cached: a 200
			// with an empty body would be, and every later request would be
			// served the emptiness from a CDN.
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
	// These bytes came from media_objects, which only ever holds what goen itself
	// encoded from a decoded image — see Normalise. The Content-Type is goen's
	// own choice and nosniff is set above, so there is nothing here a browser
	// will treat as a document. No nolint for G705 either: it does not read this
	// shape as reflecting request data into a response, and a directive that
	// suppresses nothing is a claim nobody can check.
	_, _ = w.Write(data)
}

// writeCacheHeaders sets what both the 200 and the 304 need.
func writeCacheHeaders(w http.ResponseWriter, etag string) {
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age="+
		strconv.Itoa(int(CacheTTL.Seconds()))+", immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

// ReadUpload takes one image out of a multipart form.
//
// The body is bounded BEFORE anything reads it, and the field's own reader is
// bounded again: MaxBytesReader caps the whole request, and a multipart form
// can carry several parts, so the per-part limit is what stops one file
// consuming the entire allowance while the others starve.
//
// Nothing about the client's filename or declared type is used. The returned
// object is whatever goen made of the pixels.
func (h *Handler) ReadUpload(w http.ResponseWriter, r *http.Request, field string) (Object, error) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxUploadBytes)
	// The in-memory ceiling for multipart parsing; anything larger spills to a
	// temp file that ParseMultipartForm cleans up. It is not a size limit —
	// MaxBytesReader above is.
	// G120 asks for a bound. MaxBytesReader on the line above is that bound:
	// this argument is the in-memory ceiling before parts spill to a temp file,
	// not a limit on the request.
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
//
// Exposed on the Handler because that is what the back office already holds —
// internal/admin takes the media pipeline as a *Handler rather than a *Store,
// so it can read an upload without also being able to write one directly.
func (h *Handler) Object(ctx context.Context, digest string) (Object, error) {
	return h.store.Object(ctx, digest)
}

// Recent is the picker's list, for the same reason.
func (h *Handler) Recent(ctx context.Context) ([]Object, error) {
	return h.store.Recent(ctx)
}
