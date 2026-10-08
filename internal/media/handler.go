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
	"time"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/web"
)

// digestPath is the only shape a media URL may have, checked before the
// database is touched.
var digestPath = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Original reads include pool acquisition and must finish before the server
// stops accepting response writes after 30 seconds.
const originalReadTimeout = 25 * time.Second

type Handler struct {
	store      *Store
	log        *slog.Logger
	renditions *renderer
	uploads    *uploader
}

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
		timeout     = RenderTimeout
	)
	if width > 0 {
		contentType, data, err = h.renditions.rendition(r.Context(), digest, width)
	} else {
		timeout = originalReadTimeout
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		contentType, data, err = h.store.Bytes(ctx, digest)
	}
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			http.NotFound(w, r)
		case errors.Is(err, context.Canceled):
			// The caller left; nobody is listening. Canceled ONLY: WriteTimeout
			// does not give the request a deadline, so a deadline here is goen's
			// own (RenderTimeout or originalReadTimeout), and an empty 200
			// would be cached as a success.
		case errors.Is(err, context.DeadlineExceeded):
			h.log.ErrorContext(r.Context(), "serve image timed out", "digest", digest,
				"width", width, "timeout", timeout)
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

func writeCacheHeaders(w http.ResponseWriter, etag string) {
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age="+
		strconv.Itoa(int(CacheTTL.Seconds()))+", immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func (h *Handler) StoreUpload(w http.ResponseWriter, r *http.Request, field string) (Object, error) {
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

type Upload struct {
	file    multipart.File
	form    *multipart.Form
	uploads *uploader
}

func (u *Upload) Store(ctx context.Context) (Object, error) {
	return u.uploads.store(ctx, u.file)
}

func (u *Upload) Close() {
	if u == nil {
		return
	}
	_ = u.file.Close()
	_ = u.form.RemoveAll() //nolint:errcheck // best-effort temp cleanup
}

// uploadSlots is how many uploads may be decoded at once. A slot holds the
// upload, up to MaxUploadBytes, and its decode, up to MaxDecodedBytes, and
// beside them the resize to MaxStoredSide: a band as tall as the picture at the
// stored width, up to 61 MB; the stored-size image, 23 MB; and the scaler's
// scratch, up to 33 MB. That is about 250 MB a slot, and the back office has no
// reason to decode more than a couple of images at the same moment.
const uploadSlots = 2

type uploader struct {
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

func renditionTag(width int) string {
	if width == 0 {
		return ""
	}
	return "-" + strconv.Itoa(width)
}

func (h *Handler) Object(ctx context.Context, digest string) (Object, error) {
	return h.store.Object(ctx, digest)
}

func (h *Handler) Recent(ctx context.Context) ([]Object, error) {
	return h.store.Recent(ctx)
}

// UploadNotice is the back-office notice for a refused upload. It names the
// size and the kind and nothing more: saying which decoder refused a file would
// tell an attacker which decoders are wired up.
func UploadNotice(err error) i18n.Key {
	if key, ok := uploadRefusalNotice(err); ok {
		return key
	}
	return i18n.KeyAdminNoticeUploadFailed
}

func IsRefusal(err error) bool {
	_, ok := uploadRefusalNotice(err)
	return ok
}

func uploadRefusalNotice(err error) (i18n.Key, bool) {
	switch {
	case errors.Is(err, ErrTooLarge):
		return i18n.KeyAdminNoticeTooBig, true
	case errors.Is(err, ErrNotAnImage):
		return i18n.KeyAdminNoticeNotImage, true
	case errors.Is(err, ErrLosslessWebP):
		return i18n.KeyAdminNoticeLosslessWebP, true
	case errors.Is(err, ErrBusy):
		return i18n.KeyAdminNoticeUploadBusy, true
	default:
		return "", false
	}
}
