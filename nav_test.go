package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laic7092/layx/render"
	"github.com/laix/quire/locales"
)

// clickFolioNav clicks one of the folio's chevrons and builds the frame its
// click asks for.
func clickFolioNav(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	r, ok := tt.Find(label)
	if !ok {
		t.Fatalf("the folio has no %q: %q", label, tt.Texts())
	}
	tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
}

// jumpToAnotherPage jumps through the table of contents to a page the reader is
// not on and returns that page.
func jumpToAnotherPage(t *testing.T, a *app) int {
	t.Helper()
	if a.pan == nil {
		t.Fatal("no panels to jump from")
	}
	for _, e := range a.pan.Toc.Entries {
		if !e.HasLoc {
			continue
		}
		if page, ok := a.layout.PageOf(e.Loc); ok && page != a.page {
			a.gotoLoc(e.Loc)
			return a.page
		}
	}
	t.Fatal("the book has no chapter on another page")
	return 0
}

// Following a jump — a table of contents row here, a link or a footnote the
// same way — leaves the place it was made from on the folio's trail: the back
// chevron returns to it and the forward chevron goes on again. A new jump drops
// what the reader had stepped back from, as a browser does.
func TestTheFolioStepsThroughJumps(t *testing.T) {
	a := newWindow(t)
	tt := ui.NewTester(a.view, 1180, 800)
	for range 4 {
		tt.Frame()
	}
	if _, ok := tt.Find(locales.NavBack()); ok {
		t.Fatalf("a book just opened shows a back chevron: %q", tt.Texts())
	}
	if _, ok := tt.Find(locales.NavForward()); ok {
		t.Fatalf("a book just opened shows a forward chevron: %q", tt.Texts())
	}

	// Nothing has been jumped to, so there is nothing to step back to either.
	startPage := a.page
	if _, ok := tt.Find(locales.NavBack()); ok {
		t.Fatalf("a chevron appeared with no jumps: %q", tt.Texts())
	}

	jumped := jumpToAnotherPage(t, a)
	tt.Frame()
	if !a.nav.canBack() {
		t.Error("the jump left no way back")
	}
	if a.nav.canForward() {
		t.Error("a jump forward filled the forward trail")
	}
	if _, ok := tt.Find(locales.NavBack()); !ok {
		t.Fatalf("the jump left no back chevron: %q", tt.Texts())
	}
	if _, ok := tt.Find(locales.NavForward()); ok {
		t.Fatalf("a forward chevron appeared with nowhere to go: %q", tt.Texts())
	}

	clickFolioNav(t, tt, locales.NavBack())
	if a.page != startPage {
		t.Errorf("back landed on page %d, want %d", a.page, startPage)
	}
	if !a.nav.canForward() {
		t.Error("stepping back left no way forward")
	}
	if _, ok := tt.Find(locales.NavForward()); !ok {
		t.Fatalf("stepping back left no forward chevron: %q", tt.Texts())
	}
	if _, ok := tt.Find(locales.NavBack()); ok {
		t.Fatalf("the first stop still shows a back chevron: %q", tt.Texts())
	}

	clickFolioNav(t, tt, locales.NavForward())
	if a.page != jumped {
		t.Errorf("forward landed on page %d, want %d", a.page, jumped)
	}
	if _, ok := tt.Find(locales.NavForward()); ok {
		t.Fatalf("the last stop still shows a forward chevron: %q", tt.Texts())
	}

	// A jump from the page the reader stepped forward to drops the stops that
	// lay ahead of it.
	a.gotoPage(0)
	tt.Frame()
	next := jumpToAnotherPage(t, a)
	tt.Frame()
	if a.nav.canForward() {
		t.Errorf("the forward trail survived a new jump to page %d", next)
	}
	if !a.nav.canBack() {
		t.Error("the new jump left no way back")
	}
	if _, ok := tt.Find(locales.NavForward()); ok {
		t.Errorf("a new jump still offers a forward chevron: %q", tt.Texts())
	}
}

// The trail is kept in document locations, so it survives the switch between
// pages and a scrolling column: a jump made in one mode is taken back to the
// same place in the other.
func TestTheJumpsTrailWorksWhileScrolling(t *testing.T) {
	a, tt := newScrollWindow(t)
	if a.scrollY != 0 {
		t.Fatalf("the column starts at %.0f, want the top", a.scrollY)
	}
	if _, ok := tt.Find(locales.NavBack()); ok {
		t.Fatalf("a scrolling book just opened shows a back chevron: %q", tt.Texts())
	}
	before := a.currentChapter()

	var target render.Location
	for _, e := range a.pan.Toc.Entries {
		if !e.HasLoc {
			continue
		}
		if y, ok := a.layout.OffsetOf(e.Loc); ok && y != a.scrollY {
			target = e.Loc
			break
		}
	}
	if target == (render.Location{}) {
		t.Fatal("the table of contents names no place below the top")
	}
	a.gotoLoc(target)
	tt.Frame()
	if a.scrollY == 0 {
		t.Fatal("the jump did not move the column")
	}
	if !a.nav.canBack() {
		t.Fatal("the jump left no way back")
	}

	r, ok := tt.Find(locales.NavBack())
	if !ok {
		t.Fatalf("the folio has no back chevron: %q", tt.Texts())
	}
	tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	if a.scrollY != 0 {
		t.Errorf("back left the column at %.0f, want the top", a.scrollY)
	}
	if a.currentChapter() != before {
		t.Errorf("back left the column in chapter %d, want %d", a.currentChapter(), before)
	}
}
