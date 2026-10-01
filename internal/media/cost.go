package media

import (
	"image"
)

// widestPixel is the most a standard decoder's image holds for one pixel: four
// 16-bit channels.
const widestPixel = 8

// decodeCost bounds the pixel buffers image.Decode allocates for raw: the image
// it returns and every other buffer the size of the picture that its decoder
// fills on the way. It is read from raw's own headers, never from
// cfg.ColorModel: DecodeConfig stops reading before the chunks and segments
// that change what Decode builds, such as a PNG's tRNS or a JPEG's Adobe
// segment after its first scan.
func decodeCost(raw []byte, cfg image.Config, format string) int64 {
	w, h := int64(cfg.Width), int64(cfg.Height)
	switch format {
	case "png":
		return pngCost(raw, w*h)
	case "jpeg":
		return jpegCost(raw, w, h)
	case "gif":
		return gifCost(w * h)
	case "webp":
		return webpCost(w, h)
	default:
		// A decoder that holds more than one image of the widest pixel needs a
		// case of its own before it is registered.
		return w * h * widestPixel
	}
}

// IHDR's fields as offsets into the file. image/png refuses a file whose first
// chunk is not IHDR, so a file DecodeConfig accepted has them here.
const (
	pngBitDepth   = 24
	pngColourType = 25
	pngInterlace  = 28

	pngPaletted = 3
)

// pngCost: image/png decodes grey and truecolour into NRGBA, or NRGBA64 at 16
// bits, whenever a tRNS chunk follows IHDR, and DecodeConfig stops at IHDR, so
// every image but a paletted one is costed as if it had one. An interlaced
// image is also decoded pass by pass into an image per pass, and every pixel is
// in exactly one pass, so the passes allocate as much again as the image.
func pngCost(raw []byte, pixels int64) int64 {
	if len(raw) <= pngInterlace {
		return pixels * widestPixel * 2
	}
	var perPixel int64
	switch {
	case raw[pngColourType] == pngPaletted:
		perPixel = 1
	case raw[pngBitDepth] == 16:
		perPixel = 8
	default:
		perPixel = 4
	}
	if raw[pngInterlace] != 0 {
		perPixel *= 2
	}
	return pixels * perPixel
}

// gifCost: image/gif decodes the first frame, which lies inside the screen
// DecodeConfig reports, at a byte a pixel, and copies an interlaced frame once
// more to put its rows in order.
func gifCost(pixels int64) int64 {
	return pixels * 2
}

// webpCost is what x/image/webp allocates for the only WebP Normalise lets it
// decode, a lossy one: YCbCr 4:2:0 over whole 16x16 macroblocks, 384 bytes
// each, and beside it the raw alpha plane an extended file may carry.
func webpCost(w, h int64) int64 {
	return ((w+15)/16)*((h+15)/16)*384 + w*h
}

// jpegCost is what image/jpeg allocates for the frame header's picture:
//   - a plane per component to decode into, over whole MCUs, each costed at
//     full resolution, which is how image/jpeg lays out a sampling ratio it has
//     no subsampled layout for;
//   - for a progressive frame, 64 int32 coefficients for every 8x8 block of
//     every component, kept until the last scan;
//   - for four components, or three that are RGB, a second image at four
//     bytes a pixel that the planes are converted into.
//
// Three components are RGB when their ids spell it or an Adobe segment says
// so, and that segment may follow the first scan, where DecodeConfig stops. A
// JFIF segment overrules both, but any later APP0 segment takes that back, so
// it is not counted on.
func jpegCost(raw []byte, w, h int64) int64 {
	stream := jpegWalk(raw)
	frame, ok := readJPEGFrame(stream.frame)
	if !ok {
		// A frame this walk cannot read as image/jpeg did is costed at the
		// largest MCU, with four planes, four components' coefficients and the
		// converted image.
		padded := ((w + 31) / 32 * 32) * ((h + 31) / 32 * 32)
		return padded*(4+4*jpegBlockBytes/64) + w*h*4
	}
	cost := frame.padded() * frame.components
	if stream.progressive {
		cost += frame.mcusX * frame.mcusY * frame.blocksPerMCU * jpegBlockBytes
	}
	if frame.components == 4 || (frame.components == 3 && (frame.rgbIDs || stream.adobeRGB)) {
		cost += frame.width * frame.height * 4
	}
	return cost
}

// jpegBlockBytes is one 8x8 block of int32 coefficients.
const jpegBlockBytes = 64 * 4

// jpegFrame is what a frame header decides about image/jpeg's buffers.
type jpegFrame struct {
	width, height int64
	components    int64
	// rgbIDs is three components whose ids are 'R', 'G' and 'B'.
	rgbIDs bool
	// The MCU grid is sized by the largest sampling factors, and a single
	// component is treated as 1x1 whatever its header says.
	maxH, maxV   int64
	mcusX, mcusY int64
	blocksPerMCU int64
}

// padded is a full-resolution plane's pixels over whole MCUs.
func (f jpegFrame) padded() int64 {
	return f.mcusX * 8 * f.maxH * f.mcusY * 8 * f.maxV
}

// readJPEGFrame reads a frame header's body. ok is false for one it cannot
// size, which image/jpeg refuses too.
func readJPEGFrame(header []byte) (f jpegFrame, ok bool) {
	if len(header) < 6 {
		return jpegFrame{}, false
	}
	f.height = int64(header[1])<<8 | int64(header[2])
	f.width = int64(header[3])<<8 | int64(header[4])
	f.components = int64(header[5])
	if (f.components != 1 && f.components != 3 && f.components != 4) || len(header) < 6+3*int(f.components) {
		return jpegFrame{}, false
	}
	f.maxH, f.maxV = 1, 1
	for c := range int(f.components) {
		h, v := int64(1), int64(1)
		if f.components > 1 {
			hv := header[6+3*c+1]
			h, v = int64(hv>>4), int64(hv&0x0f)
		}
		if h < 1 || h > 4 || v < 1 || v > 4 {
			return jpegFrame{}, false
		}
		f.maxH, f.maxV = max(f.maxH, h), max(f.maxV, v)
		f.blocksPerMCU += h * v
	}
	f.mcusX = (f.width + 8*f.maxH - 1) / (8 * f.maxH)
	f.mcusY = (f.height + 8*f.maxV - 1) / (8 * f.maxV)
	f.rgbIDs = f.components == 3 && header[6] == 'R' && header[9] == 'G' && header[12] == 'B'
	return f, true
}

// JPEG markers, from Table B.1 of the specification.
const (
	jpegSOF0  = 0xc0 // baseline
	jpegSOF1  = 0xc1 // extended sequential
	jpegSOF2  = 0xc2 // progressive
	jpegRST0  = 0xd0
	jpegRST7  = 0xd7
	jpegSOI   = 0xd8
	jpegEOI   = 0xd9
	jpegSOS   = 0xda
	jpegAPP14 = 0xee
)

// jpegStream is what image/jpeg's decoder reads from a stream's segments.
type jpegStream struct {
	// frame is the first frame header's body, the only one image/jpeg accepts,
	// or nil.
	frame       []byte
	progressive bool
	// adobeRGB is an Adobe segment naming transform 0, which image/jpeg
	// decodes as RGB unless a JFIF segment says otherwise.
	adobeRGB bool
}

// jpegWalk reads raw's segments from SOI to EOI as image/jpeg's decoder does.
// A scan's entropy-coded bytes need no decoding to step over: image/jpeg
// consumes them only as stuffed bytes and restart markers and stops at any
// other marker, so its marker loop meets the segments this walk does. It
// converts the planes only on reaching EOI, so a segment past where this walk
// has to stop can never make it convert.
func jpegWalk(raw []byte) jpegStream {
	var s jpegStream
	if len(raw) < 2 || raw[0] != 0xff || raw[1] != jpegSOI {
		return s
	}
	for i := 2; ; {
		marker, next, ok := jpegMarker(raw, i)
		if !ok || marker == jpegEOI {
			return s
		}
		i = next
		if jpegRST0 <= marker && marker <= jpegRST7 {
			// A restart marker carries no length.
			continue
		}
		body, after, whole := jpegSegment(raw, next)
		if !whole {
			return s
		}
		s.read(marker, body)
		i = after
	}
}

// read takes from one segment what image/jpeg's decoder does.
func (s *jpegStream) read(marker byte, body []byte) {
	switch {
	case s.frame == nil && (marker == jpegSOF0 || marker == jpegSOF1 || marker == jpegSOF2):
		s.frame, s.progressive = body, marker == jpegSOF2
	case marker == jpegAPP14 && adobeRGB(body):
		s.adobeRGB = true
	}
}

// adobeRGB reports an Adobe segment naming transform 0, read as image/jpeg
// reads one: from its first twelve bytes, and not at all if it is shorter.
func adobeRGB(body []byte) bool {
	return len(body) >= 12 && string(body[:5]) == "Adobe" && body[11] == 0
}

// jpegSegment reads the length-prefixed segment body at raw[i], whose length
// counts its own two bytes. next is the index just past the body.
func jpegSegment(raw []byte, i int) (body []byte, next int, ok bool) {
	if i+2 > len(raw) {
		return nil, 0, false
	}
	n := (int(raw[i])<<8 | int(raw[i+1])) - 2
	i += 2
	if n < 0 || n > len(raw)-i {
		return nil, 0, false
	}
	return raw[i : i+n], i + n, true
}

// jpegMarker reads the marker at or after raw[i] as image/jpeg does: bytes
// before a 0xff are dropped one at a time, "\xff\x00" is data, and fill bytes
// of 0xff may precede the marker. next is the index just past it.
func jpegMarker(raw []byte, i int) (marker byte, next int, ok bool) {
	for {
		if i+2 > len(raw) {
			return 0, 0, false
		}
		first, second := raw[i], raw[i+1]
		i += 2
		for first != 0xff {
			if i >= len(raw) {
				return 0, 0, false
			}
			first, second = second, raw[i]
			i++
		}
		if second == 0 {
			continue
		}
		for second == 0xff {
			if i >= len(raw) {
				return 0, 0, false
			}
			second = raw[i]
			i++
		}
		return second, i, true
	}
}
