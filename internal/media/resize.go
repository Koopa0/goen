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

// Resize renders data at the given width, preserving aspect ratio and never
// upscaling. It bounds nothing but the width it accepts; the renderer in
// render.go is what stops it running once per request.
func Resize(data []byte, contentType string, width int) ([]byte, error) {
	if !assets.KnownWidth(width) {
		return nil, fmt.Errorf("media: %d is not a rendition goen offers", width)
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode stored image: %w", err)
	}

	b := src.Bounds()
	if b.Dx() <= width {
		return data, nil
	}
	height := max(b.Dy()*width/b.Dx(), 1)

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
