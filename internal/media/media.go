// Package media stores and serves uploaded images.
//
// Nothing the client says about a file is believed: the decoders decide what it
// is, and what is stored is goen's own re-encoding rather than the upload.
package media

import (
	"errors"
	"time"
)

// MaxUploadBytes is the largest file goen will read from a request.
const MaxUploadBytes = 8 << 20

// MaxStoredBytes bounds goen's normalized output as well as the wire input.
// Decoding and re-encoding can expand data, so the input cap cannot imply this.
const MaxStoredBytes = 8 << 20

// MaxPixels bounds what goen will decode.
const MaxPixels = 40_000_000

// MaxDecodedBytes bounds the memory one decode may hold, read from the headers
// before any pixel is allocated. MaxPixels alone cannot: a 16-bit PNG holds
// eight bytes a pixel, and a progressive JPEG keeps every coefficient beside
// its pixels, so forty million pixels from a file of a few hundred kilobytes
// can cost hundreds of megabytes.
//
// At 128 MiB the largest square pictures it lets through are, in megapixels:
// 40, where MaxPixels binds first, for a grey or YCbCr baseline JPEG, a
// paletted PNG, a GIF and a lossy WebP; 33.5 for any other 8-bit PNG; 19.8 for
// a lossless WebP and a lossy one carrying metadata; 19.1 for an RGB JPEG; 16.7
// for a 16-bit or an interlaced 8-bit PNG and a CMYK JPEG; 14.8 for a
// progressive 4:2:0 JPEG; 14.5 for a WebP with alpha; 8.9 for a progressive
// 4:4:4 JPEG; and 8.3 for an interlaced 16-bit PNG.
const MaxDecodedBytes = 128 << 20

// MaxDimension bounds either side, matching media_objects_dimensions_sane.
const MaxDimension = 8000

// MaxStoredSide is the longest side goen keeps. Larger uploads are scaled down
// before encoding: no page shows more than this, and the original would only
// cost database space and memory to decode again for every rendition.
const MaxStoredSide = 2400

// JPEGQuality is what goen re-encodes photographs at.
const JPEGQuality = 82

// CacheTTL is how long a served image may be held. Safe at a year because the
// URL is the content's own digest, so the bytes at it can never change.
const CacheTTL = 365 * 24 * time.Hour

var (
	// ErrNotAnImage is a file whose bytes no supported decoder accepts.
	ErrNotAnImage = errors.New("media: not a supported image")
	// ErrTooLarge is a file over MaxUploadBytes, or an image over MaxPixels or
	// MaxDecodedBytes.
	ErrTooLarge = errors.New("media: the image is too large")
	// ErrBusy is an upload refused because every upload slot is decoding.
	ErrBusy = errors.New("media: every upload slot is busy")
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
