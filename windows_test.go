package main

import (
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/laix/layx/render"
	"github.com/laix/quire/internal/book"
	"github.com/laix/quire/internal/reader"
	"github.com/laix/quire/locales"
)

// Each window is its own reader: a page turned, a font size changed or a book
// opened in one leaves every other window where it was. They share the store,
// which is what lets the shelf and each book's place outlive all of them.
func TestEachWindowKeepsItsOwnPlace(t *testing.T) {
	d := &desk{}
	one, two := d.newApp(), d.newApp()

	one.book = &book.Book{Title: "one"}
	one.page = 7
	one.opts.FontSize = 21
	two.book = &book.Book{Title: "two"}
	two.page = 3
	two.opts.FontSize = 16

	if got := one.book.Title; got != "one" {
		t.Errorf("first window reads %q", got)
	}
	if got := two.book.Title; got != "two" {
		t.Errorf("second window reads %q", got)
	}
	if one.page != 7 || two.page != 3 {
		t.Errorf("pages crossed windows: %d and %d", one.page, two.page)
	}
	if one.opts.FontSize != 21 || two.opts.FontSize != 16 {
		t.Errorf("font sizes crossed windows: %g and %g", one.opts.FontSize, two.opts.FontSize)
	}
}

// The paper is the app's, not a window's: the palette the pages are laid out
// with is the process's, so one window changing it turns every window over
// together instead of letting them repaint in each other's colours.
func TestAPaperChangeTurnsEveryWindow(t *testing.T) {
	d := &desk{}
	one, two := d.newApp(), d.newApp()
	d.add(one)
	d.add(two)

	one.setTheme(reader.ThemeBold)
	if one.theme != reader.ThemeBold || two.theme != reader.ThemeBold || d.theme != reader.ThemeBold {
		t.Fatalf("after the first window's change: %q, %q, %q",
			one.theme, two.theme, d.theme)
	}
	two.setTheme(reader.ThemeNight)
	if one.theme != reader.ThemeNight || two.theme != reader.ThemeNight || d.theme != reader.ThemeNight {
		t.Fatalf("after the second window's change: %q, %q, %q",
			one.theme, two.theme, d.theme)
	}
	if one.themeSys || two.themeSys {
		t.Error("a paper the reader picked is not the system's")
	}
}

// Every book window gets its own frame in the app's state, so moving one does
// not move the others on the next launch, and none of them takes the library's
// key.
func TestEachBookWindowRemembersItsOwnFrame(t *testing.T) {
	d := &desk{}
	first, second, third := d.nextBookKey(), d.nextBookKey(), d.nextBookKey()
	for _, key := range []string{first, second, third} {
		if key == "main" {
			t.Errorf("a book window took the library's state key: %q", key)
		}
	}
	if first == second || second == third || first == third {
		t.Errorf("windows share a state key: %q, %q, %q", first, second, third)
	}
}

// One menu bar serves every window: a selection in the focused window enables
// page copy and add-note, whichever window installed the bar, and a second
// window with nothing selected puts them back.
func TestTheSharedMenuFollowsEachWindow(t *testing.T) {
	d := &desk{}
	one := d.newApp()
	d.add(one)
	one.menuBar()
	two := d.newApp()
	d.add(two)

	if two.menuCopy != d.menuCopy {
		t.Fatal("a window made after the menu did not take the shared items")
	}
	one.selQuads = []render.SelectionQuad{{}}
	one.syncMenu()
	if d.menuCopy.Disabled {
		t.Error("page copy stayed disabled with a selection")
	}
	two.selQuads = nil
	two.syncMenu()
	if !d.menuCopy.Disabled {
		t.Error("page copy stayed enabled after the focused window cleared it")
	}
}

// A window made after the reading faces were parsed can draw at once: it shares
// the immutable font set and gets its own painter, because glyphs and images are
// the window's. Reading a book from that window must not be the only path that
// builds the painter.
func TestAWindowMadeLaterCanDrawWithTheSharedFaces(t *testing.T) {
	d := &desk{fonts: &reader.Fonts{}}
	a := d.newApp()
	if a.fonts != d.fonts {
		t.Fatal("the window did not take the shared font set")
	}
	if a.paint == nil {
		t.Fatal("a window with faces but no painter cannot draw a page")
	}
	b := d.newApp()
	if b.fonts != d.fonts {
		t.Fatal("the second window did not take the shared font set")
	}
	if b.paint == nil || b.paint == a.paint {
		t.Errorf("painters are not one per window: %p and %p", a.paint, b.paint)
	}
}

// The desk knows which window already holds a file: that is how a book asked
// for twice comes forward instead of opening a second copy. A window without a
// book is the library, and it is never the one a book is opened in.
func TestTheDeskFindsTheWindowThatHoldsABook(t *testing.T) {
	d := &desk{}
	home := d.newApp()
	home.win = &mygo.Window{}
	d.add(home)
	d.library = home
	reading := d.newApp()
	reading.bookWindow = true
	reading.win = &mygo.Window{}
	reading.book = &book.Book{}
	d.add(reading)

	if got := d.focused(); got != reading {
		t.Errorf("focused window = %v, want the last open one", got)
	}
	if d.holding("/books/x.epub") != nil {
		t.Error("a file no window holds was reported as held")
	}
	reading.file = "/books/x.epub"
	if got := d.holding("/books/x.epub"); got != reading {
		t.Errorf("holding window = %v, want the reading one", got)
	}
	if home.hasBook() || !reading.hasBook() {
		t.Errorf("hasBook: library %v, reading %v", home.hasBook(), reading.hasBook())
	}

	d.remove(reading)
	if got := d.focused(); got != home {
		t.Errorf("focused window = %v after the reading window closed", got)
	}
}

// A book named before MyGo is up waits for the window instead of being
// dropped, and a pending book is not one a window already holds.
func TestAnEarlyBookWaitsForTheWindow(t *testing.T) {
	d := &desk{}
	d.openPath("/books/x.epub")
	if d.pendingOpen == "" {
		t.Fatal("a book opened before the app was ready was dropped")
	}
	if d.holding(d.pendingOpen) != nil {
		t.Error("a pending book was reported as open in a window")
	}
}

// A book window opens on paper while its file is read, not on the library: the
// reader asked for one book, and the shelf is another window's business.
func TestABookWindowOpensOnPaper(t *testing.T) {
	a := &app{bookWindow: true, opts: reader.DefaultOptions(), instant: true}
	tt := ui.NewTester(a.view, 900, 700)
	if !tt.HasText(locales.OpeningBook()) {
		t.Fatalf("the empty book window does not say it is opening: %q", tt.Texts())
	}
	if tt.HasText(locales.SidebarLibraryHeading()) {
		t.Errorf("a book window showed the library: %q", tt.Texts())
	}
}
