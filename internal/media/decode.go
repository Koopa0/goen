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

	// Registered for their decoders only: goen re-encodes everything it accepts
	// as JPEG or PNG.
	_ "image/gif"

	_ "golang.org/x/image/webp"
)

// Normalise reads an upload and returns the bytes goen will store: its own
// encoding of the decoded pixels, digested after re-encoding. The caller must
// have bounded r, which is read into memory whole.
func Normalise(r io.Reader) (obj Object, data []byte, err error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return Object{}, nil, ErrNotAnImage
		}
		// A MaxBytesReader overrun arrives here.
		return Object{}, nil, fmt.Errorf("%w: %w", ErrTooLarge, err)
	}
	if len(raw) == 0 {
		return Object{}, nil, ErrNotAnImage
	}

	// The header first: a 200-byte PNG can declare 50000x50000, which no
	// byte-size limit can see and which costs 10GB to decode.
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return Object{}, nil, ErrNotAnImage
	}
	if boundsErr := boundsOK(cfg.Width, cfg.Height); boundsErr != nil {
		return Object{}, nil, boundsErr
	}

	img, _, decodeErr := image.Decode(bytes.NewReader(raw))
	if decodeErr != nil {
		return Object{}, nil, ErrNotAnImage
	}
	return normaliseDecoded(img, format, encode)
}

// normaliseDecoded bounds, encodes and digests already-decoded pixels.
func normaliseDecoded(
	img image.Image,
	format string,
	encoder func(image.Image, string) ([]byte, string, error),
) (obj Object, data []byte, err error) {
	// Re-check the DECODED bounds: a decoder that produced something other than
	// the header declared must not reach the encoder.
	b := img.Bounds()
	if boundsErr := boundsOK(b.Dx(), b.Dy()); boundsErr != nil {
		return Object{}, nil, boundsErr
	}

	encoded, contentType, err := encoder(img, format)
	if err != nil {
		return Object{}, nil, err
	}
	if err := storedSizeOK(len(encoded)); err != nil {
		return Object{}, nil, err
	}

	sum := sha256.Sum256(encoded)
	return Object{
		Digest:      hex.EncodeToString(sum[:]),
		ContentType: contentType,
		Width:       int32(b.Dx()),       //nolint:gosec // G115: bounded by boundsOK above
		Height:      int32(b.Dy()),       //nolint:gosec // G115: bounded by boundsOK above
		ByteSize:    int32(len(encoded)), //nolint:gosec // G115: bounded by MaxStoredBytes
	}, encoded, nil
}

func storedSizeOK(n int) error {
	if n <= 0 {
		return ErrNotAnImage
	}
	if n > MaxStoredBytes {
		return ErrTooLarge
	}
	return nil
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

// encode writes the decoded pixels back out as PNG or JPEG, chosen from the
// DECODED format rather than from a client-supplied type.
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
