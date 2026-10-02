package media

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
)

const (
	jpegAPP1 = 0xe1

	exifOrientationTag = 0x0112
	tiffTypeShort      = 3
)

// exifOrientation reads the EXIF Orientation of a JPEG, 1 to 8, or 1 (upright)
// when the file has none or carries a value outside that range. Browsers apply
// the tag to the original, and goen's re-encode drops it, so the pixels must be
// turned to match what the uploader saw.
func exifOrientation(raw []byte) int {
	if len(raw) < 2 || raw[0] != 0xff || raw[1] != jpegSOI {
		return 1
	}
	for i := 2; ; {
		marker, next, ok := jpegMarker(raw, i)
		if !ok || marker == jpegEOI || marker == jpegSOS {
			return 1
		}
		i = next
		if jpegRST0 <= marker && marker <= jpegRST7 {
			continue
		}
		body, after, whole := jpegSegment(raw, next)
		if !whole {
			return 1
		}
		if marker == jpegAPP1 && bytes.HasPrefix(body, []byte("Exif\x00\x00")) {
			return tiffOrientation(body[6:])
		}
		i = after
	}
}

// tiffOrientation reads the Orientation entry of the first image directory of
// an EXIF TIFF block.
func tiffOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(tiff[:4]) {
	case "II*\x00":
		order = binary.LittleEndian
	case "MM\x00*":
		order = binary.BigEndian
	default:
		return 1
	}
	ifd := int64(order.Uint32(tiff[4:8]))
	if ifd+2 > int64(len(tiff)) {
		return 1
	}
	count := int64(order.Uint16(tiff[ifd:]))
	for n := range count {
		entry := ifd + 2 + n*12
		if entry+12 > int64(len(tiff)) {
			return 1
		}
		e := tiff[entry : entry+12]
		if order.Uint16(e) != exifOrientationTag {
			continue
		}
		if order.Uint16(e[2:]) != tiffTypeShort || order.Uint32(e[4:]) != 1 {
			return 1
		}
		if v := int(order.Uint16(e[8:])); v >= 1 && v <= 8 {
			return v
		}
		return 1
	}
	return 1
}

// orient returns img turned as EXIF orientation o says: 2 mirrors left to
// right, 3 turns it half way, 4 mirrors top to bottom, 5 transposes, 6 turns it
// clockwise, 7 transverses and 8 turns it counter-clockwise. An upright image is
// returned as is.
func orient(img image.Image, o int) image.Image {
	if o < 2 || o > 8 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := range dh {
		for x := range dw {
			var sx, sy int
			switch o {
			case 2:
				sx, sy = w-1-x, y
			case 3:
				sx, sy = w-1-x, h-1-y
			case 4:
				sx, sy = x, h-1-y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, h-1-x
			case 7:
				sx, sy = w-1-y, h-1-x
			case 8:
				sx, sy = w-1-y, x
			}
			dst.Set(x, y, color.RGBAModel.Convert(img.At(b.Min.X+sx, b.Min.Y+sy)))
		}
	}
	return dst
}
