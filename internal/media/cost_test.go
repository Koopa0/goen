package media

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// TestAnImageOverTheByteBudgetIsRefusedBeforeItIsDecoded holds MaxDecodedBytes
// to the header. Every forgery here is inside MaxPixels and carries no pixel
// data, so ErrTooLarge can only come from the budget: a decode would fail with
// ErrNotAnImage, and the control of each pair shows exactly that.
func TestAnImageOverTheByteBudgetIsRefusedBeforeItIsDecoded(t *testing.T) {
	t.Parallel()

	const side = 6000 // 36 megapixels, under MaxPixels
	if side*side > MaxPixels {
		t.Fatalf("%dx%d is over MaxPixels; this test would be about pixels", side, side)
	}
	for _, tt := range []struct {
		name string
		body []byte
		want error
	}{
		{
			name: "a 16-bit RGBA PNG, eight bytes a pixel",
			body: pngHeader(side, side, 16, pngTruecolourAlpha),
			want: ErrTooLarge,
		},
		{
			name: "the same pixels as 8-bit grey, one byte a pixel",
			body: pngHeader(side, side, 8, pngGrey),
			want: ErrNotAnImage,
		},
		{
			name: "a progressive JPEG, which keeps every coefficient",
			body: jpegHeader(5000, 5000, jpegSOF2),
			want: ErrTooLarge,
		},
		{
			name: "the same frame as baseline, which keeps none",
			body: jpegHeader(5000, 5000, jpegSOF0),
			want: ErrNotAnImage,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, _, err := image.DecodeConfig(bytes.NewReader(tt.body)); err != nil {
				t.Fatalf("the forged header does not parse, so it tests nothing: %v", err)
			}
			_, _, err := Normalise(bytes.NewReader(tt.body))
			if !errors.Is(err, tt.want) {
				t.Errorf("Normalise = %v, want %v", err, tt.want)
			}
		})
	}
}

// TestTheBudgetCountsWhatEachDecoderHolds pins the per-pixel figures to the
// buffers the standard decoders allocate for each model.
func TestTheBudgetCountsWhatEachDecoderHolds(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		model color.Model
		want  int64
	}{
		{color.Palette{color.Black, color.White}, 1},
		{color.GrayModel, 1},
		{color.Gray16Model, 2},
		{color.YCbCrModel, 3},
		{color.NRGBAModel, 4},
		{color.RGBAModel, 4},
		{color.CMYKModel, 4},
		{color.NRGBA64Model, 8},
		{color.RGBA64Model, 8},
		{color.ModelFunc(func(c color.Color) color.Color { return c }), 8},
	} {
		if got := bytesPerPixel(tt.model); got != tt.want {
			t.Errorf("bytesPerPixel(%T) = %d, want %d", tt.model, got, tt.want)
		}
	}

	// 4:2:0 progressive: the luma blocks hold four bytes a pixel and each
	// chroma plane one, beside the three-byte image.
	raw := jpegHeaderSampled(1600, 1600, jpegSOF2, 0x22)
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode config: %v", err)
	}
	pixels := int64(1600 * 1600)
	if got, want := decodeCost(raw, cfg, format), pixels*3+pixels*6; got != want {
		t.Errorf("progressive 4:2:0 costs %d, want %d", got, want)
	}
}

// FuzzTheFrameWalkFindsTheFrameImageJPEGDecodes keeps jpegFrame reading the
// same header image/jpeg does. Where they disagreed, an attacker could show the
// budget a small baseline frame and the decoder a large progressive one.
func FuzzTheFrameWalkFindsTheFrameImageJPEGDecodes(f *testing.F) {
	f.Add(jpegHeader(640, 480, jpegSOF0))
	f.Add(jpegHeader(640, 480, jpegSOF2))
	// Junk before a marker, a stuffed zero and fill bytes, all of which
	// image/jpeg skips.
	f.Add(append([]byte{0xff, 0xd8, 'j', 'u', 'n', 'k', 0xff, 0x00, 0xff, 0xff}, jpegHeader(64, 32, jpegSOF2)[3:]...))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 33, 17)), nil); err != nil {
		f.Fatalf("encode: %v", err)
	}
	f.Add(buf.Bytes())

	f.Fuzz(func(t *testing.T, raw []byte) {
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(raw))
		if err != nil {
			return
		}
		header, _, ok := jpegFrame(raw)
		if !ok {
			// Costed as progressive at full resolution: safe, if pessimistic.
			return
		}
		if len(header) < 5 {
			t.Fatalf("frame header of %d bytes for a stream image/jpeg decoded", len(header))
		}
		height := int(header[1])<<8 | int(header[2])
		width := int(header[3])<<8 | int(header[4])
		if width != cfg.Width || height != cfg.Height {
			t.Errorf("the walk found a %dx%d frame; image/jpeg decodes %dx%d",
				width, height, cfg.Width, cfg.Height)
		}
	})
}

// PNG colour types, from the IHDR chunk.
const (
	pngGrey            = 0
	pngTruecolourAlpha = 6
)

// pngHeader is a PNG signature plus an IHDR declaring w by h at the given bit
// depth and colour type, with no image data.
func pngHeader(w, h uint32, depth, colourType byte) []byte {
	var buf bytes.Buffer
	buf.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 0, 21)
	ihdr = append(ihdr, 0, 0, 0, 13, 'I', 'H', 'D', 'R')
	ihdr = append(ihdr, be32(w)...)
	ihdr = append(ihdr, be32(h)...)
	ihdr = append(ihdr, depth, colourType, 0, 0, 0)
	buf.Write(ihdr)
	buf.Write(crcOf(ihdr[4:]))
	return buf.Bytes()
}

// jpegHeader is a JFIF JPEG with three 1x1-sampled components that stops after
// its frame header, which is as far as image.DecodeConfig reads.
func jpegHeader(w, h uint16, sof byte) []byte {
	return jpegHeaderSampled(w, h, sof, 0x11)
}

// jpegHeaderSampled is jpegHeader with the luma component's sampling factors
// given as the header's own nibble pair.
func jpegHeaderSampled(w, h uint16, sof, luma byte) []byte {
	return []byte{
		0xff, jpegSOI,
		0xff, 0xe0, 0, 16, 'J', 'F', 'I', 'F', 0, 1, 1, 0, 0, 1, 0, 1, 0, 0,
		0xff, sof, 0, 17, 8, byte(h >> 8 & 0xff), byte(h & 0xff), byte(w >> 8 & 0xff), byte(w & 0xff), 3,
		1, luma, 0,
		2, 0x11, 1,
		3, 0x11, 1,
	}
}
