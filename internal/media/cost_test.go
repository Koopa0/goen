package media

import (
	"bytes"
	"errors"
	"image"
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
			name: "an 8-bit grey PNG, four bytes a pixel once a tRNS chunk follows",
			body: pngHeader(side, side, 8, pngGrey),
			want: ErrTooLarge,
		},
		{
			name: "the same pixels paletted, one byte a pixel even with a tRNS chunk",
			body: pngHeader(side, side, 8, pngPaletted, pngChunk{"PLTE", make([]byte, 6)}, pngChunk{"tRNS", []byte{0}}),
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

// TestAProgressiveJPEGIsCostedAtEveryCoefficient pins the coefficient count to
// what image/jpeg allocates at 4:2:0: four bytes a pixel of blocks for the
// component sampled 2x2 and one for each of the others, beside the three-byte
// planes. image/jpeg sizes the MCU grid from the largest factors, which need
// not be luma's.
func TestAProgressiveJPEGIsCostedAtEveryCoefficient(t *testing.T) {
	t.Parallel()

	const side = 1600
	for _, tt := range []struct {
		name    string
		factors [3]byte
	}{
		{"luma sampled 2x2", [3]byte{0x22, 0x11, 0x11}},
		{"Cb sampled 2x2", [3]byte{0x11, 0x22, 0x11}},
	} {
		raw := jpegHeaderSampled(side, side, jpegSOF2, tt.factors)
		cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("%s: decode config: %v", tt.name, err)
		}
		pixels := int64(side * side)
		if got, want := decodeCost(raw, cfg, format), pixels*3+pixels*6; got != want {
			t.Errorf("%s: a progressive frame costs %d, want %d", tt.name, got, want)
		}
	}
}

// FuzzTheJPEGWalkSeesWhatImageJPEGDecodes keeps jpegWalk reading the stream
// image/jpeg does. Where they disagreed, an attacker could show the budget a
// small baseline frame and the decoder a large progressive one, or hide from
// it the segment that makes the decoder convert.
func FuzzTheJPEGWalkSeesWhatImageJPEGDecodes(f *testing.F) {
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
	f.Add(jpegFile(f, jpegSpec{w: 40, h: 24, factors: []byte{0x22, 0x11, 0x11}, after: [][]byte{adobeSegment(0)}}))
	f.Add(jpegFile(f, jpegSpec{w: 40, h: 24, factors: []byte{0x11, 0x11, 0x11, 0x11}, before: [][]byte{adobeSegment(0)}}))
	f.Add(jpegFile(f, jpegSpec{w: 40, h: 24, factors: []byte{0x11, 0x22, 0x11}, progressive: true}))

	f.Fuzz(func(t *testing.T, raw []byte) {
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(raw))
		if err != nil {
			return
		}
		if stream := jpegWalk(raw); stream.frame != nil {
			frame, ok := readJPEGFrame(stream.frame)
			if !ok {
				t.Fatalf("the walk found a frame header it cannot read in a stream image/jpeg accepts")
			}
			if frame.width != int64(cfg.Width) || frame.height != int64(cfg.Height) {
				t.Errorf("the walk found a %dx%d frame; image/jpeg decodes %dx%d",
					frame.width, frame.height, cfg.Width, cfg.Height)
			}
		}
		// A frame this large is refused by its size alone, and decoding it
		// would slow the search down.
		if cfg.Width*cfg.Height > 1<<18 {
			return
		}
		img, err := jpeg.Decode(bytes.NewReader(raw))
		if err != nil {
			return
		}
		if cost, held := decodeCost(raw, cfg, "jpeg"), heldBytes(t, img); cost < held {
			t.Errorf("decodeCost = %d, but image/jpeg returned %T holding %d bytes", cost, img, held)
		}
	})
}

// PNG colour types, from the IHDR chunk.
const (
	pngGrey            = 0
	pngTruecolourAlpha = 6
)

// pngHeader is a PNG signature plus an IHDR declaring w by h at the given bit
// depth and colour type, then any extra chunks, with no image data.
func pngHeader(w, h uint32, depth, colourType byte, extra ...pngChunk) []byte {
	var buf bytes.Buffer
	buf.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 0, 21)
	ihdr = append(ihdr, 0, 0, 0, 13, 'I', 'H', 'D', 'R')
	ihdr = append(ihdr, be32(w)...)
	ihdr = append(ihdr, be32(h)...)
	ihdr = append(ihdr, depth, colourType, 0, 0, 0)
	buf.Write(ihdr)
	buf.Write(crcOf(ihdr[4:]))
	for _, c := range extra {
		writePNGChunk(&buf, c.kind, c.data)
	}
	return buf.Bytes()
}

// jpegHeader is a JFIF JPEG with three 1x1-sampled components that stops after
// its frame header, which is as far as image.DecodeConfig reads.
func jpegHeader(w, h uint16, sof byte) []byte {
	return jpegHeaderSampled(w, h, sof, [3]byte{0x11, 0x11, 0x11})
}

// jpegHeaderSampled is jpegHeader with each component's sampling factors given
// as the header's own nibble pair.
func jpegHeaderSampled(w, h uint16, sof byte, factors [3]byte) []byte {
	return []byte{
		0xff, jpegSOI,
		0xff, 0xe0, 0, 16, 'J', 'F', 'I', 'F', 0, 1, 1, 0, 0, 1, 0, 1, 0, 0,
		0xff, sof, 0, 17, 8, byte(h >> 8 & 0xff), byte(h & 0xff), byte(w >> 8 & 0xff), byte(w & 0xff), 3,
		1, factors[0], 0,
		2, factors[1], 1,
		3, factors[2], 1,
	}
}
