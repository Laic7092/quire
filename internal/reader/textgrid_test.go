package reader

import (
	"math"
	"testing"
)

// TestBaselinesShareOnePhase holds the snapping to what it is for: down a
// column of type, every line stands at the same phase of the device grid.
func TestBaselinesShareOnePhase(t *testing.T) {
	// One line box of this reader's body type, on a 4K screen at 150%: 30.09
	// device pixels, which is the ratio that walks.
	const line, scale = 17 * 1.18, 1.5
	for _, dy := range []float32{0, 78, 78.6667, 123.5} {
		phases := map[float64]bool{}
		for i := 0; i < 12; i++ {
			got := dy + gridBaseline(dy, float32(i)*line, scale)
			dev := float64(got) * float64(scale)
			// a thousandth of a pixel is float32 noise, not a phase
			phases[math.Round(math.Abs(dev-math.Round(dev))*1000)/1000] = true
		}
		if len(phases) != 1 {
			t.Fatalf("dy %g: %d phases down twelve lines %v, want one", dy, len(phases), phases)
		}
	}
}

// TestUnsnappedBaselinesWalk is the other half: without the snapping the phase
// steps a quarter of a pixel every two or three lines, which is the seam.
func TestUnsnappedBaselinesWalk(t *testing.T) {
	const line, scale = 17 * 1.18, 1.5
	phases := map[float64]bool{}
	for i := 0; i < 12; i++ {
		dev := float64(i) * line * scale
		phases[math.Round(math.Abs(dev-math.Round(dev))*4)/4] = true
	}
	if len(phases) < 3 {
		t.Fatalf("twelve unsnapped lines stand at %d phases (%v), want several", len(phases), phases)
	}
}

// TestSnappedBaselineMovesWithTheColumn is the property the glide rests on: a
// snapped baseline travels with the column pixel for pixel, so scrolling stays
// one picture moved and a line is never rasterized at a new phase mid-glide.
func TestSnappedBaselineMovesWithTheColumn(t *testing.T) {
	const scale, y = 1.5, 7.25
	for _, top := range []float32{0, 78, 78.3333, 123.5} {
		first := float64(top+gridBaseline(top, y, scale)) * scale
		for i := 1; i <= 6; i++ {
			// the column moved up i device pixels
			at := top - float32(i)/scale
			got := float64(at+gridBaseline(at, y, scale)) * scale
			if diff := got - (first - float64(i)); math.Abs(diff) > 1e-4 {
				t.Fatalf("top %g: a line moved %.4f device px for a column that moved %d", top, diff, i)
			}
		}
	}
}

// TestTheAtlasHoldsOnePhasePerColumn is the seam, measured on the atlas: with
// the baselines on the grid every glyph of a column is rasterized at one place
// vertically, where without it each line brings the near copy its own phase
// asked for — the seam, paid for in bitmaps as well as seen.
func TestTheAtlasHoldsOnePhasePerColumn(t *testing.T) {
	l, up, _ := benchScrollColumn(t)
	useAtlas(t, "1")
	const scale, scrollY = 1.5, 4000
	count := func() map[uint8]int {
		up.glyphs = nil // a fresh atlas
		paintViewport(l, up, scale, scrollY)
		phases := map[uint8]int{}
		for key := range up.glyphs.index {
			phases[key.phase>>2]++ // the y part of the phase
		}
		return phases
	}
	on := count()
	if len(on) != 1 || on[0] == 0 {
		t.Fatalf("a column on the grid stands at %v vertically, want one phase", on)
	}
	textGrid = false
	t.Cleanup(func() { textGrid = true })
	off := count()
	if len(off) < 3 {
		t.Fatalf("a column off the grid stands at %v vertically, want the seam back", off)
	}
	t.Logf("atlas phases: %v on the grid, %v without", on, off)
}
