// Package media stores and serves uploaded images.
//
// Nothing the client says about a file is believed: what it is, is whatever Go's
// decoders make of it, and what is stored is goen's own re-encoding of the
// pixels rather than the uploaded bytes.
package media

import (
	"errors"
	"time"
)

// MaxUploadBytes is the largest file goen will read from a request.
const MaxUploadBytes = 8 << 20

// MaxPixels bounds what goen will decode.
const MaxPixels = 40_000_000

// MaxDimension bounds either side, matching media_objects_dimensions_sane.
const MaxDimension = 8000

// JPEGQuality is what goen re-encodes photographs at.
const JPEGQuality = 82

// CacheTTL is how long a served image may be held. Safe at a year because the
// URL is the content's own digest, so the bytes at it can never change.
const CacheTTL = 365 * 24 * time.Hour

// The errors a caller branches on.
var (
	// ErrNotAnImage is a file whose bytes no supported decoder accepts.
	ErrNotAnImage = errors.New("media: not a supported image")
	// ErrTooLarge is a file over MaxUploadBytes or an image over MaxPixels.
	ErrTooLarge = errors.New("media: the image is too large")
	// ErrNotFound is a digest with no row.
	ErrNotFound = errors.New("media: no such image")
)

// Object is a stored image.
type Object struct {
	// Digest is the sha256 of the stored bytes: the id, the URL and the ETag.
	Digest      string
	ContentType string
	Width       int32
	Height      int32
	ByteSize    int32
}

// URL is where this image is served.
func (o Object) URL() string { return "/media/" + o.Digest }
