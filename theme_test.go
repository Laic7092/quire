package main

import (
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/laix/quire/internal/reader"
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
	a.layPaper = paperOf(a.palette())
	doc, base, page := a.doc, a.layout, a.page

	a.setTheme(reader.ThemeBold)
	tt.Frame()
	if got := a.palette().Theme; got != reader.ThemeBold {
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
	if a.layPaper != paperOf(a.palette()) {
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
	if got, want := a.layout.Typography().Color, a.palette().Ink; got != want {
		t.Fatalf("pages print in %v, want the paper's ink %v", got, want)
	}
	if got, want := a.layout.Typography().LinkColor, a.palette().Link; got != want {
		t.Fatalf("links are drawn in %v, want the paper's link colour %v", got, want)
	}
}

// TestLibraryChromeIgnoresBookAppearance pins the library to the desktop's own
// light/dark look. A focused book window forces MyGo's process-wide appearance
// so its title buttons match the paper; that must not turn the library behind
// it over, or closing the book would visibly turn it back again.
func TestLibraryChromeIgnoresBookAppearance(t *testing.T) {
	d := &desk{}

	// The desktop is light: remember that while MyGo follows it.
	if got := d.libraryChromeDark(false, true); got {
		t.Fatal("the library turned dark on a light desktop")
	}
	// A dark book forces the app's appearance; the library stays light.
	if got := d.libraryChromeDark(true, false); got {
		t.Fatal("a dark book turned the library's chrome dark")
	}
	// Closing the book returns to the desktop; still light, no visible flip.
	if got := d.libraryChromeDark(false, true); got {
		t.Fatal("the library changed when the book closed")
	}

	// A real desktop appearance change while the app follows it is followed.
	if got := d.libraryChromeDark(true, true); !got {
		t.Fatal("the library did not follow a dark desktop")
	}
	// A light book forced over that dark desktop leaves the library dark.
	if got := d.libraryChromeDark(false, false); !got {
		t.Fatal("a light book turned the library's chrome light")
	}
}

// TestWindowControlsFollowThePaper pins the appearance the window's controls
// are drawn in to the bar they sit on. MyGo paints minimize, maximize and close
// (and GTK's title buttons) in the appearance it was given, not in the window's
// palette, so a white page on a dark desktop drew them white on white, and a
// dark page on a light one black on black.
func TestWindowControlsFollowThePaper(t *testing.T) {
	cases := []struct {
		name   string
		follow bool
		paper  reader.Theme
		want   mygo.ThemeSource
	}{
		{"following the desktop", true, reader.ThemeFocus, mygo.ThemeSystem},
		{"the white page", false, reader.ThemeOriginal, mygo.ThemeLight},
		{"the cream page", false, reader.ThemeFocus, mygo.ThemeLight},
		{"the pale page", false, reader.ThemePaper, mygo.ThemeLight},
		{"the warm page", false, reader.ThemeCalm, mygo.ThemeLight},
		{"the graphite page", false, reader.ThemeQuiet, mygo.ThemeDark},
		{"the black page", false, reader.ThemeBold, mygo.ThemeDark},
	}
	for _, c := range cases {
		if got := windowSource(c.follow, c.paper); got != c.want {
			t.Errorf("%s: the controls are drawn in %q, want %q", c.name, got, c.want)
		}
	}
}
