package media

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"

	"golang.org/x/image/draw"

	"github.com/koopa0/goen/assets"
)

// Resize renders data at the given width, preserving aspect ratio.
//
// Never upscales: asking for 800 from a 600-wide original returns the original
// bytes unchanged. Enlarging produces a bigger file that looks worse, and a
// srcset offering it would make a browser download the worse one believing it
// had chosen better.
//
// CatmullRom rather than NearestNeighbor or ApproxBiLinear: the sharpest
// available scaler is the right trade when the CPU is paid once and the quality
// is seen every time.
//
// "Paid once" is a property of the CALLER and never of this function. The
// year-long Cache-Control belongs to the CLIENT, and it does nothing at all for
// the first request from every client, for a crawler, or for anyone hammering the
// URL on purpose — this runs in full for every one of them. What makes the claim
// true on the server side is the renderer in render.go: a bounded in-process
// cache in front of this, one flight per (digest, width), and a limit on how many
// of these may run at once. This function is the raw scaler and bounds nothing
// but the width it will accept.
func Resize(data []byte, contentType string, width int) ([]byte, error) {
	if !assets.KnownWidth(width) {
		return nil, fmt.Errorf("media: %d is not a rendition goen offers", width)
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		// The bytes in the table are goen's own encoding, so a decode failure
		// here is corruption rather than bad input.
		return nil, fmt.Errorf("decode stored image: %w", err)
	}

	b := src.Bounds()
	if b.Dx() <= width {
		return data, nil
	}
	height := b.Dy() * width / b.Dx()
	if height < 1 {
		height = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)

	var buf bytes.Buffer
	if contentType == "image/png" {
		if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, dst); err != nil {
			return nil, fmt.Errorf("encode rendition: %w", err)
		}
		return buf.Bytes(), nil
	}
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: JPEGQuality}); err != nil {
		return nil, fmt.Errorf("encode rendition: %w", err)
	}
	return buf.Bytes(), nil
}
