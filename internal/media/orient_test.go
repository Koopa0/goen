package media

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"strings"
	"testing"
)

// grid is a 3x2 image whose six pixels are told apart by their red value:
//
//	A B C
//	D E F
func grid() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	for i := range 6 {
		img.Set(i%3, i/3, color.RGBA{R: uint8(10 * (i + 1)), A: 255})
	}
	return img
}

func letters(img image.Image) []string {
	var rows []string
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		var row strings.Builder
		for x := b.Min.X; x < b.Max.X; x++ {
			r, _, _, _ := img.At(x, y).RGBA()
			row.WriteRune(rune('A' + (r>>8)/10 - 1))
		}
		rows = append(rows, row.String())
	}
	return rows
}

func TestOrientTurnsThePixelsAsEXIFSays(t *testing.T) {
	t.Parallel()

	tests := []struct {
		orientation int
		want        []string
	}{
		{1, []string{"ABC", "DEF"}},
		{2, []string{"CBA", "FED"}},
		{3, []string{"FED", "CBA"}},
		{4, []string{"DEF", "ABC"}},
		{5, []string{"AD", "BE", "CF"}},
		{6, []string{"DA", "EB", "FC"}},
		{7, []string{"FC", "EB", "DA"}},
		{8, []string{"CF", "BE", "AD"}},
		{0, []string{"ABC", "DEF"}},
		{9, []string{"ABC", "DEF"}},
	}
	for _, tt := range tests {
		got := letters(orient(grid(), tt.orientation))
		if len(got) != len(tt.want) {
			t.Errorf("orientation %d: rows %v, want %v", tt.orientation, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("orientation %d: rows %v, want %v", tt.orientation, got, tt.want)
				break
			}
		}
	}
}

type tiffOrder interface {
	binary.ByteOrder
	binary.AppendByteOrder
}

// withOrientation inserts an EXIF APP1 segment carrying orientation o after the
// SOI of a JPEG, in the byte order the camera chose.
func withOrientation(t *testing.T, jpegData []byte, o uint16, order tiffOrder) []byte {
	t.Helper()
	tiff := []byte("II*\x00")
	if order == binary.BigEndian {
		tiff = []byte("MM\x00*")
	}
	tiff = order.AppendUint32(tiff, 8)
	tiff = order.AppendUint16(tiff, 1)
	tiff = order.AppendUint16(tiff, exifOrientationTag)
	tiff = order.AppendUint16(tiff, tiffTypeShort)
	tiff = order.AppendUint32(tiff, 1)
	tiff = order.AppendUint16(tiff, o)
	tiff = append(tiff, 0, 0)
	tiff = order.AppendUint32(tiff, 0)

	body := append([]byte("Exif\x00\x00"), tiff...)
	seg := binary.BigEndian.AppendUint16([]byte{0xff, jpegAPP1}, uint16(len(body)+2)) //nolint:gosec // G115: a few hundred bytes of test EXIF
	seg = append(seg, body...)
	out := append([]byte{}, jpegData[:2]...)
	out = append(out, seg...)
	return append(out, jpegData[2:]...)
}

func TestExifOrientationIsReadInEitherByteOrder(t *testing.T) {
	t.Parallel()

	base := jpegBytes(t, 12, 8)
	if got := exifOrientation(base); got != 1 {
		t.Errorf("a JPEG with no EXIF reads as orientation %d, want 1", got)
	}
	for _, order := range []tiffOrder{binary.LittleEndian, binary.BigEndian} {
		for o := uint16(1); o <= 8; o++ {
			if got := exifOrientation(withOrientation(t, base, o, order)); got != int(o) {
				t.Errorf("%v orientation %d read as %d", order, o, got)
			}
		}
	}
	if got := exifOrientation(withOrientation(t, base, 9, binary.LittleEndian)); got != 1 {
		t.Errorf("an out-of-range orientation reads as %d, want 1", got)
	}
	if got := exifOrientation(withOrientation(t, base, 6, binary.LittleEndian)[:30]); got != 1 {
		t.Errorf("a truncated EXIF segment reads as %d, want 1", got)
	}
}

// A portrait phone photo is stored as landscape pixels with Orientation 6; the
// uploader saw it upright, so the stored copy must be too.
func TestNormaliseStoresWhatTheUploaderSaw(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 30, 20)), nil); err != nil {
		t.Fatal(err)
	}
	obj, data, err := Normalise(bytes.NewReader(withOrientation(t, buf.Bytes(), 6, binary.BigEndian)))
	if err != nil {
		t.Fatalf("refused: %v", err)
	}
	if obj.Width != 20 || obj.Height != 30 {
		t.Errorf("stored %dx%d, want 20x30 for a photo whose Orientation is 6", obj.Width, obj.Height)
	}
	if exifOrientation(data) != 1 || bytes.Contains(data, []byte("Exif")) {
		t.Error("the stored copy still carries an EXIF segment")
	}
}
