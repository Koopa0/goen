package media

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"github.com/koopa0/goen/assets"
)

// pngBytes is a real PNG of the given size.
func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

// jpegBytes is a real JPEG.
func jpegBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

// TestAnUploadIsReEncodedNotStored proves goen stores its own bytes.
//
// This is the security property the package exists for.
//
// The bytes goen keeps are the bytes goen produced. An upload that decodes and
// is re-encoded from its pixels cannot carry anything that is not pixels — no
// HTML polyglot a browser might sniff as a page, no EXIF payload, no appended
// archive. Sanitising the input would be a list of things to remember;
// re-encoding is nothing left to remember.
func TestAnUploadIsReEncodedNotStored(t *testing.T) {
	clean := pngBytes(t, 40, 30)

	// A real PNG with junk appended: still decodes, and the trailer is exactly
	// what a polyglot smuggles a payload in.
	payload := []byte(`<html><script>alert(1)</script>`)
	dirty := append(append([]byte{}, clean...), payload...)

	obj, stored, err := Normalise(bytes.NewReader(dirty))
	if err != nil {
		t.Fatalf("a valid PNG with a trailer was refused: %v", err)
	}
	if bytes.Contains(stored, payload) {
		t.Error("the appended payload survived into the stored bytes; goen is " +
			"storing the upload rather than its own encoding")
	}
	if bytes.Equal(stored, dirty) {
		t.Error("the stored bytes are the uploaded bytes verbatim")
	}
	if obj.Width != 40 || obj.Height != 30 {
		t.Errorf("dimensions are %dx%d, want 40x30", obj.Width, obj.Height)
	}
	// And what came out is genuinely decodable as what it claims to be.
	if _, err := png.Decode(bytes.NewReader(stored)); err != nil {
		t.Errorf("the stored PNG does not decode: %v", err)
	}
}

// TestTheDigestIsOfWhatIsServed proves the digest names the output, not the
// upload.
//
// Content addressing is only sound if the digest names the bytes a client will
// receive. Taking it of the UPLOAD would let two files with the same pixels and
// different trailers become two rows serving identical images — and would break
// the immutability the year-long Cache-Control rests on.
func TestTheDigestIsOfWhatIsServed(t *testing.T) {
	clean := pngBytes(t, 20, 20)
	withTrailer := append(append([]byte{}, clean...), []byte("junk")...)

	a, dataA, err := Normalise(bytes.NewReader(clean))
	if err != nil {
		t.Fatalf("clean: %v", err)
	}
	b, dataB, err := Normalise(bytes.NewReader(withTrailer))
	if err != nil {
		t.Fatalf("with trailer: %v", err)
	}

	if a.Digest != b.Digest {
		t.Errorf("the same pixels produced two digests (%s, %s); the digest is "+
			"being taken of the upload rather than the output", a.Digest[:12], b.Digest[:12])
	}
	if !bytes.Equal(dataA, dataB) {
		t.Error("the same pixels produced different stored bytes")
	}
	if len(a.Digest) != 64 || strings.ToLower(a.Digest) != a.Digest {
		t.Errorf("digest %q is not 64 lowercase hex characters", a.Digest)
	}
}

// TestNothingButAnImageIsAccepted proves the decoder decides, not the client.
//
// Every case here is a file a client could post with Content-Type: image/png
// and a .png name. Neither is consulted — the decoder decides.
func TestNothingButAnImageIsAccepted(t *testing.T) {
	tests := []struct {
		name string
		body []byte
	}{
		{"empty", nil},
		{"plain text", []byte("hello")},
		{"html", []byte(`<html><body><script>alert(1)</script></body></html>`)},
		{"a PHP tag", []byte("<?php system($_GET['c']); ?>")},
		{"an SVG, which is a document and not a raster", []byte(
			`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)},
		{"a PNG magic number and nothing else", []byte("\x89PNG\r\n\x1a\n")},
		{"a truncated PNG", pngBytes(t, 10, 10)[:40]},
		{"a zip", []byte("PK\x03\x04nothing else")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := Normalise(bytes.NewReader(tt.body)); err == nil {
				t.Error("accepted")
			}
		})
	}
}

// TestADecompressionBombIsRefusedBeforeItIsAllocated proves the header bound
// runs before any pixel buffer.
//
// A tiny PNG can declare enormous dimensions: the file is 200 bytes and the
// pixel buffer is gigabytes. A byte-size limit cannot see this, which is why
// the header is checked separately and before the decode.
func TestADecompressionBombIsRefusedBeforeItIsAllocated(t *testing.T) {
	// Built by hand rather than encoded, because encoding one would itself
	// allocate the buffer this test is about.
	bomb := forgedPNGHeader(50000, 50000)
	_, _, err := Normalise(bytes.NewReader(bomb))
	if err == nil {
		t.Fatal("a 50000x50000 declaration was accepted")
	}
	// ErrTooLarge and not ErrNotAnImage, and the difference is the whole point.
	// This file also fails the FULL decode — it has no pixel data — so a test
	// that only asked whether something was refused stayed green with the
	// header check deleted, which the mutation run found. ErrTooLarge can only
	// come from boundsOK, which runs before any pixel buffer is allocated; a
	// bomb that reached the decoder would have cost 10GB to refuse.
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("a decompression bomb was refused with %v, want ErrTooLarge — "+
			"that means it was caught by the decoder rather than by the header "+
			"check, and a real bomb would have been allocated first", err)
	}

	// The boundary, both sides, so the limit is the limit and not roughly it.
	if err := boundsOK(MaxDimension, 1); err != nil {
		t.Errorf("a %dx1 image was refused: %v", MaxDimension, err)
	}
	if err := boundsOK(MaxDimension+1, 1); err == nil {
		t.Errorf("a %dx1 image was accepted", MaxDimension+1)
	}
	if err := boundsOK(7000, 7000); err == nil {
		t.Error("49 megapixels was accepted; MaxPixels is 40 million")
	}
	if err := boundsOK(0, 10); err == nil {
		t.Error("a zero-width image was accepted")
	}
}

// forgedPNGHeader is a PNG signature plus an IHDR declaring w by h, with no
// image data. DecodeConfig reads it; nothing allocates.
func forgedPNGHeader(w, h uint32) []byte {
	var buf bytes.Buffer
	buf.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 0, 21)
	ihdr = append(ihdr, 0, 0, 0, 13, 'I', 'H', 'D', 'R')
	ihdr = append(ihdr, be32(w)...)
	ihdr = append(ihdr, be32(h)...)
	ihdr = append(ihdr, 8, 6, 0, 0, 0)
	buf.Write(ihdr)
	buf.Write(crcOf(ihdr[4:]))
	return buf.Bytes()
}

// be32 is a big-endian uint32, which is how PNG writes every length and
// dimension.
func be32(v uint32) []byte {
	return []byte{byte(v >> 24 & 0xFF), byte(v >> 16 & 0xFF), byte(v >> 8 & 0xFF), byte(v & 0xFF)}
}

// crcOf is the PNG chunk CRC32.
func crcOf(b []byte) []byte {
	const poly = 0xEDB88320
	crc := uint32(0xFFFFFFFF)
	for _, c := range b {
		crc ^= uint32(c)
		for range 8 {
			if crc&1 == 1 {
				crc = (crc >> 1) ^ poly
			} else {
				crc >>= 1
			}
		}
	}
	crc ^= 0xFFFFFFFF
	return be32(crc)
}

// TestTheOutputFormatIsGoensChoice proves the stored format comes from the
// decode, never from a header.
//
// PNG stays PNG: a logo or screenshot with flat colour and hard edges is ruined
// by JPEG, and transparency becomes black. Everything else becomes JPEG,
// because a photograph as PNG is several times the bytes for nothing visible.
// The decision comes from the DECODED format, never from a client's header.
func TestTheOutputFormatIsGoensChoice(t *testing.T) {
	tests := []struct {
		name string
		body []byte
		want string
	}{
		{"png stays png", pngBytes(t, 12, 12), "image/png"},
		{"jpeg stays jpeg", jpegBytes(t, 12, 12), "image/jpeg"},
		{"gif becomes jpeg", gifBytes(t, 12, 12), "image/jpeg"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj, _, err := Normalise(bytes.NewReader(tt.body))
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			if obj.ContentType != tt.want {
				t.Errorf("content type is %q, want %q", obj.ContentType, tt.want)
			}
		})
	}
}

// gifBytes is a real GIF.
func gifBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewPaletted(image.Rect(0, 0, w, h), color.Palette{color.Black, color.White})
	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

// TestByteSizeDescribesTheStoredBytes proves the recorded size is the real
// one.
//
// media_objects_size_matches enforces it in the schema; this catches it before
// the write, where the message can say what went wrong.
func TestByteSizeDescribesTheStoredBytes(t *testing.T) {
	obj, data, err := Normalise(bytes.NewReader(pngBytes(t, 64, 48)))
	if err != nil {
		t.Fatalf("refused: %v", err)
	}
	if int(obj.ByteSize) != len(data) {
		t.Errorf("ByteSize is %d and the data is %d bytes", obj.ByteSize, len(data))
	}
}

// TestSrcsetNeverMisleadsTheBrowser proves every candidate states a width the
// image really has.
//
// A srcset is a promise about pixel widths, and a browser both CHOOSES and
// LAYS OUT on those numbers. Two ways to break that promise, both of which the
// first version of this did:
//
//   - Mixing a bare candidate with w-descriptors. HTML allows all-w, all-x, or
//     one bare candidate alone; mixed, the bare one is a parse error and the
//     full-size image drops silently out of the set.
//   - Offering a width the original does not have. Resize refuses to upscale,
//     so an 800w candidate for a 600px image serves 600 pixels while claiming
//     800.
func TestSrcsetNeverMisleadsTheBrowser(t *testing.T) {
	const d = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

	tests := []struct {
		name  string
		width int
		want  string
	}{
		{
			"a wide original offers both renditions",
			1600,
			"/media/" + d + "/400 400w, /media/" + d + "/800 800w, /media/" + d + " 1600w",
		},
		{
			// 800 is not offered: it equals the original, so the rendition
			// would be the original bytes under a second URL.
			"an 800px original offers only 400",
			800,
			"/media/" + d + "/400 400w, /media/" + d + " 800w",
		},
		{
			"a small original offers only itself",
			300,
			"/media/" + d + " 300w",
		},
		{"an unknown width offers nothing", 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := assets.ProductImageSrcsetAt(d, tt.width)
			if got != tt.want {
				t.Errorf("Srcset = %q\n            want %q", got, tt.want)
			}
			if got == "" {
				return
			}
			// Every candidate carries a descriptor, or the set is malformed.
			for _, candidate := range strings.Split(got, ", ") {
				if !strings.HasSuffix(candidate, "w") || !strings.Contains(candidate, " ") {
					t.Errorf("candidate %q has no width descriptor", candidate)
				}
			}
		})
	}
}

// TestResizeNeverUpscalesAndOnlyServesKnownWidths proves the width allowlist
// bounds the work an anonymous request can ask for.
//
// The allowlist is the security property: resizing costs CPU proportional to
// the output, so an open ?w= is a denial of service that costs the attacker one
// request.
func TestResizeNeverUpscalesAndOnlyServesKnownWidths(t *testing.T) {
	original := pngBytes(t, 1200, 900)
	_, stored, err := Normalise(bytes.NewReader(original))
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}

	small, err := Resize(stored, "image/png", 400)
	if err != nil {
		t.Fatalf("resize: %v", err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(small))
	if err != nil {
		t.Fatalf("decode rendition: %v", err)
	}
	if cfg.Width != 400 {
		t.Errorf("the 400 rendition is %d wide", cfg.Width)
	}
	// Aspect ratio preserved: 1200x900 at 400 wide is 300 tall.
	if cfg.Height != 300 {
		t.Errorf("the 400 rendition is %d tall, want 300 — the aspect ratio moved", cfg.Height)
	}

	// Upscaling returns the original bytes rather than a bigger, worse file.
	narrow := pngBytes(t, 200, 100)
	_, storedNarrow, err := Normalise(bytes.NewReader(narrow))
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	up, err := Resize(storedNarrow, "image/png", 800)
	if err != nil {
		t.Fatalf("resize: %v", err)
	}
	if !bytes.Equal(up, storedNarrow) {
		t.Error("a 200px image asked for at 800 was enlarged rather than returned as-is")
	}

	// Anything off the allowlist is refused, including plausible sizes.
	for _, w := range []int{0, -1, 1, 399, 401, 1600, 100000} {
		if _, err := Resize(stored, "image/png", w); err == nil {
			t.Errorf("width %d was rendered; only %v are allowed", w, assets.MediaWidths)
		}
	}
}
