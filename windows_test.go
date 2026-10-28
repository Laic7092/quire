package main

import (
	"testing"

	"github.com/egoist/mygo"
	"github.com/laix/layx/render"
	"github.com/laix/quire/internal/book"
	"github.com/laix/quire/internal/reader"
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

// Every window gets its own frame in the app's state, so moving one does not
// move the others on the next launch.
func TestEachWindowRemembersItsOwnFrame(t *testing.T) {
	d := &desk{}
	first, second, third := d.nextStateKey(), d.nextStateKey(), d.nextStateKey()
	if first != "main" {
		t.Errorf("the first window's state key is %q, want %q", first, "main")
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

// The desk knows which window is a library, which one has a book, and which one
// already holds a file: that is how a second book gets a window of its own and
// how the same file asked for twice gets one.
func TestTheDeskFindsTheRightWindow(t *testing.T) {
	d := &desk{}
	home := d.newApp()
	home.win = &mygo.Window{}
	d.add(home)
	reading := d.newApp()
	reading.win = &mygo.Window{}
	reading.book = &book.Book{}
	d.add(reading)

	if got := d.homeWindow(); got != home {
		t.Errorf("home window = %v, want the one without a book", got)
	}
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
		t.Errorf("hasBook: home %v, reading %v", home.hasBook(), reading.hasBook())
	}

	d.remove(reading)
	if got := d.focused(); got != home {
		t.Errorf("focused window = %v after the reading window closed", got)
	}
}

// A book named before MyGo is up waits for the first window instead of being
// dropped, and the desk tells a book in a window from an empty one when the
// system hands it another file later.
func TestAnEarlyBookWaitsForTheFirstWindow(t *testing.T) {
	d := &desk{}
	a := d.newApp()
	d.openPath(a, "/books/x.epub")
	if d.pendingOpen == "" {
		t.Fatal("a book opened before the app was ready was dropped")
	}
	if d.holding(d.pendingOpen) != nil {
		t.Error("a pending book was reported as open in a window")
	}
}
