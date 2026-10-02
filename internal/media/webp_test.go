package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"io"
	"testing"

	"golang.org/x/image/riff"
	"golang.org/x/image/webp"
)

// refusalAllowance is what Normalise may allocate refusing one of the files
// below: the upload read whole and its headers read, far short of the picture
// buffer any of them declares.
const refusalAllowance = 1 << 20

// TestALosslessWebPIsRefusedBeforeItIsDecoded holds the refusal of every WebP
// whose decode would run x/image's lossless decoder, and of a chunk list the
// walk cannot follow to a frame. Each refused file declares a picture whose
// buffers are megabytes, so a refusal that came from decoding it, or after
// decoding it, shows in what Normalise allocated. A lossy WebP, with or
// without a raw alpha chunk, is still accepted.
//
// It is not parallel: it reads the process's allocation counter, which a test
// running beside it would add to.
func TestALosslessWebPIsRefusedBeforeItIsDecoded(t *testing.T) {
	const side = 1000
	lossless := vp8lFrame(side, side, true)
	for _, tt := range []struct {
		name string
		raw  []byte
		want error
	}{
		{"a lossless file", webpFile(riffChunk{"VP8L", lossless}), ErrLosslessWebP},
		{"an extended file with a lossless frame", webpFile(
			riffChunk{"VP8X", vp8xHeader(side, side, false)},
			riffChunk{"VP8L", lossless},
		), ErrLosslessWebP},
		{"a lossless frame behind a chunk the decoder skips", webpFile(
			riffChunk{"VP8Z", []byte("skipped")},
			riffChunk{"VP8L", lossless},
		), ErrLosslessWebP},
		{"an extended file with losslessly compressed alpha", webpFile(
			riffChunk{"VP8X", vp8xHeader(side, side, true)},
			riffChunk{"ALPH", append([]byte{1}, lossless[5:]...)},
			riffChunk{"VP8 ", vp8Frame(side, side)},
		), ErrLosslessWebP},
		{"a lossy frame whose chunk runs past the end of the file",
			overstated(webpFile(riffChunk{"VP8 ", vp8Frame(2*side, 2*side)[:64]})), ErrNotAnImage},
		{"an extended file with no frame", webpFile(
			riffChunk{"VP8X", vp8xHeader(side, side, false)},
		), ErrNotAnImage},
		{"an alpha chunk with no compression method x/image knows", webpFile(
			riffChunk{"VP8X", vp8xHeader(side, side, true)},
			riffChunk{"ALPH", append([]byte{2}, make([]byte, 16)...)},
			riffChunk{"VP8 ", vp8Frame(side, side)},
		), ErrNotAnImage},
		{"a lossy file", webpFile(riffChunk{"VP8 ", vp8Frame(64, 48)}), nil},
		{"an extended lossy file with raw alpha", webpFile(
			riffChunk{"VP8X", vp8xHeader(64, 48, true)},
			riffChunk{"ALPH", append([]byte{0}, bytes.Repeat([]byte{0xff}, 64*48)...)},
			riffChunk{"VP8 ", vp8Frame(64, 48)},
		), nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, format, err := image.DecodeConfig(bytes.NewReader(tt.raw)); err != nil || format != "webp" {
				t.Fatalf("DecodeConfig = %q, %v: Normalise would refuse the file there, "+
					"so the row tests nothing", format, err)
			}
			var obj Object
			var err error
			spent := allocated(func() {
				obj, _, err = Normalise(bytes.NewReader(tt.raw))
			})
			if tt.want == nil {
				if err != nil {
					t.Fatalf("Normalise = %v, want a lossy WebP accepted", err)
				}
				if obj.ContentType != "image/jpeg" {
					t.Errorf("stored as %q, want image/jpeg", obj.ContentType)
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Errorf("Normalise = %v, want %v", err, tt.want)
			}
			if spent > refusalAllowance {
				t.Errorf("Normalise allocated %d bytes refusing it, more than %d: "+
					"the file was decoded before it was refused", spent, refusalAllowance)
			}
		})
	}
}

// FuzzTheWebPWalkRefusesWhatTheLosslessDecoderWouldRead holds
// refuseLosslessWebP to x/image/webp's own decode loop. Where they disagreed,
// a file could show the walk a lossy frame and hand the decoder a lossless
// one, whose Huffman tables no budget bounds. Two witnesses: reachesVP8L,
// which runs that loop's chunk logic over the riff reader it uses, and the
// decoder itself, which returns NRGBA only from a lossless frame.
func FuzzTheWebPWalkRefusesWhatTheLosslessDecoderWouldRead(f *testing.F) {
	lossless := vp8lFrame(16, 8, true)
	for _, seed := range [][]byte{
		webpFile(riffChunk{"VP8L", lossless}),
		webpFile(riffChunk{"VP8 ", vp8Frame(16, 8)}),
		webpFile(riffChunk{"VP8X", vp8xHeader(16, 8, false)}, riffChunk{"VP8L", lossless}),
		webpFile(riffChunk{"VP8Z", []byte("odd")}, riffChunk{"VP8L", lossless}),
		webpFile(
			riffChunk{"VP8X", vp8xHeader(16, 8, true)},
			riffChunk{"ALPH", append([]byte{1}, lossless[5:]...)},
			riffChunk{"VP8 ", vp8Frame(16, 8)},
		),
		webpFile(
			riffChunk{"VP8X", vp8xHeader(16, 8, true)},
			riffChunk{"ALPH", append([]byte{0}, make([]byte, 16*8)...)},
			riffChunk{"VP8 ", vp8Frame(16, 8)},
		),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
		if err != nil || format != "webp" {
			return
		}
		refusal := refuseLosslessWebP(raw)
		if refusal == nil && reachesVP8L(raw) {
			t.Fatal("x/image/webp's decode loop hands this file to the lossless decoder, and the walk accepts it")
		}
		// Only an accepted file is decoded, small, so a search that finds a
		// lossless one the walk missed does not build its Huffman tables at
		// full size.
		if refusal != nil || cfg.Width*cfg.Height > 1<<12 {
			return
		}
		img, err := webp.Decode(bytes.NewReader(raw))
		if _, lossless := img.(*image.NRGBA); err == nil && lossless {
			t.Fatal("the walk accepted a file x/image/webp decoded as lossless")
		}
	})
}

// reachesVP8L reports whether x/image/webp's decode loop would run the
// lossless decoder over raw. It is that loop's chunk logic (webp/decode.go,
// x/image v0.46.0) over the riff reader the loop uses, kept apart from
// refuseLosslessWebP so that each checks the other.
func reachesVP8L(raw []byte) bool {
	form, chunks, err := riff.NewReader(bytes.NewReader(raw))
	if err != nil || form != (riff.FourCC{'W', 'E', 'B', 'P'}) {
		return false
	}
	var wantAlpha, seenVP8X bool
	for {
		id, size, data, err := chunks.Next()
		if err != nil {
			return false
		}
		switch string(id[:]) {
		case "ALPH":
			if !wantAlpha {
				return false
			}
			var method [1]byte
			if _, err := io.ReadFull(data, method[:]); err != nil {
				return false
			}
			// A raw alpha plane leaves the loop refusing any VP8L after it.
			return method[0]&0x03 == 1
		case "VP8 ":
			return false
		case "VP8L":
			return true
		case "VP8X":
			if seenVP8X || size != 10 {
				return false
			}
			seenVP8X = true
			var header [10]byte
			if _, err := io.ReadFull(data, header[:]); err != nil {
				return false
			}
			wantAlpha = header[0]&webpAlphaFlag != 0
		}
	}
}

// overstated is raw with its RIFF form and first chunk each claiming a
// megabyte more than the file holds.
func overstated(raw []byte) []byte {
	out := bytes.Clone(raw)
	for _, at := range []int{4, webpFormStart + 4} {
		n := binary.LittleEndian.Uint32(out[at:])
		binary.LittleEndian.PutUint32(out[at:], n+1<<20)
	}
	return out
}
