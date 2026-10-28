package main

import (
	"image/color"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx/render"
	"github.com/laix/quire/internal/appstate"
)

// The window is the sheet: the paper covers every edge of it, and the text keeps
// the band the bars leave it, so the measure the book is laid out at never
// depends on the chrome. The bar is a layer over the sheet and not a row of it:
// it takes no room from the page.
func TestTheSheetIsTheWholeWindowUnderTheChrome(t *testing.T) {
	a := newWindow(t)
	var surface, area, body, bar, window ui.Rect
	tt := ui.NewTester(func(c *ui.Context) {
		a.view(c)
		// The committed frame's boxes, which is what the draw follows.
		surface, area, body = a.surface.Bounds(c), a.area.Bounds(c), a.body.Bounds(c)
		bar = a.headBar.Bounds(c)
		w, h := c.Size()
		window = ui.Rect{W: w, H: h}
	}, 1180, 800)
	tt.SetTitleBar(ui.TitleBar{Height: headerHeight})
	tt.Frame()
	tt.Frame()

	if surface != window {
		t.Errorf("the sheet covers %v of a %v window: the paper stops short of the edges", surface, window)
	}
	if bar.Y != 0 || bar.H != headerHeight || bar.W != window.W {
		t.Errorf("the bar is %v, not the window's first %v DIPs", bar, headerHeight)
	}
	// The text sits in the band between the bars, as it did when they were rows
	// of the window: what a page holds is untouched by the layer they moved to.
	if body.Y != headerHeight {
		t.Errorf("the page starts at y=%v, want the bar's %v", body.Y, headerHeight)
	}
	if end, want := body.Y+body.H, window.H-footerHeight; end != want {
		t.Errorf("the page ends at y=%v, want the folio's %v", end, want)
	}
	if area.W != window.W {
		t.Errorf("the page area is %v wide in a %v window", area.W, window.W)
	}
}

// The bars go away as the reader moves down a scrolling column and come back as
// the reader moves up it, fading over the frames between: the frames of the fade
// only paint, so a glide down the column still builds nothing.
func TestTheBarsGoAwayDownTheColumn(t *testing.T) {
	a, tt := newScrollWindow(t)
	// A pixel in the capsule at the head of the window: its fill while the bar
	// is up, the paper's own once it has gone.
	const x, y = 20, 20
	p := paper()
	paperPixel := color.RGBA{R: p.R, G: p.G, B: p.B, A: 255}
	if got := tt.Image().RGBAAt(x, y); got == paperPixel {
		t.Fatalf("the bar's capsule is not drawn at (%d, %d): %v", x, y, got)
	}

	tt.Scroll(500, 400, 0, 120)
	if a.chromeShown() {
		t.Errorf("the bars stand out after the reader moved down the column")
	}
	tt.Advance(2 * chromeFade)
	tt.Frame()
	if got := tt.Image().RGBAAt(x, y); got != paperPixel {
		t.Errorf("the head of the window is %v with the bar away, want the paper %v", got, paperPixel)
	}

	tt.Scroll(500, 400, 0, -120)
	if !a.chromeShown() {
		t.Errorf("the bars are still away after the reader moved back up the column")
	}
	tt.Advance(2 * chromeFade)
	tt.Frame()
	if tt.Image().RGBAAt(x, y) == paperPixel {
		t.Errorf("the head of the window is bare: the bar did not come back")
	}
}

// A paged book has nothing to put away. Its pages are turned and not scrolled,
// so the bars stay where they are however far the reader goes.
func TestAPagedBookKeepsItsBars(t *testing.T) {
	a := newWindow(t)
	tt := ui.NewTester(a.view, 1180, 800)
	tt.Frame()
	a.turn(1)
	tt.Frame()
	if !a.chromeShown() {
		t.Errorf("a paged book put its bars away")
	}
}

// The bars come back for anything the reader needs them for: the pointer where
// they are, the panel whose rows they open, and a selection to act on.
func TestTheBarsComeBackForTheReader(t *testing.T) {
	a, tt := newScrollWindow(t)
	away := func(what string) {
		t.Helper()
		tt.Scroll(500, 400, 0, 120)
		if a.chromeShown() {
			t.Fatalf("the bars stand out %s", what)
		}
	}
	away("before the reader moved")

	// The pointer at the head of the window, where the bar is.
	tt.Move(500, headerHeight/2)
	if !a.chromeShown() {
		t.Errorf("the pointer at the head of the window left the bars away")
	}

	// The pointer at the foot of the window, where the folio is.
	tt.Move(500, 400)
	away("after the pointer left the head")
	tt.Move(500, 759)
	if !a.chromeShown() {
		t.Errorf("the pointer at the foot of the window left the bars away")
	}

	// The table of contents, whose rows the bar opens.
	tt.Move(500, 400)
	away("after the pointer left the foot")
	a.pan.Show(appstate.SidebarToc)
	if !a.chromeShown() {
		t.Errorf("the contents panel left the bars away")
	}
	a.pan.Show(appstate.SidebarNone)

	// A selection, which the folio says what to do with.
	a.selQuads = []render.SelectionQuad{{}}
	if !a.chromeShown() {
		t.Errorf("a selection left the bars away")
	}
	a.clearSelection()
	if a.chromeShown() {
		t.Errorf("the bars stand out again with nothing to show")
	}
}
