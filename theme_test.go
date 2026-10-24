package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/reader"
)

// TestThemeSwitchKeepsTheBook pins a paper change to a repaint of the layout on
// screen: the document stands (so the search index, the images and the reader's
// place stand with it), every page keeps its line boxes and its place, and the
// frame that applies the palette is the same frame that draws the new colours.
func TestThemeSwitchKeepsTheBook(t *testing.T) {
	a := newWindow(t)
	tt := ui.NewTester(a.view, 1180, 800)
	tt.Frame()
	// The layout newWindow built is not at the tester's own measure: give it the
	// sheet this window lays out, as the first frames of a real one would.
	sw, sh := a.sheetSize(a.boxW, a.boxH, a.opts)
	a.layW, a.layH, a.layOpts = sw, sh, a.opts
	a.layPaper = paperOf(reader.Current())
	doc, base, page := a.doc, a.layout, a.page

	a.setTheme(reader.ThemeBold)
	tt.Frame()
	if got := reader.Current().Theme; got != reader.ThemeBold {
		t.Fatalf("paper is %q after a theme change, want %q", got, reader.ThemeBold)
	}
	// The palette and the page were both applied inside that frame: no job was
	// started, so the page cannot lag behind the chrome.
	if a.loading || a.job != 0 {
		t.Fatalf("a theme change started a layout job (loading=%v, job=%d)", a.loading, a.job)
	}
	if a.layout == base {
		t.Fatal("the frame drew the old paper")
	}
	if a.layPaper != paperOf(reader.Current()) {
		t.Fatal("the published layout is not on the current paper")
	}
	if a.doc != doc {
		t.Fatal("a theme change rebuilt the document")
	}
	if got := a.layout.Document(); got != doc {
		t.Fatal("the recoloured layout does not belong to the book")
	}
	if a.layout.PageCount() != base.PageCount() {
		t.Fatalf("page count moved on a theme change: %d -> %d", base.PageCount(), a.layout.PageCount())
	}
	if a.page != page {
		t.Fatalf("the reading place moved on a theme change: page %d -> %d", page, a.page)
	}
	if got, want := a.layout.Typography().Color, reader.Current().Ink; got != want {
		t.Fatalf("pages print in %v, want the paper's ink %v", got, want)
	}
	if got, want := a.layout.Typography().LinkColor, reader.Current().Link; got != want {
		t.Fatalf("links are drawn in %v, want the paper's link colour %v", got, want)
	}
}
