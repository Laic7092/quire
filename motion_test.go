package main

import (
	"image"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/reader"
)

// A glide paints the column without building the window: the view is built once
// for a gesture — on the frame the ask arrived in — and the frames after it only
// paint, apart from the build every folioEvery that keeps the bar under the text
// on the place the column has reached. The frame that lands asks for the build
// that writes the place down and leaves the folio there.
func TestGlidePaintsWithoutBuilding(t *testing.T) {
	builds := 0
	a, tt := newScrollWindowView(t, func(c *ui.Context, view func(*ui.Context)) {
		builds++
		view(c)
	})
	a.instant = false
	a.now = tt.Now // the glide is measured on the frames the tester runs
	tt.Frame()
	builds = 0

	r := sheetRect(t, tt)
	tt.Wheel(r.X+r.W/2, r.Y+r.H/2, 0, 120)
	if !a.gliding() {
		t.Fatalf("the wheel started no glide: it owes %.1f", a.scrollGoal-a.scrollY)
	}
	// The ask itself is built, as an event's frame is (and the tester settles an
	// event with up to twenty passes); what is measured here is the glide after
	// it, which starts once the wheel's event has settled.

	// The frames of the glide paint the column and ask for the next one
	// themselves: what builds among them is the folio's build, one in eight
	// frames or fewer.
	frames, after := 0, 0
	start := time.Now()
	for i := 0; i < 240 && a.gliding(); i++ {
		before := builds
		tt.Advance(17 * time.Millisecond)
		frames++
		if builds != before {
			after += builds - before
			// Every build of a glide is the folio's, and the folio is built at
			// most once a folioEvery of the time the glide has run.
			if limit := int(tt.Now().Sub(start)/folioEvery) + 1; after > limit {
				t.Fatalf("%d frames of the glide built the window %d times in %s, more than the folio's %d",
					frames, after, tt.Now().Sub(start), limit)
			}
		}
	}
	if a.gliding() {
		t.Fatalf("the glide never landed in %d frames: it still owes %.1f",
			frames, a.scrollGoal-a.scrollY)
	}
	if frames < 3 {
		t.Errorf("the glide took %d frames", frames)
	}
	if after*4 > frames {
		t.Errorf("%d of %d frames of the glide built the window", after, frames)
	}
	if a.scrollY != a.scrollGoal {
		t.Errorf("landed at %.2f, want the place it owes, %.2f", a.scrollY, a.scrollGoal)
	}

	// The frame after it builds, and the folio is drawn where the column stands:
	// a glide that only painted would leave the percentage behind.
	before := builds
	tt.Advance(17 * time.Millisecond)
	if builds != before+1 {
		t.Errorf("the frame after the glide built the window %d times, want one", builds-before)
	}
	if got := a.pageLabel(); !tt.HasText(got) {
		t.Errorf("the folio does not say %q after the glide: %q", got, tt.Texts())
	}
}

// A frame that only paints paints the same column a build would: the column's
// place, the tiles and the marks all come from the frame's own clock, and
// nothing of the view is needed to place them.
func TestRepaintPaintsTheSameColumn(t *testing.T) {
	a, tt := newScrollWindow(t)
	a.instant = false
	a.now = tt.Now
	tt.Frame()

	r := sheetRect(t, tt)
	tt.Wheel(r.X+r.W/2, r.Y+r.H/2, 0, 120)
	for i := 0; i < 240 && a.gliding(); i++ {
		tt.Advance(17 * time.Millisecond)
	}
	if a.gliding() {
		t.Fatal("the glide never landed")
	}
	// The frame the glide landed on painted without building; the build after
	// it lays the same window out of the same state. The folio below the sheet is
	// the one thing that changes — that is what the build is for — so the sheet
	// itself is what is compared.
	painted := copyImage(tt.Image())
	tt.Frame()
	built := tt.Image()
	if differs, total := differing(painted, built, crop(r)); differs != 0 {
		t.Errorf("painting the sheet again drew something else: %d of %d bytes differ", differs, total)
	}
}

// crop narrows the comparison to the pixels of r, in device pixels.
func crop(r ui.Rect) image.Rectangle {
	return image.Rect(int(r.X), int(r.Y), int(r.X+r.W), int(r.Y+r.H))
}

// copyImage copies the last frame, which the next one draws over.
func copyImage(img *image.RGBA) *image.RGBA {
	out := image.NewRGBA(img.Rect)
	copy(out.Pix, img.Pix)
	return out
}

// differing counts the bytes two images do not share inside box, and how many
// bytes box holds.
func differing(a, b *image.RGBA, box image.Rectangle) (n, total int) {
	box = box.Intersect(a.Rect).Intersect(b.Rect)
	for y := box.Min.Y; y < box.Max.Y; y++ {
		row := a.Pix[y*a.Stride:]
		other := b.Pix[y*b.Stride:]
		for x := 4 * box.Min.X; x < 4*box.Max.X; x++ {
			total++
			if row[x] != other[x] {
				n++
			}
		}
	}
	return n, total
}

// A page turn paints the sheet without building the window too: the slide's
// place is a function of the clock, so the frames that show it only paint. The
// build at the end settles the turn, and starts the one a held key asked for
// while it was on its way.
func TestTurnPaintsWithoutBuilding(t *testing.T) {
	a := newWindow(t)
	a.instant = false
	builds := 0
	var tt *ui.Tester
	tt = ui.NewTester(func(c *ui.Context) {
		builds++
		a.view(c)
	}, 1180, 800)
	a.now = tt.Now
	tt.Frame()
	builds = 0

	from := a.page
	tt.Key(0, ui.KeyRight)
	if a.turning == nil {
		t.Fatal("the right arrow started no turn")
	}
	if a.page == from {
		t.Fatalf("the turn did not move from page %d", from+1)
	}
	ask := builds

	frames := 0
	for i := 0; i < 240 && a.turning != nil; i++ {
		before := builds
		tt.Advance(17 * time.Millisecond)
		frames++
		if builds != before && a.turning != nil {
			// The one build the turn is allowed is the one that settles it.
			if _, moving := a.turnProgress(); moving {
				t.Fatalf("frame %d of the turn built the window while the sheet was moving", frames)
			}
		}
	}
	if a.turning != nil {
		t.Fatalf("the turn never landed in %d frames", frames)
	}
	if frames < 3 {
		t.Errorf("the turn took %d frames", frames)
	}
	if builds != ask+1 {
		t.Errorf("the turn built the window %d times after the ask, want one at the end", builds-ask)
	}
	if got := a.pageLabel(); !tt.HasText(got) {
		t.Errorf("the folio does not say %q after the turn: %q", got, tt.Texts())
	}
}

// A turn slides the strip in whole device pixels, as the scrolling column moves,
// so both spreads keep one glyph phase for the whole slide: what a turn costs
// the atlas is the bitmaps of the spread arriving, not a fresh set of them for
// every pixel the strip passes. Measured against the same turn unsnapped — the
// switch LAYX_NO_SNAP sets — it is the difference between a few hundred
// rasterizations a turn and thousands.
func TestTurnKeepsOneGlyphPhase(t *testing.T) {
	rastered, slots := turnGlyphs(t, true)
	unsnapped, unsnappedSlots := turnGlyphs(t, false)
	t.Logf("snapped: %d rasterized, %d slots; unsnapped: %d rasterized, %d slots",
		rastered, slots, unsnapped, unsnappedSlots)
	if rastered*4 > unsnapped {
		t.Errorf("a snapped turn rasterized %d glyphs, want far fewer than the %d of an unsnapped one",
			rastered, unsnapped)
	}
	if slots*4 > unsnappedSlots*3 {
		t.Errorf("a snapped turn left %d glyphs in the atlas, want well under the %d of an unsnapped one",
			slots, unsnappedSlots)
	}
}

// turnGlyphs runs one turn to its end and returns what it rasterized and how
// many glyphs it left in the atlas.
func turnGlyphs(t *testing.T, snap bool) (rastered, slots int) {
	t.Helper()
	old := snapScroll
	snapScroll = snap
	t.Cleanup(func() { snapScroll = old })
	reader.CountGlyphs(true)
	t.Cleanup(func() { reader.CountGlyphs(false) })

	a := newWindow(t)
	a.instant = false
	at := time.Now()
	a.now = func() time.Time { return at }
	tt := ui.NewTester(a.view, 1180, 800)
	tt.SetScale(2)
	tt.Frame()
	a.paint.ResetGlyphStats()
	a.turn(1)
	if a.turning == nil {
		t.Fatal("the turn did not start")
	}
	a.services.Invalidate()
	for i := 0; i < 60 && a.turning != nil; i++ {
		at = at.Add(17 * time.Millisecond)
		tt.Frame()
	}
	if a.turning != nil {
		t.Fatal("the turn never landed")
	}
	s := a.paint.GlyphStats()
	return s.Rasters, s.Slots
}
