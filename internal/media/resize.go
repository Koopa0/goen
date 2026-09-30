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
	return resizeWith(data, contentType, width, draw.CatmullRom.NewScaler)
}

// resizeWith is Resize over the scalers newScaler makes, which is
// draw.CatmullRom.NewScaler in every wiring goen has.
func resizeWith(
	data []byte, contentType string, width int,
	newScaler func(dw, dh, sw, sh int) draw.Scaler,
) ([]byte, error) {
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

	// In bands, as an upload is: any visitor can ask for a rendition, and one
	// scale over the whole image holds 32 bytes per rendition column per source
	// row, 123 MB for a 2400px source at 1600.
	dst := scaleInBands(src, width, height, newScaler)

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
