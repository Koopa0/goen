package media

import (
	"image"
	"image/color"
)

// decodeCost is the memory decoding raw holds at once, read from its header:
// the pixel buffer its colour model needs, and for a JPEG what image/jpeg keeps
// beside that buffer.
func decodeCost(raw []byte, cfg image.Config, format string) int64 {
	pixels := int64(cfg.Width) * int64(cfg.Height)
	cost := pixels * bytesPerPixel(cfg.ColorModel)
	if format == "jpeg" {
		cost += jpegWorkingSet(raw, cfg.ColorModel, pixels)
	}
	return cost
}

// bytesPerPixel is what the image a decoder returns holds per pixel. A model no
// decoder goen registers returns is costed as the widest one.
func bytesPerPixel(m color.Model) int64 {
	// Before the switch: a Palette is a slice, and comparing two interfaces
	// that both hold one panics.
	if _, paletted := m.(color.Palette); paletted {
		return 1
	}
	switch m {
	case color.GrayModel, color.AlphaModel:
		return 1
	case color.Gray16Model, color.Alpha16Model:
		return 2
	case color.YCbCrModel:
		return 3
	case color.NYCbCrAModel, color.CMYKModel, color.RGBAModel, color.NRGBAModel:
		return 4
	default:
		return 8
	}
}

// jpegWorkingSet is what image/jpeg holds beside the image it returns. An RGB
// or CMYK frame is decoded into YCbCr and black planes first and converted into
// a second image. A progressive frame keeps every DCT coefficient of every
// 8x8 block, 64 int32s each, until its last scan. A baseline YCbCr or grey
// frame decodes block by block into its image and holds nothing more.
func jpegWorkingSet(raw []byte, model color.Model, pixels int64) int64 {
	var cost int64
	if model == color.RGBAModel || model == color.CMYKModel {
		cost += pixels * 4
	}
	const blockBytes = 64 * 4
	header, progressive, found := jpegFrame(raw)
	blocks, counted := jpegBlocks(header)
	switch {
	case !found || !counted:
		// A frame header not found the way image/jpeg finds it could say
		// anything, so the frame is costed as progressive at full resolution
		// in every component.
		cost += pixels * 4 * jpegComponents(model)
	case progressive:
		cost += blocks * blockBytes
	}
	return cost
}

// jpegComponents is how many components a JPEG of this decoded model carries.
func jpegComponents(m color.Model) int64 {
	switch m {
	case color.GrayModel:
		return 1
	case color.CMYKModel:
		return 4
	default:
		return 3
	}
}

// JPEG markers, from Table B.1 of the specification.
const (
	jpegSOF0 = 0xc0 // baseline
	jpegSOF1 = 0xc1 // extended sequential
	jpegSOF2 = 0xc2 // progressive
	jpegRST0 = 0xd0
	jpegRST7 = 0xd7
	jpegSOI  = 0xd8
	jpegEOI  = 0xd9
	jpegSOS  = 0xda
)

// jpegFrame walks raw to its first frame header the way image/jpeg's decoder
// does, skipping what that decoder skips, and returns the header's body and
// whether the frame is progressive. ok is false for a stream it cannot walk to
// a frame header.
func jpegFrame(raw []byte) (header []byte, progressive, ok bool) {
	if len(raw) < 2 || raw[0] != 0xff || raw[1] != jpegSOI {
		return nil, false, false
	}
	for i := 2; ; {
		marker, next, found := jpegMarker(raw, i)
		if !found || marker == jpegEOI || marker == jpegSOS {
			return nil, false, false
		}
		if jpegRST0 <= marker && marker <= jpegRST7 {
			// A restart marker carries no length.
			i = next
			continue
		}
		body, after, whole := jpegSegment(raw, next)
		if !whole {
			return nil, false, false
		}
		if marker == jpegSOF0 || marker == jpegSOF1 || marker == jpegSOF2 {
			return body, marker == jpegSOF2, true
		}
		i = after
	}
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

// jpegBlocks counts the 8x8 blocks image/jpeg allocates coefficients for, from
// a frame header's body: per component, the frame's MCUs times that
// component's sampling factors. image/jpeg sizes the MCU grid from the first
// component, which it requires to have the largest factors, and treats a
// single component as 1x1 whatever its header says.
func jpegBlocks(header []byte) (int64, bool) {
	if len(header) < 6 {
		return 0, false
	}
	height := int64(header[1])<<8 | int64(header[2])
	width := int64(header[3])<<8 | int64(header[4])
	components := int(header[5])
	if components == 0 || len(header) < 6+3*components {
		return 0, false
	}
	factors := func(c int) (int64, int64) {
		if components == 1 {
			return 1, 1
		}
		hv := header[6+3*c+1]
		return int64(hv >> 4), int64(hv & 0x0f)
	}
	h0, v0 := factors(0)
	if h0 == 0 || v0 == 0 {
		return 0, false
	}
	mcus := ((width + 8*h0 - 1) / (8 * h0)) * ((height + 8*v0 - 1) / (8 * v0))
	var blocks int64
	for c := range components {
		h, v := factors(c)
		blocks += mcus * h * v
	}
	return blocks, true
}
