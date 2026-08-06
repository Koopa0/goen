// Package media stores and serves uploaded images.
//
// # The threat model
//
// An upload endpoint is the classic hole, so the rules here are absolute rather
// than best-effort:
//
//   - Nothing the client says is believed. Not the filename, not the
//     Content-Type header, not the extension. The only thing that decides what
//     a file is, is whether Go's image decoders can decode it.
//   - The bytes goen stores are the bytes goen PRODUCED. An accepted upload is
//     decoded to a pixel buffer and re-encoded from that buffer, which destroys
//     everything that is not pixels: an HTML polyglot that a browser would
//     sniff as a page, a PHP tag, EXIF holding a payload or a customer's home
//     coordinates, a trailing ZIP. There is nothing left to sanitise because
//     nothing came through.
//   - Size is bounded before the body is read, and dimensions are bounded after
//     the header is parsed but before the pixels are allocated. A 200-byte PNG
//     can declare 50000x50000 and cost 10GB to decode; that is why the second
//     bound exists and why it is not the same check as the first.
//   - What is served is served from an origin-safe content type with
//     nosniff, never as anything a browser will execute.
package media

import (
	"errors"
	"time"
)

// MaxUploadBytes is the largest file goen will read from a request.
//
// 8 MiB. A product photograph off a modern phone is 3–5 MB; anything much above
// that is a mistake or an attempt. It is enforced by http.MaxBytesReader, so
// the connection is cut rather than the memory allocated.
const MaxUploadBytes = 8 << 20

// MaxPixels bounds what goen will decode.
//
// 40 megapixels — comfortably above any real photograph, and far below what a
// decompression bomb needs to hurt. Checked against the image HEADER before the
// pixel buffer is allocated, which is the only point at which checking helps: a
// 200-byte PNG declaring 50000x50000 passes every byte-size limit there is.
const MaxPixels = 40_000_000

// MaxDimension bounds either side, matching media_objects_dimensions_sane.
//
// A 1x60000 image is under MaxPixels and is still not a product photo.
const MaxDimension = 8000

// JPEGQuality is what goen re-encodes photographs at.
//
// 82 is the usual point where the next increment costs bytes and buys nothing a
// person can see.
const JPEGQuality = 82

// CacheTTL is how long a served image may be held.
//
// A year, and immutable. Safe because the URL is the content's own digest: the
// bytes at a given URL can never change, so there is no invalidation problem to
// have. This is the whole reason the table is content-addressed.
const CacheTTL = 365 * 24 * time.Hour

// The errors a caller branches on.
var (
	// ErrNotAnImage is a file whose bytes no supported decoder accepts. It is
	// deliberately the same answer for "a text file", "a corrupt JPEG" and "a
	// TIFF": the caller's next step is identical, and naming the difference
	// tells an attacker which decoders are wired up.
	ErrNotAnImage = errors.New("media: not a supported image")
	// ErrTooLarge is a file over MaxUploadBytes or an image over MaxPixels.
	ErrTooLarge = errors.New("media: the image is too large")
	// ErrNotFound is a digest with no row.
	ErrNotFound = errors.New("media: no such image")
)

// Object is a stored image.
type Object struct {
	// Digest is the sha256 of Bytes, lowercase hex. It is the id, the URL path
	// segment, and the ETag.
	Digest      string
	ContentType string
	Width       int32
	Height      int32
	ByteSize    int32
}

// URL is where this image is served.
func (o Object) URL() string { return "/media/" + o.Digest }
