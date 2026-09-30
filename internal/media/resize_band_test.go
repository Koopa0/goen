package media

import (
	"bytes"
	"image"
	"image/png"
	"math"
	"testing"

	"golang.org/x/image/draw"
)

// TestNoScaleHoldsMoreThanOneBandOfScratch is the bound on the resize's scratch
// memory. A call to x/image/draw's kernel scaler holds 32 bytes per destination
// column per source row, so a single call over the whole image grows with the
// source rather than with what goen keeps: 485 MB for a 6320px square. Every
// call is recorded here, and none may span more than a band of the axis it is
// not reducing.
func TestNoScaleHoldsMoreThanOneBandOfScratch(t *testing.T) {
	t.Parallel()

	const w, h, limit = 1000, 700, 400
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	var calls []scaleCall
	out := fitLongestSideWith(src, limit, func(dw, dh, sw, sh int) draw.Scaler {
		return recordingScaler{real: draw.CatmullRom.NewScaler(dw, dh, sw, sh), calls: &calls}
	})
	if b := out.Bounds(); b.Dx() != limit || b.Dy() != limit*h/w {
		t.Fatalf("scaled to %v, want %dx%d", b, limit, limit*h/w)
	}
	if len(calls) == 0 {
		t.Fatal("no scale went through the scalers handed in; the bound below measures nothing")
	}

	oneBand := resizeBand * max(limit, h)
	for _, c := range calls {
		if scratch := c.dr.Dx() * c.sr.Dy(); scratch > oneBand {
			t.Errorf("a scale from %v to %v holds %d cells of scratch, over one band's %d; "+
				"the whole image's is %d", c.sr, c.dr, scratch, oneBand, limit*h)
		}
	}
}

// TestARenditionHoldsNoMoreThanOneBandOfScratch is the same bound on the
// rendition an anonymous visitor asks for at /media/{digest}/{width}: every
// scale Resize makes spans at most a band of the axis it is not reducing.
func TestARenditionHoldsNoMoreThanOneBandOfScratch(t *testing.T) {
	t.Parallel()

	const w, h, width = 1000, 700, 400
	var stored bytes.Buffer
	if err := png.Encode(&stored, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatalf("encode the stored image: %v", err)
	}
	var calls []scaleCall
	rendered, err := resizeWith(stored.Bytes(), "image/png", width, func(dw, dh, sw, sh int) draw.Scaler {
		return recordingScaler{real: draw.CatmullRom.NewScaler(dw, dh, sw, sh), calls: &calls}
	})
	if err != nil {
		t.Fatalf("resize: %v", err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(rendered))
	if err != nil {
		t.Fatalf("decode the rendition: %v", err)
	}
	if cfg.Width != width || cfg.Height != width*h/w {
		t.Fatalf("rendered %dx%d, want %dx%d", cfg.Width, cfg.Height, width, width*h/w)
	}
	if len(calls) == 0 {
		t.Fatal("no scale went through the scalers handed in; the bound below measures nothing")
	}

	oneBand := resizeBand * max(width, h)
	for _, c := range calls {
		if scratch := c.dr.Dx() * c.sr.Dy(); scratch > oneBand {
			t.Errorf("a scale from %v to %v holds %d cells of scratch, over one band's %d; "+
				"the whole image's is %d", c.sr, c.dr, scratch, oneBand, width*h)
		}
	}
}

// TestRendersRunNoWiderThanTheirMemoryBoundOnAnyMachine: a render's memory is
// set by the stored image, not by the machine, so more cores must not mean
// more renders at once.
func TestRendersRunNoWiderThanTheirMemoryBoundOnAnyMachine(t *testing.T) {
	t.Parallel()

	for procs, want := range map[int]int{0: 1, 1: 1, 2: 2, 4: maxRenderSlots, 64: maxRenderSlots} {
		if got := renderSlotsFor(procs); got != want {
			t.Errorf("renderSlotsFor(%d) = %d, want %d", procs, got, want)
		}
	}
	if renderSlots < 1 || renderSlots > maxRenderSlots {
		t.Errorf("renderSlots = %d, want between 1 and %d", renderSlots, maxRenderSlots)
	}
}

// scaleCall is one Scale a recordingScaler passed on.
type scaleCall struct{ dr, sr image.Rectangle }

// recordingScaler notes every call before making it.
type recordingScaler struct {
	real  draw.Scaler
	calls *[]scaleCall
}

func (s recordingScaler) Scale(
	dst draw.Image, dr image.Rectangle, src image.Image, sr image.Rectangle,
	op draw.Op, opts *draw.Options,
) {
	*s.calls = append(*s.calls, scaleCall{dr: dr, sr: sr})
	s.real.Scale(dst, dr, src, sr, op, opts)
}

// TestScalingInBandsDrawsWhatOneScaleDraws holds the bands to the picture one
// call would draw. The sizes are not multiples of resizeBand, so the shorter
// last band of each pass is in it. The waves are steep enough that a band
// shifted by one row or column moves a pixel by far more than the one level the
// intermediate image's rounding accounts for, and gentle enough that no
// overshoot is clipped in between.
func TestScalingInBandsDrawsWhatOneScaleDraws(t *testing.T) {
	t.Parallel()

	const w, h = 1000, 700
	wave := func(at, period float64) uint8 {
		return uint8(128 + 100*math.Sin(2*math.Pi*at/period))
	}
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			o := src.PixOffset(x, y)
			src.Pix[o+0] = wave(float64(x), 41)
			src.Pix[o+1] = wave(float64(y), 29)
			src.Pix[o+2] = wave(float64(x+y), 53)
			src.Pix[o+3] = 0xff
		}
	}

	got, ok := fitLongestSide(src, 400).(*image.RGBA)
	if !ok {
		t.Fatal("fitLongestSide did not return an RGBA image")
	}
	want := image.NewRGBA(image.Rect(0, 0, 400, 280))
	draw.CatmullRom.Scale(want, want.Bounds(), src, src.Bounds(), draw.Src, nil)
	if got.Bounds() != want.Bounds() {
		t.Fatalf("bands drew %v, one call draws %v", got.Bounds(), want.Bounds())
	}

	worst, at := 0, 0
	for i := range want.Pix {
		d := int(got.Pix[i]) - int(want.Pix[i])
		d = max(d, -d)
		if d > worst {
			worst, at = d, i
		}
	}
	if worst > 1 {
		x, y := (at%got.Stride)/4, at/got.Stride
		t.Errorf("the banded scale differs from one call by %d at (%d,%d); rounding the "+
			"intermediate image accounts for 1 at most", worst, x, y)
	}
}
