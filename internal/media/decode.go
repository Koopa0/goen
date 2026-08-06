package media

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"

	// Registered for their decoders only. goen re-encodes everything it accepts
	// into JPEG or PNG, so a GIF or WebP upload is read and then written back
	// as one of those two — the input format is a convenience, the output
	// format is a decision.
	_ "image/gif"

	_ "golang.org/x/image/webp"
)

// Normalise reads an upload and returns the bytes goen will store.
//
// The returned bytes are goen's own encoding of the decoded pixels, never the
// caller's file. That is the security property this function exists for: an
// upload that decodes as an image and is re-encoded cannot carry anything that
// is not pixels — no polyglot, no EXIF, no appended archive.
//
// It is also why the digest is taken AFTER re-encoding: the identity of a
// stored image is the identity of what goen serves, so two different uploads of
// the same photograph deduplicate only if what comes out is byte-identical.
func Normalise(r io.Reader) (obj Object, data []byte, err error) {
	// Read once into memory. The caller has already bounded the reader with
	// http.MaxBytesReader, so this cannot be unbounded — and both the header
	// check and the full decode need to start from the beginning.
	raw, err := io.ReadAll(r)
	if err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return Object{}, nil, ErrNotAnImage
		}
		// A MaxBytesReader overrun arrives here. It is the caller's limit that
		// was hit, so say so rather than reporting a read failure.
		return Object{}, nil, fmt.Errorf("%w: %s", ErrTooLarge, err.Error())
	}
	if len(raw) == 0 {
		return Object{}, nil, ErrNotAnImage
	}

	// The HEADER first, before any pixel buffer exists. DecodeConfig reads only
	// enough to learn the dimensions, which is what makes a decompression bomb
	// cheap to refuse: a 200-byte PNG declaring 50000x50000 is rejected here for
	// the price of parsing its header.
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return Object{}, nil, ErrNotAnImage
	}
	if boundsErr := boundsOK(cfg.Width, cfg.Height); boundsErr != nil {
		return Object{}, nil, boundsErr
	}

	img, _, decodeErr := image.Decode(bytes.NewReader(raw))
	if decodeErr != nil {
		// A header that parses and pixels that do not is a truncated or
		// malformed file, and it is not goen's job to guess the rest.
		return Object{}, nil, ErrNotAnImage
	}

	encoded, contentType, err := encode(img, format)
	if err != nil {
		return Object{}, nil, err
	}

	// Re-checked against the DECODED bounds, not only the header's. A decoder
	// that produced something other than the header declared would otherwise
	// write a row past media_objects_dimensions_sane, and the constraint would
	// refuse it with a message about the schema rather than about the upload.
	b := img.Bounds()
	if boundsErr := boundsOK(b.Dx(), b.Dy()); boundsErr != nil {
		return Object{}, nil, boundsErr
	}

	sum := sha256.Sum256(encoded)
	return Object{
		Digest:      hex.EncodeToString(sum[:]),
		ContentType: contentType,
		// Safe: boundsOK just bounded both at MaxDimension, and the encoded
		// length is bounded by MaxUploadBytes several times over.
		Width:    int32(b.Dx()),       //nolint:gosec // G115: bounded by boundsOK above
		Height:   int32(b.Dy()),       //nolint:gosec // G115: bounded by boundsOK above
		ByteSize: int32(len(encoded)), //nolint:gosec // G115: bounded by MaxUploadBytes
	}, encoded, nil
}

// boundsOK refuses an image that is too big to decode safely.
func boundsOK(w, h int) error {
	if w <= 0 || h <= 0 {
		return ErrNotAnImage
	}
	if w > MaxDimension || h > MaxDimension {
		return ErrTooLarge
	}
	// Multiplied as int64: on a 32-bit build w*h of two in-range values
	// overflows, and an overflowed product compares as small.
	if int64(w)*int64(h) > MaxPixels {
		return ErrTooLarge
	}
	return nil
}

// encode writes the decoded pixels back out in one of goen's two formats.
//
// PNG in, PNG out — a screenshot or a logo with flat colour and hard edges is
// destroyed by JPEG, and a transparent background becomes black. Everything
// else becomes JPEG, because a photograph as PNG is several times the bytes for
// no visible gain.
//
// The choice is made from the DECODED format, not from a client-supplied type.
func encode(img image.Image, format string) (data []byte, contentType string, err error) {
	var buf bytes.Buffer
	if format == "png" {
		enc := png.Encoder{CompressionLevel: png.BestCompression}
		if err := enc.Encode(&buf, img); err != nil {
			return nil, "", fmt.Errorf("re-encode png: %w", err)
		}
		return buf.Bytes(), "image/png", nil
	}
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: JPEGQuality}); err != nil {
		return nil, "", fmt.Errorf("re-encode jpeg: %w", err)
	}
	return buf.Bytes(), "image/jpeg", nil
}
