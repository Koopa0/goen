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

	"golang.org/x/image/draw"
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
	if format == "webp" {
		if webpErr := refuseLosslessWebP(raw); webpErr != nil {
			return Object{}, nil, webpErr
		}
	}
	if boundsErr := boundsOK(cfg.Width, cfg.Height); boundsErr != nil {
		return Object{}, nil, boundsErr
	}
	if decodeCost(raw, cfg, format) > MaxDecodedBytes {
		return Object{}, nil, ErrTooLarge
	}

	img, _, decodeErr := image.Decode(bytes.NewReader(raw))
	if decodeErr != nil {
		return Object{}, nil, ErrNotAnImage
	}
	orientation := 1
	if format == "jpeg" {
		orientation = exifOrientation(raw)
	}
	return normaliseDecoded(img, format, orientation, encode)
}

// normaliseDecoded bounds, orients, encodes and digests already-decoded pixels.
// The orientation is applied after the fit, so the copy it makes is of the
// stored size and not of a camera's full frame.
func normaliseDecoded(
	img image.Image,
	format string,
	orientation int,
	encoder func(image.Image, string) ([]byte, string, error),
) (obj Object, data []byte, err error) {
	// Re-check the DECODED bounds: a decoder that produced something other than
	// the header declared must not reach the encoder.
	b := img.Bounds()
	if boundsErr := boundsOK(b.Dx(), b.Dy()); boundsErr != nil {
		return Object{}, nil, boundsErr
	}

	img = orient(fitLongestSide(img, MaxStoredSide), orientation)
	b = img.Bounds()

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

const resizeBand = 64

// fitLongestSide scales img down, keeping its aspect ratio, so that neither side
// exceeds limit. An image already within it is returned as is: goen never
// upscales.
func fitLongestSide(img image.Image, limit int) image.Image {
	return fitLongestSideWith(img, limit, draw.CatmullRom.NewScaler)
}

func fitLongestSideWith(img image.Image, limit int, newScaler func(dw, dh, sw, sh int) draw.Scaler) image.Image {
	b := img.Bounds()
	long := max(b.Dx(), b.Dy())
	if long <= limit {
		return img
	}
	return scaleInBands(img, max(b.Dx()*limit/long, 1), max(b.Dy()*limit/long, 1), newScaler)
}

// scaleInBands draws img at w x h. It scales the width across bands of rows,
// then the height across bands of columns. x/image/draw's kernel scaler holds
// 32 bytes per destination column per source row, which in one call is 485 MB
// for a 6320px square scaled to 2400. Across a band whose other axis keeps its
// size, CatmullRom's weights are exactly 1 and 0, so the bands meet without a
// seam and the scaler's buffer is one band's worth.
func scaleInBands(img image.Image, w, h int, newScaler func(dw, dh, sw, sh int) draw.Scaler) *image.RGBA {
	b := img.Bounds()
	narrow := image.NewRGBA(image.Rect(0, 0, w, b.Dy()))
	across := newScaler(w, resizeBand, b.Dx(), resizeBand)
	for y := 0; y < b.Dy(); y += resizeBand {
		y1 := min(y+resizeBand, b.Dy())
		// A shorter last band differs from the scaler's size, and Scale then
		// builds a scaler of its own for it.
		across.Scale(narrow, image.Rect(0, y, w, y1),
			img, image.Rect(b.Min.X, b.Min.Y+y, b.Max.X, b.Min.Y+y1), draw.Src, nil)
	}

	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	down := newScaler(resizeBand, h, resizeBand, b.Dy())
	for x := 0; x < w; x += resizeBand {
		x1 := min(x+resizeBand, w)
		down.Scale(dst, image.Rect(x, 0, x1, h), narrow, image.Rect(x, 0, x1, b.Dy()), draw.Src, nil)
	}
	return dst
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
// DECODED format rather than from a client-supplied type. A pixel that is not
// opaque forces PNG whatever the format: JPEG has no alpha channel and writes a
// transparent pixel as black, which turns a product cut-out into a black frame.
func encode(img image.Image, format string) (data []byte, contentType string, err error) {
	var buf bytes.Buffer
	if format == "png" || hasAlpha(img) {
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

func hasAlpha(img image.Image) bool {
	o, ok := img.(interface{ Opaque() bool })
	return ok && !o.Opaque()
}
