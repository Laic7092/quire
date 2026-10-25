package main

import (
	"context"
	"image"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/reader"
	"github.com/laix/layx-reader/locales"
)

// newScrollWindow is newWindow with a scrolling column: the sample book laid out
// with render.ModeScroll at the sheet the reader measures, and the app told the
// column is what is on screen.
func newScrollWindow(t *testing.T) (*app, *ui.Tester) {
	t.Helper()
	a := newWindow(t)
	a.opts.Scroll = true
	tt := ui.NewTester(a.view, 1000, 760)
	tt.Frame()

	// A scrolling book has one column, so the sheet is one maximum page wide
	// and as tall as the area the frame measured between the bars.
	layout, err := reader.BuildLayout(context.Background(), a.doc, a.fonts,
		pageMaxWidth, a.boxH, reader.Options{FontSize: reader.DefaultFontSize, Scroll: true})
	if err != nil {
		t.Fatal(err)
	}
	if layout.ContentHeight() <= a.boxH {
		t.Fatalf("the sample book is only %.0f tall; it does not scroll", layout.ContentHeight())
	}
	a.layout = layout
	a.layScroll = true
	a.tileH = a.boxH
	a.page = 0
	a.scrollY = 0
	a.scrollChapter = -1
	a.clearSelection()
	if a.pan != nil {
		if loc, ok := a.currentLoc(); ok {
			a.pan.RefreshAt(a.book, layout, loc)
		}
	}
	tt.Frame()
	return a, tt
}

// sheetRect is the page box the reader draws into, as the frame laid it out.
func sheetRect(t *testing.T, tt *ui.Tester) ui.Rect {
	t.Helper()
	r, ok := tt.Find(locales.PageLabel())
	if !ok {
		t.Fatalf("no sheet in the window: %q", tt.Texts())
	}
	return r
}

// The scrolling column draws, moves with the wheel and the keys, and reports
// its place as a percentage instead of a page number.
func TestScrollModeScrollsTheColumn(t *testing.T) {
	a, tt := newScrollWindow(t)
	if !tt.HasText("0%") {
		t.Fatalf("the folio starts at %q", tt.Texts())
	}
	if tt.HasText("1 of ") {
		t.Errorf("a scroll layout printed a page number: %q", tt.Texts())
	}

	r := sheetRect(t, tt)
	x, y := r.X+r.W/2, r.Y+r.H/2
	tt.Wheel(x, y, 0, 240)
	tt.Frame()
	if a.scrollY <= 0 {
		t.Fatalf("the wheel did not move the column: scrollY = %.0f", a.scrollY)
	}
	if got, want := a.pageLabel(), tt.Texts(); !contains(want, got) {
		t.Errorf("the folio does not say %q: %q", got, want)
	}

	// A click on the text is not a page turn, and does not move the column: a
	// scrolling reader clicks to place the caret, not to skip a screen.
	before := a.scrollY
	tt.ClickAt(x, y)
	tt.Frame()
	if a.scrollY != before {
		t.Errorf("a click scrolled from %.0f to %.0f", before, a.scrollY)
	}

	// Page keys move a screenful, the arrows a few lines, Home and End go to
	// the ends.
	tt.Key(0, ui.KeyRight)
	tt.Frame()
	if a.scrollY <= before {
		t.Errorf("the right arrow did not scroll: %.0f", a.scrollY)
	}
	step := a.scrollY
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if a.scrollY <= step {
		t.Errorf("the down arrow did not scroll: %.0f", a.scrollY)
	}
	tt.Key(0, ui.KeyEnd)
	tt.Frame()
	if a.scrollY != a.maxScroll() {
		t.Errorf("End left the column at %.0f of %.0f", a.scrollY, a.maxScroll())
	}
	if !tt.HasText("100%") {
		t.Errorf("the end of the book is not 100%%: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyHome)
	tt.Frame()
	if a.scrollY != 0 {
		t.Errorf("Home left the column at %.0f", a.scrollY)
	}
	if !tt.HasText("0%") {
		t.Errorf("the top of the book is not 0%%: %q", tt.Texts())
	}
}

// Dragging in the column selects the lines it crosses, and the rectangles are
// content rectangles with no page of their own.
func TestScrollModeSelectsText(t *testing.T) {
	a, tt := newScrollWindow(t)
	r := sheetRect(t, tt)
	x := r.X + r.W/2

	tt.Press(x, r.Y+80)
	tt.Move(x, r.Y+220)
	tt.Release(x, r.Y+220)
	tt.Frame()

	if len(a.selQuads) == 0 {
		t.Fatalf("the drag selected nothing: %q", tt.Texts())
	}
	for _, q := range a.selQuads {
		if q.Page != -1 {
			t.Errorf("a quad carries page %d, want -1 in scroll mode", q.Page)
		}
		if q.Content.Empty() {
			t.Error("a quad has no content rectangle")
		}
	}
	if text := a.selectedText(); text == "" {
		t.Error("nothing was selected")
	}
	if !tt.HasText(locales.SelectionSelected()) {
		t.Errorf("the folio does not report the selection: %q", tt.Texts())
	}

	// Scrolling keeps the selection: its rectangles are content coordinates,
	// so they move with the text rather than being dropped.
	quads := len(a.selQuads)
	before := a.scrollY
	tt.Wheel(x, r.Y+r.H/2, 0, 120)
	tt.Frame()
	if a.scrollY == before {
		t.Fatal("the wheel did not scroll")
	}
	if len(a.selQuads) != quads {
		t.Errorf("scrolling dropped the selection: %d quads, had %d", len(a.selQuads), quads)
	}

	// A jump from the table of contents moves the column to that chapter.
	if a.pan == nil || len(a.pan.Toc.Entries) < 2 {
		t.Fatalf("no second chapter to jump to")
	}
	e := a.pan.Toc.Entries[1]
	if !e.HasLoc {
		t.Fatal("the second chapter has no location")
	}
	top, ok := a.layout.OffsetOf(e.Loc)
	if !ok {
		t.Fatal("the chapter's location does not resolve")
	}
	a.gotoLoc(e.Loc)
	tt.Frame()
	if a.scrollY != clampf(top, 0, a.maxScroll()) {
		t.Errorf("jumped to %.0f, want the chapter top %.0f", a.scrollY, top)
	}
	if a.chapterNumber() != e.Chapter+1 {
		t.Errorf("chapter number %d after jumping to %d", a.chapterNumber(), e.Chapter+1)
	}
}

// Switching between pages and scrolling keeps the place: the option change
// remembers where the reader was for the layout that replaces this one.
func TestScrollModeKeepsThePlaceAcrossModes(t *testing.T) {
	a, _ := newScrollWindow(t)
	a.scrollTo(a.maxScroll() / 3)
	loc, ok := a.currentLoc()
	if !ok {
		t.Fatal("no reading location")
	}
	before, ok := a.layout.OffsetOf(loc)
	if !ok {
		t.Fatal("the reading location does not resolve")
	}

	o := a.opts
	o.Scroll = false
	a.setOptions(o)
	if !a.jumpAt {
		t.Fatal("the mode change forgot the reading position")
	}
	if a.jumpLoc.Chapter != loc.Chapter {
		t.Errorf("remembered chapter %d, was %d", a.jumpLoc.Chapter, loc.Chapter)
	}
	if after, ok := a.layout.OffsetOf(a.jumpLoc); !ok || after != before {
		t.Errorf("remembered offset %.0f, was at %.0f", after, before)
	}
	if a.opts.Scroll {
		t.Error("the scroll option is still on after switching to pages")
	}
}

// contains reports whether any of texts has want.
func contains(texts []string, want string) bool {
	for _, s := range texts {
		if s == want {
			return true
		}
	}
	return false
}

// The scroll layout's tiles start where the flow does: the first one is at
// content 0 and the last one covers the end of the book.
func TestScrollTilesCoverTheFlow(t *testing.T) {
	a, _ := newScrollWindow(t)
	layout := a.layout
	if n := layout.TileCount(); n < 2 {
		t.Fatalf("tile count = %d", n)
	}
	if first := layout.Tile(0); first == nil || first.Top() != 0 {
		t.Fatalf("first tile top is not 0")
	}
	last := layout.Tile(layout.TileCount() - 1)
	if last == nil {
		t.Fatal("the last tile is nil")
	}
	if last.Top()+last.Height() < layout.ContentHeight()-1 {
		t.Errorf("the last tile ends at %.0f, the book at %.0f",
			last.Top()+last.Height(), layout.ContentHeight())
	}
	// The renderer numbers pages in a paged layout only.
	if p := layout.Page(0); p != nil {
		t.Error("a scroll layout handed out a page")
	}
}

// The display settings carry the mode switch: the switch turns scrolling off
// again, and the setting is what the next layout is built from.
func TestScrollSwitchInTheDisplaySettings(t *testing.T) {
	a, tt := newScrollWindow(t)
	a.aaOpen, a.aaMore = true, true
	a.syncSettingsFields()
	tt.Frame()
	if !tt.HasText(locales.SettingsScrolling()) {
		t.Fatalf("the display settings have no scrolling switch: %q", tt.Texts())
	}
	if err := tt.Click(locales.SettingsScrolling()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.opts.Scroll {
		t.Error("the scrolling switch did not turn scrolling off")
	}
	if !a.jumpAt {
		t.Error("turning scrolling off forgot the reading position")
	}
}

// A wheel's notches glide the column to their target instead of jumping: the
// distance is stepped from the clock, the notches on the way add to it, and the
// column lands where they asked. A burst in one frame only owes the lead, so the
// column is never sent a run of steps it has to sprint through.
func TestScrollGlidesToTheWheelTarget(t *testing.T) {
	a, _ := newScrollWindow(t)
	if a.maxScroll() < 700 {
		t.Skipf("the sample book only scrolls %.0f", a.maxScroll())
	}
	a.instant = false
	at := time.Now()
	a.now = func() time.Time { return at }

	a.scrollBy(100)
	a.scrollBy(100)
	if want := float32(100) * scrollLead; a.scrollGoal != want {
		t.Fatalf("two notches in one frame sent the column to %.0f, want %.0f", a.scrollGoal, want)
	}
	if !a.gliding() {
		t.Fatal("the column did not start gliding")
	}
	if a.scrollY != 0 {
		t.Errorf("an ask moved the column before a frame did: %.0f", a.scrollY)
	}

	// The frames cover the distance, a share of what is left each, until the step
	// reaches scrollTopSpeed; then it travels at that speed and eases into the
	// place. The first frame is the longest, the last the shortest, and it lands.
	const frame = 7 * time.Millisecond
	var moves []float32
	for i := 0; i < 80 && a.gliding(); i++ {
		at = at.Add(frame)
		before := a.scrollY
		a.stepScroll()
		moves = append(moves, a.scrollY-before)
	}
	if a.gliding() {
		t.Fatalf("the column never landed: it owes %.1f", a.scrollGoal-a.scrollY)
	}
	if a.scrollY != 100 {
		t.Errorf("landed at %.1f, want 100", a.scrollY)
	}
	if len(moves) < 3 {
		t.Fatalf("the column covered 100 DIPs in %d frames", len(moves))
	}
	if moves[0] <= moves[len(moves)-1] {
		t.Errorf("the column does not ease in: it moved %.1f then %.1f", moves[0], moves[len(moves)-1])
	}
	if len(moves) > 60 {
		t.Errorf("the column took %d frames (%s) to cover 100 DIPs", len(moves), time.Duration(len(moves))*frame)
	}

	// A place far away is travelled at scrollTopSpeed rather than whipped there:
	// no frame of the jump moves more than a frame's worth of that speed.
	a.scrollTo(a.maxScroll())
	fastest := float32(scrollTopSpeed) * float32(frame) / float32(time.Second)
	for i := 0; i < 5 && a.gliding(); i++ {
		at = at.Add(frame)
		before := a.scrollY
		a.stepScroll()
		if moved := a.scrollY - before; moved > fastest+0.01 {
			t.Errorf("a jump moved %.1f DIPs in %s, want at most %.1f", moved, frame, fastest)
		}
	}
}

// A touchpad's deltas are the movement itself: they land in the frame they
// arrive in, and stack from where the column is drawn, so the text never trails
// the hand. A wheel's notch still glides to its target.
func TestScrollPreciseDeltasLandAtOnce(t *testing.T) {
	a, tt := newScrollWindow(t)
	r := sheetRect(t, tt)
	x, y := r.X+r.W/2, r.Y+r.H/2
	a.instant = false
	at := time.Now()
	a.now = func() time.Time { return at }

	tt.Scroll(x, y, 0, 40)
	if a.gliding() {
		t.Fatalf("a precise delta started a glide, to %.0f", a.scrollGoal)
	}
	if a.scrollY != 40 {
		t.Fatalf("a precise delta of 40 put the column at %.2f, want 40", a.scrollY)
	}
	tt.Scroll(x, y, 0, 12.5)
	if a.scrollY != 52.5 {
		t.Fatalf("a second delta stacked to %.2f, want 52.5", a.scrollY)
	}
	// Nothing is on its way, so the frames after the gesture do not move it.
	at = at.Add(50 * time.Millisecond)
	tt.Frame()
	if a.scrollY != 52.5 {
		t.Errorf("the column drifted to %.2f after the gesture", a.scrollY)
	}
}

// Whatever asks for a distance is the same ask: a key's step and a wheel's notch
// take the same motion, and asks that keep coming are followed at the rate the
// follow can carry — with one ask's worth of lag, and a short coast after the
// last one rather than a run. A hold asks faster than that, so the column stays
// one step behind and feeds itself a step at a time: it never piles the hold up
// into a sprint, which is what two steps of lead looked like at a key's repeat
// rate.
func TestScrollAsksCruiseAtTheirRate(t *testing.T) {
	a, _ := newScrollWindow(t)
	step := 3 * a.opts.FontSize * a.opts.LineHeight
	if a.maxScroll() < 200*step {
		t.Skipf("the sample book only scrolls %.0f", a.maxScroll())
	}
	a.instant = false
	at := time.Now()
	a.now = func() time.Time { return at }

	// A hold: asks every 40 ms — a key's step and a wheel's notch by turns, which
	// is the same ask — and frames every 7 ms, as a 144 Hz display draws them.
	const every, frame = 40 * time.Millisecond, 7 * time.Millisecond
	next := at.Add(every)
	asked := 0
	var moves []float32
	for i := 0; i < 120; i++ {
		at = at.Add(frame)
		if !at.Before(next) {
			if asked%2 == 0 {
				a.scrollLines(3)
			} else {
				a.scrollBy(step)
			}
			asked++
			next = next.Add(every)
		}
		before := a.scrollY
		a.stepScroll()
		moves = append(moves, a.scrollY-before)
		if lead := a.scrollGoal - a.scrollY; lead > scrollLead*step+0.01 {
			t.Fatalf("ask %d left the column %.1f behind, want %d step(s) (%.1f)", asked, lead, scrollLead, scrollLead*step)
		}
	}
	// The asks ask for one step every 40 ms, faster than the follow can carry:
	// the column moves what it can, and never more than was asked for.
	var sum float32
	for _, d := range moves[len(moves)-40:] {
		sum += d
	}
	got := sum / (40 * float32(frame) / float32(time.Second))
	want := step / float32(every) * float32(time.Second)
	if got > want*1.02 {
		t.Errorf("the column ran ahead of the asks at %.0f DIPs a second, want at most %.0f (one step every %s)", got, want, every)
	}
	if got < want*0.4 {
		t.Errorf("the column fell behind at %.0f DIPs a second, want more than %.0f (one step every %s)", got, want*0.4, every)
	}

	// The asking stops: what is left is one step, and it lands.
	at = at.Add(400 * time.Millisecond)
	a.stepScroll()
	if a.gliding() {
		t.Errorf("the column is still gliding %s after the last ask", 400*time.Millisecond)
	}
}

// The column is drawn on whole device pixels, so two offsets inside the same
// pixel print the same page: a glyph keeps one subpixel phase while the reader
// is between two of them, instead of being swapped for a near copy of itself —
// and moved by half a pixel — on every frame of the gesture.
func TestScrollDrawsOnWholePixels(t *testing.T) {
	if !snapScroll {
		t.Skip("LAYX_NO_SNAP turned the snapping off")
	}
	a, tt := newScrollWindow(t)
	if a.maxScroll() < 2 {
		t.Skipf("the sample book only scrolls %.0f", a.maxScroll())
	}
	r := sheetRect(t, tt)
	a.instant = false
	at := time.Now()
	a.now = func() time.Time { return at }
	draw := func(y float32) *image.RGBA {
		a.scrollAt = time.Time{}
		a.scrollGoal, a.scrollY = y, y
		tt.Frame()
		return tt.Image()
	}
	mid := a.maxScroll() / 2
	first := draw(mid + 0.2)
	second := draw(mid + 0.35)
	differ := 0
	for py := int(r.Y); py < int(r.Y+r.H); py++ {
		for px := int(r.X); px < int(r.X+r.W); px++ {
			if !image.Pt(px, py).In(first.Bounds()) {
				continue
			}
			if first.RGBAAt(px, py) != second.RGBAAt(px, py) {
				differ++
			}
		}
	}
	if differ != 0 {
		t.Errorf("%d pixels changed between two offsets inside one pixel", differ)
	}
}

// A line belongs to the tile that holds its top edge, so a line near a tile
// boundary overhangs into the next tile's area. Drawing the lower tile first
// keeps that overhang on top of the paper instead of letting the next tile's
// opaque sheet cut it in half mid-screen.
func TestScrollDoesNotCutLinesAtTileBoundaries(t *testing.T) {
	a, tt := newScrollWindow(t)
	r := sheetRect(t, tt)

	// Find a tile boundary that falls inside a line of text.
	var boundary float32
	for k := 1; k < a.layout.TileCount(); k++ {
		y := float32(k) * a.tileH
		before, ok1 := a.layout.LocationAt(y - 0.5)
		after, ok2 := a.layout.LocationAt(y + 0.5)
		if ok1 && ok2 && before == after {
			boundary = y
			break
		}
	}
	if boundary == 0 {
		t.Skip("no line straddles a tile boundary in this book")
	}

	// Put the boundary in the middle of the column and draw it.
	y := boundary - a.scrollViewport()/2
	a.scrollTo(y)
	tt.Frame()
	img := tt.Image()
	at := r.Y + scrollInset + (boundary - y)

	x0, x1 := int(r.X+40), int(r.W+r.X-40)
	above := inkPixels(img, x0, int(at)-8, x1, int(at)-1)
	below := inkPixels(img, x0, int(at)+1, x1, int(at)+8)
	if above == 0 {
		t.Skipf("the band above the boundary has no ink to compare")
	}
	if below*3 < above {
		t.Errorf("the line is cut at the tile boundary: %d ink pixels above it, %d below", above, below)
	}
}

// inkPixels counts the pixels in a rectangle that are not the paper: text is
// drawn in the palette's ink, which differs from the sheet around it.
func inkPixels(img *image.RGBA, x0, y0, x1, y1 int) int {
	paper := img.RGBAAt(x0, y0-20)
	n := 0
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			p := img.RGBAAt(x, y)
			diff := absInt(int(p.R)-int(paper.R)) + absInt(int(p.G)-int(paper.G)) +
				absInt(int(p.B)-int(paper.B))
			if diff > 120 {
				n++
			}
		}
	}
	return n
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
