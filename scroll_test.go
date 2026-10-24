package main

import (
	"context"
	"image"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/reader"
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
	r, ok := tt.Find("书页")
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
	if !tt.HasText("Selected") {
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
	if !tt.HasText("Scrolling") {
		t.Fatalf("the display settings have no scrolling switch: %q", tt.Texts())
	}
	if err := tt.Click("Scrolling"); err != nil {
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
// place is stepped from the clock, and the notches on the way stack.
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
	if a.scrollGoal != 200 {
		t.Fatalf("two notches sent the column to %.0f, want 200", a.scrollGoal)
	}
	if !a.gliding() {
		t.Fatal("the column did not start gliding")
	}
	at = at.Add(scrollGlideDuration / 2)
	a.stepScroll()
	if a.scrollY <= 0 || a.scrollY >= 200 {
		t.Errorf("half way down the glide, scrollY = %.0f of 200", a.scrollY)
	}
	if !a.gliding() {
		t.Error("the glide landed at half its time")
	}

	at = at.Add(scrollGlideDuration)
	a.stepScroll()
	if a.gliding() {
		t.Error("the glide never landed")
	}
	if a.scrollY != 200 {
		t.Errorf("landed at %.0f, want 200", a.scrollY)
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
