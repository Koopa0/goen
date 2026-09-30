package media

import (
	"bytes"
	"compress/lzw"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"image"
	"math"
	"runtime"
	"testing"
)

// decoderState is what image.Decode allocates whatever the picture's size: the
// buffered reader it wraps raw in, each decoder's tables and windows (zlib's
// alone is 32 KiB), and the rounding of every large allocation up to whole
// pages, which an interlaced PNG makes eight of. A lossy WebP also copies its
// compressed partitions, which the test adds as len(raw).
const decoderState = 128 << 10

// TestTheBudgetBoundsWhatDecodeAllocates decodes one real file of every layout
// the budget prices differently and holds decodeCost above both the image
// image.Decode returns and everything it allocated on the way. The headers
// DecodeConfig reads do not say what either will be: a tRNS chunk, an Adobe
// segment after the first scan or a VP8L frame behind a VP8X header each
// decode wider than the header's colour model.
//
// It is not parallel: it reads the process's allocation counter, which a test
// running beside it would add to.
func TestTheBudgetBoundsWhatDecodeAllocates(t *testing.T) {
	const w, h = 250, 200 // not a whole number of MCUs or macroblocks
	for _, tt := range []struct {
		name string
		raw  []byte
	}{
		{"8-bit grey PNG", pngFile(t, w, h, 8, pngGrey, false)},
		{"16-bit grey PNG", pngFile(t, w, h, 16, pngGrey, false)},
		{"8-bit grey PNG with tRNS", pngFile(t, w, h, 8, pngGrey, false, pngChunk{"tRNS", []byte{0, 1}})},
		{"16-bit grey PNG with tRNS", pngFile(t, w, h, 16, pngGrey, false, pngChunk{"tRNS", []byte{0, 1}})},
		{"2-bit grey PNG with tRNS, rows ending mid-byte", pngFile(t, w+1, h, 2, pngGrey, false, pngChunk{"tRNS", []byte{0, 1}})},
		{"8-bit RGB PNG", pngFile(t, w, h, 8, pngTruecolour, false)},
		{"8-bit RGB PNG with tRNS", pngFile(t, w, h, 8, pngTruecolour, false, pngChunk{"tRNS", make([]byte, 6)})},
		{"8-bit RGBA PNG", pngFile(t, w, h, 8, pngTruecolourAlpha, false)},
		{"16-bit RGBA PNG", pngFile(t, w, h, 16, pngTruecolourAlpha, false)},
		{"paletted PNG", pngFile(t, w, h, 8, pngPaletted, false, pngChunk{"PLTE", make([]byte, 6)})},
		{"interlaced 8-bit RGBA PNG, odd sides", pngFile(t, w+1, h+1, 8, pngTruecolourAlpha, true)},
		{"interlaced 16-bit grey PNG with tRNS", pngFile(t, w, h, 16, pngGrey, true, pngChunk{"tRNS", []byte{0, 1}})},
		{"grey JPEG", jpegFile(t, jpegSpec{w: w, h: h, factors: []byte{0x11}})},
		{"baseline 4:2:0 JPEG", jpegFile(t, jpegSpec{w: w, h: h, factors: []byte{0x22, 0x11, 0x11}})},
		{"baseline 4:4:4 JPEG", jpegFile(t, jpegSpec{w: w, h: h, factors: []byte{0x11, 0x11, 0x11}})},
		{"JPEG with Cb sampled finer than Y", jpegFile(t, jpegSpec{w: w, h: h, factors: []byte{0x11, 0x22, 0x11}})},
		{"progressive 4:2:0 JPEG", jpegFile(t, jpegSpec{w: w, h: h, factors: []byte{0x22, 0x11, 0x11}, progressive: true})},
		{"progressive grey JPEG", jpegFile(t, jpegSpec{w: w, h: h, factors: []byte{0x11}, progressive: true})},
		{"JPEG whose component ids spell RGB", jpegFile(t, jpegSpec{w: w, h: h, factors: []byte{0x11, 0x11, 0x11}, ids: []byte("RGB")})},
		{"CMYK JPEG", jpegFile(t, jpegSpec{
			w: w, h: h, factors: []byte{0x11, 0x11, 0x11, 0x11}, before: [][]byte{adobeSegment(0)},
		})},
		{"YCCK JPEG", jpegFile(t, jpegSpec{
			w: w, h: h, factors: []byte{0x22, 0x11, 0x11, 0x22}, before: [][]byte{adobeSegment(2)}, progressive: true,
		})},
		{"JPEG with an Adobe RGB segment after its scan", jpegFile(t, jpegSpec{
			w: w, h: h, factors: []byte{0x22, 0x11, 0x11}, after: [][]byte{adobeSegment(0)},
		})},
		{"JFIF JPEG with a JFXX and an Adobe RGB segment after its scan", jpegFile(t, jpegSpec{
			w: w, h: h, factors: []byte{0x22, 0x11, 0x11}, before: [][]byte{jfifSegment()},
			after: [][]byte{jpegSegmentOf(jpegAPP0, []byte("JFXX\x00\x10")), adobeSegment(0)},
		})},
		{"GIF", gifFile(t, w, h, false)},
		// Twice the side here and for the colour-indexed and alpha WebPs: what
		// they allocate beside the image is a byte or two a pixel, and has to
		// stand clear of decoderState to be seen.
		{"interlaced GIF", gifFile(t, 2*w, 2*h, true)},
		{"lossy WebP", webpFile(riffChunk{"VP8 ", vp8Frame(w, h)})},
		{"lossless WebP", webpFile(riffChunk{"VP8L", vp8lFrame(w, h, false)})},
		{"colour-indexed lossless WebP", webpFile(riffChunk{"VP8L", vp8lFrame(2*w, 2*h, true)})},
		{"extended WebP with a lossy frame", webpFile(riffChunk{"VP8X", vp8xHeader(w, h, false)}, riffChunk{"VP8 ", vp8Frame(w, h)})},
		{"extended WebP with a lossless frame", webpFile(riffChunk{"VP8X", vp8xHeader(w, h, false)}, riffChunk{"VP8L", vp8lFrame(w, h, true)})},
		{"extended WebP with raw alpha", webpFile(
			riffChunk{"VP8X", vp8xHeader(w, h, true)},
			riffChunk{"ALPH", append([]byte{0}, make([]byte, w*h)...)},
			riffChunk{"VP8 ", vp8Frame(w, h)},
		)},
		{"extended WebP with lossless alpha", webpFile(
			riffChunk{"VP8X", vp8xHeader(2*w, 2*h, true)},
			riffChunk{"ALPH", append([]byte{1}, vp8lFrame(2*w, 2*h, true)[5:]...)},
			riffChunk{"VP8 ", vp8Frame(2*w, 2*h)},
		)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg, format, err := image.DecodeConfig(bytes.NewReader(tt.raw))
			if err != nil {
				t.Fatalf("decode config: %v", err)
			}
			cost := decodeCost(tt.raw, cfg, format)

			var img image.Image
			spent := allocated(func() {
				img, _, err = image.Decode(bytes.NewReader(tt.raw))
			})
			if err != nil {
				t.Fatalf("the file does not decode, so it tests nothing: %v", err)
			}
			if held := heldBytes(t, img); cost < held {
				t.Errorf("decodeCost = %d, but image.Decode returned %T holding %d bytes", cost, img, held)
			}
			if limit := cost + decoderState + int64(len(tt.raw)); spent > limit {
				t.Errorf("decodeCost = %d, but image.Decode allocated %d bytes, more than the cost "+
					"and %d bytes of decoder state and input", cost, spent, limit-cost)
			}
		})
	}
}

// allocated is the bytes f allocates, the least of three runs, so that what the
// runtime allocates for itself during one of them is not counted.
func allocated(f func()) int64 {
	least := uint64(math.MaxUint64)
	for range 3 {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		f()
		runtime.ReadMemStats(&after)
		least = min(least, after.TotalAlloc-before.TotalAlloc)
	}
	return int64(least) //nolint:gosec // G115: a few megabytes at most
}

// heldBytes is the pixel storage img holds, including what a decoder padded it
// with to whole blocks.
func heldBytes(t *testing.T, img image.Image) int64 {
	t.Helper()
	switch m := img.(type) {
	case *image.Gray:
		return int64(cap(m.Pix))
	case *image.Gray16:
		return int64(cap(m.Pix))
	case *image.RGBA:
		return int64(cap(m.Pix))
	case *image.RGBA64:
		return int64(cap(m.Pix))
	case *image.NRGBA:
		return int64(cap(m.Pix))
	case *image.NRGBA64:
		return int64(cap(m.Pix))
	case *image.CMYK:
		return int64(cap(m.Pix))
	case *image.Paletted:
		return int64(cap(m.Pix))
	case *image.YCbCr:
		return int64(cap(m.Y) + cap(m.Cb) + cap(m.Cr))
	case *image.NYCbCrA:
		return int64(cap(m.Y) + cap(m.Cb) + cap(m.Cr) + cap(m.A))
	}
	t.Fatalf("%T: the test does not know what it holds", img)
	return 0
}

// pngTruecolour is IHDR's colour type for RGB without alpha.
const pngTruecolour = 2

// pngChunk is one chunk placed between IHDR and IDAT.
type pngChunk struct {
	kind string
	data []byte
}

// pngFile is a w by h PNG whose every sample is zero.
func pngFile(tb testing.TB, w, h int, depth, colourType byte, interlaced bool, extra ...pngChunk) []byte {
	tb.Helper()
	channels := 1
	switch colourType {
	case pngTruecolour:
		channels = 3
	case pngTruecolourAlpha:
		channels = 4
	}
	bitsPerPixel := int(depth) * channels

	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	rows := func(pw, ph int) {
		row := make([]byte, 1+(bitsPerPixel*pw+7)/8)
		for range ph {
			if _, err := zw.Write(row); err != nil {
				tb.Fatalf("compress: %v", err)
			}
		}
	}
	if !interlaced {
		rows(w, h)
	} else {
		for _, p := range []struct{ x, y, dx, dy int }{
			{0, 0, 8, 8}, {4, 0, 8, 8}, {0, 4, 4, 8}, {2, 0, 4, 4}, {0, 2, 2, 4}, {1, 0, 2, 2}, {0, 1, 1, 2},
		} {
			pw, ph := (w-p.x+p.dx-1)/p.dx, (h-p.y+p.dy-1)/p.dy
			if pw > 0 && ph > 0 {
				rows(pw, ph)
			}
		}
	}
	if err := zw.Close(); err != nil {
		tb.Fatalf("compress: %v", err)
	}

	var buf bytes.Buffer
	buf.WriteString("\x89PNG\r\n\x1a\n")
	interlace := byte(0)
	if interlaced {
		interlace = 1
	}
	ihdr := binary.BigEndian.AppendUint32(nil, uint32(w)) //nolint:gosec // G115: a test's small side
	ihdr = binary.BigEndian.AppendUint32(ihdr, uint32(h)) //nolint:gosec // G115: a test's small side
	writePNGChunk(&buf, "IHDR", append(ihdr, depth, colourType, 0, 0, interlace))
	for _, c := range extra {
		writePNGChunk(&buf, c.kind, c.data)
	}
	writePNGChunk(&buf, "IDAT", z.Bytes())
	writePNGChunk(&buf, "IEND", nil)
	return buf.Bytes()
}

func writePNGChunk(buf *bytes.Buffer, kind string, data []byte) {
	typed := append([]byte(kind), data...)
	buf.Write(binary.BigEndian.AppendUint32(nil, uint32(len(data)))) //nolint:gosec // G115: a test's small chunk
	buf.Write(typed)
	buf.Write(binary.BigEndian.AppendUint32(nil, crc32.ChecksumIEEE(typed)))
}

// JPEG markers jpegFile writes beside those cost.go reads.
const (
	jpegDHT  = 0xc4
	jpegDQT  = 0xdb
	jpegAPP0 = 0xe0
)

// jpegSpec describes a JPEG for jpegFile.
type jpegSpec struct {
	w, h int
	// factors is each component's sampling factors as the header's nibble pair.
	factors []byte
	// ids names the components; nil numbers them from 1.
	ids         []byte
	progressive bool
	// before and after are whole segments placed before the frame header and
	// after the scan.
	before, after [][]byte
}

// jpegFile is a JPEG whose every coefficient is zero. A DC table and an AC
// table of one one-bit code each stand for a zero difference and an end of
// block, so every block is two zero bits, or one in a progressive DC scan,
// which is all a progressive frame needs to decode.
func jpegFile(tb testing.TB, s jpegSpec) []byte {
	tb.Helper()
	n := len(s.factors)
	ids := s.ids
	if ids == nil {
		for c := range n {
			ids = append(ids, byte(c+1))
		}
	}

	var out bytes.Buffer
	out.Write([]byte{0xff, jpegSOI})
	for _, seg := range s.before {
		out.Write(seg)
	}
	out.Write(jpegSegmentOf(jpegDQT, append([]byte{0}, bytes.Repeat([]byte{1}, 64)...)))

	sof := byte(jpegSOF0)
	if s.progressive {
		sof = jpegSOF2
	}
	frame := make([]byte, 0, 6+3*n)
	frame = append(frame, 8, byte(s.h>>8&0xff), byte(s.h&0xff), byte(s.w>>8&0xff), byte(s.w&0xff), byte(n&0xff))
	for c := range n {
		frame = append(frame, ids[c], s.factors[c], 0)
	}
	out.Write(jpegSegmentOf(sof, frame))

	oneCode := append([]byte{1}, make([]byte, 15)...)
	out.Write(jpegSegmentOf(jpegDHT, append(append([]byte{0x00}, oneCode...), 0)))
	out.Write(jpegSegmentOf(jpegDHT, append(append([]byte{0x10}, oneCode...), 0)))

	scan := make([]byte, 0, 1+2*n+3)
	scan = append(scan, byte(n&0xff))
	for c := range n {
		scan = append(scan, ids[c], 0x00)
	}
	end := byte(63)
	bitsPerBlock := 2
	if s.progressive {
		end, bitsPerBlock = 0, 1
	}
	out.Write(jpegSegmentOf(jpegSOS, append(scan, 0, end, 0)))

	maxH, maxV, perMCU := 1, 1, 0
	for _, hv := range s.factors {
		maxH, maxV = max(maxH, int(hv>>4)), max(maxV, int(hv&0x0f))
		perMCU += int(hv>>4) * int(hv&0x0f)
	}
	if n == 1 {
		maxH, maxV, perMCU = 1, 1, 1
	}
	blocks := ((s.w + 8*maxH - 1) / (8 * maxH)) * ((s.h + 8*maxV - 1) / (8 * maxV)) * perMCU
	bits := blocks * bitsPerBlock
	data := make([]byte, (bits+7)/8)
	if spare := len(data)*8 - bits; spare > 0 {
		data[len(data)-1] = byte(1<<spare - 1)
	}
	out.Write(data)

	for _, seg := range s.after {
		out.Write(seg)
	}
	out.Write([]byte{0xff, jpegEOI})
	return out.Bytes()
}

// jpegSegmentOf is a whole segment: its marker, its length and body.
func jpegSegmentOf(marker byte, body []byte) []byte {
	n := len(body) + 2
	return append([]byte{0xff, marker, byte(n >> 8 & 0xff), byte(n & 0xff)}, body...)
}

// adobeSegment is an Adobe APP14 segment naming a colour transform.
func adobeSegment(transform byte) []byte {
	return jpegSegmentOf(jpegAPP14, []byte{'A', 'd', 'o', 'b', 'e', 0, 100, 0, 0, 0, 0, transform})
}

// jfifSegment is a JFIF APP0 segment.
func jfifSegment() []byte {
	return jpegSegmentOf(jpegAPP0, []byte{'J', 'F', 'I', 'F', 0, 1, 1, 0, 0, 1, 0, 1, 0, 0})
}

// gifFile is a w by h GIF of one frame, every pixel the first colour.
func gifFile(tb testing.TB, w, h int, interlaced bool) []byte {
	tb.Helper()
	var out bytes.Buffer
	out.WriteString("GIF89a")
	out.Write(binary.LittleEndian.AppendUint16(nil, uint16(w))) //nolint:gosec // G115: a test's small side
	out.Write(binary.LittleEndian.AppendUint16(nil, uint16(h))) //nolint:gosec // G115: a test's small side
	out.Write([]byte{0x80, 0, 0, 0, 0, 0, 0xff, 0xff, 0xff})    // a two-colour global table
	fields := byte(0)
	if interlaced {
		fields = 0x40
	}
	out.Write([]byte{0x2c, 0, 0, 0, 0})
	out.Write(binary.LittleEndian.AppendUint16(nil, uint16(w))) //nolint:gosec // G115: a test's small side
	out.Write(binary.LittleEndian.AppendUint16(nil, uint16(h))) //nolint:gosec // G115: a test's small side
	out.WriteByte(fields)

	const litWidth = 2
	var packed bytes.Buffer
	lw := lzw.NewWriter(&packed, lzw.LSB, litWidth)
	if _, err := lw.Write(make([]byte, w*h)); err != nil {
		tb.Fatalf("compress: %v", err)
	}
	if err := lw.Close(); err != nil {
		tb.Fatalf("compress: %v", err)
	}
	out.WriteByte(litWidth)
	for data := packed.Bytes(); len(data) > 0; {
		block := data[:min(255, len(data))]
		out.WriteByte(byte(len(block) & 0xff))
		out.Write(block)
		data = data[len(block):]
	}
	out.Write([]byte{0, 0x3b})
	return out.Bytes()
}

// riffChunk is one chunk of a WebP file.
type riffChunk struct {
	kind string
	data []byte
}

// webpFile wraps chunks in a RIFF WEBP container.
func webpFile(chunks ...riffChunk) []byte {
	var body bytes.Buffer
	body.WriteString("WEBP")
	for _, c := range chunks {
		body.WriteString(c.kind)
		body.Write(binary.LittleEndian.AppendUint32(nil, uint32(len(c.data)))) //nolint:gosec // G115: a test's small chunk
		body.Write(c.data)
		if len(c.data)%2 == 1 {
			body.WriteByte(0)
		}
	}
	var out bytes.Buffer
	out.WriteString("RIFF")
	out.Write(binary.LittleEndian.AppendUint32(nil, uint32(body.Len()))) //nolint:gosec // G115: a test's small file
	out.Write(body.Bytes())
	return out.Bytes()
}

// vp8xHeader is a VP8X chunk's body for a w by h canvas.
func vp8xHeader(w, h int, alpha bool) []byte {
	flags := byte(0)
	if alpha {
		flags = webpAlphaFlag
	}
	return []byte{flags, 0, 0, 0,
		byte((w - 1) & 0xff), byte((w - 1) >> 8 & 0xff), byte((w - 1) >> 16 & 0xff),
		byte((h - 1) & 0xff), byte((h - 1) >> 8 & 0xff), byte((h - 1) >> 16 & 0xff)}
}

// vp8Frame is a w by h lossy key frame whose partitions are all zero bytes. A
// boolean decoder reads every bit of a zero stream as 0, whatever its
// probability, so every header field, prediction mode and first token is the
// zeroth: no segments, no filter, and an end of block in every block.
func vp8Frame(w, h int) []byte {
	macroblocks := ((w + 15) / 16) * ((h + 15) / 16)
	first := 1024 + 8*macroblocks
	frame := make([]byte, 10+first+32*macroblocks)
	copy(frame, []byte{
		byte(first<<5&0xff) | 0x10, byte(first >> 3 & 0xff), byte(first >> 11 & 0xff), // key frame, shown
		0x9d, 0x01, 0x2a,
		byte(w & 0xff), byte(w >> 8 & 0xff), byte(h & 0xff), byte(h >> 8 & 0xff),
	})
	return frame
}

// vp8lFrame is a w by h lossless frame whose five Huffman codes each have one
// symbol, so every pixel costs no bits. A colour-indexed one has sixteen
// colours, the most x/image/vp8l decodes packed two pixels to each four-byte
// pixel before expanding them.
func vp8lFrame(w, h int, colourIndexed bool) []byte {
	var b lsbBits
	b.put(0x2f, 8)
	b.put(uint64(w-1), 14) //nolint:gosec // G115: a test's small side
	b.put(uint64(h-1), 14) //nolint:gosec // G115: a test's small side
	b.put(0, 1)            // alpha hint
	b.put(0, 3)            // version
	oneSymbolCodes := func() {
		for range 5 {
			b.put(1, 1) // simple
			b.put(0, 1) // one symbol
			b.put(0, 1) // of one bit
			b.put(0, 1) // symbol 0
		}
	}
	if colourIndexed {
		b.put(1, 1)  // a transform
		b.put(3, 2)  // colour indexing
		b.put(15, 8) // sixteen colours
		b.put(0, 1)  // no colour cache
		oneSymbolCodes()
	}
	b.put(0, 1) // no more transforms
	b.put(0, 1) // no colour cache
	b.put(0, 1) // no Huffman groups
	oneSymbolCodes()
	return b.bytes()
}

// lsbBits packs values least significant bit first, as VP8L reads them.
type lsbBits struct {
	out  []byte
	acc  uint64
	nacc uint
}

func (b *lsbBits) put(v uint64, n uint) {
	b.acc |= v << b.nacc
	b.nacc += n
	for b.nacc >= 8 {
		b.out = append(b.out, byte(b.acc&0xff))
		b.acc >>= 8
		b.nacc -= 8
	}
}

func (b *lsbBits) bytes() []byte {
	if b.nacc > 0 {
		b.out = append(b.out, byte(b.acc&0xff))
		b.acc, b.nacc = 0, 0
	}
	return b.out
}
