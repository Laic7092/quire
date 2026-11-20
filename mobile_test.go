package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laix/quire/locales"
)

// A tap in the middle of a phone's page shows and hides the bars; the sides
// still turn the page, and the middle is not a page turn.
func TestPhoneMiddleTapTogglesTheBars(t *testing.T) {
	a := newWindow(t)
	a.phone = true
	tt := ui.NewTester(a.view, 390, 844)
	tt.Frame()
	tt.Frame()

	if !a.chromeShown() {
		t.Fatal("the bars are away before the reader touched the page")
	}
	tt.ClickAt(195, 400)
	if a.chromeShown() {
		t.Error("a tap in the middle left the bars up")
	}
	tt.ClickAt(195, 400)
	if !a.chromeShown() {
		t.Error("a second tap in the middle did not bring the bars back")
	}

	start := a.page
	tt.ClickAt(40, 400)
	if a.page != start-1 {
		t.Errorf("a tap at the left went to page %d, want %d", a.page, start-1)
	}
	tt.ClickAt(350, 400)
	if a.page != start {
		t.Errorf("a tap at the right went to page %d, want %d", a.page, start)
	}
}

// A finger on a scrolling column drags it, rather than marking text: the
// column follows the finger and keeps its speed for a moment.
func TestPhoneFingerDragsTheScrollingColumn(t *testing.T) {
	a, tt := newScrollWindow(t)
	a.phone = true
	before := a.scrollY
	tt.FingerDown(500, 500)
	tt.FingerMove(500, 420)
	tt.FingerMove(500, 340)
	tt.FingerUp(500, 340)
	if a.scrollY <= before {
		t.Errorf("the column stayed at %.1f after a finger moved 160 DIPs down", a.scrollY)
	}
	if len(a.selQuads) > 0 {
		t.Error("dragging the column left a selection behind")
	}
}

// The phone's library is the tabbed one: the destinations at the foot choose
// the filter, and the Mac source list is not built beside it.
func TestPhoneLibraryUsesTheTabBar(t *testing.T) {
	a := newWindow(t)
	a.phone = true
	a.book = nil
	a.doc = nil
	a.layout = nil
	a.bookWindow = false
	tt := ui.NewTester(a.view, 390, 844)
	tt.Frame()
	tt.Frame()

	if a.libTab != mobileTabHome || a.libFilter != 0 {
		t.Fatalf("the library opens on tab %d, filter %d", a.libTab, a.libFilter)
	}
	if err := tt.Click(locales.SidebarAll()); err != nil {
		t.Fatalf("the All destination is not in the bar: %v", err)
	}
	if a.libTab != mobileTabAll || a.libFilter != 3 {
		t.Errorf("the All destination chose tab %d, filter %d", a.libTab, a.libFilter)
	}
	// The shops the Mac source list holds are a screen of their own, and its
	// rows are built without a Mac sidebar behind them.
	if err := tt.Click(locales.SidebarSources()); err != nil {
		t.Fatalf("the Sources destination is not in the bar: %v", err)
	}
	if a.libTab != mobileTabSources {
		t.Errorf("the Sources destination chose tab %d", a.libTab)
	}
	tt.Frame()
	if !tt.HasText(locales.MenuOpen()) {
		t.Error("the Sources screen does not offer the way to open a book")
	}
}

// The phone's grid falls from three covers to two when the screen cannot give
// three a finger's width, and the covers keep their shape either way.
func TestPhoneMetricsFitTheCovers(t *testing.T) {
	m := phoneMetricsFor(390)
	if m.cols != 3 {
		t.Errorf("a 390 DIP phone puts %d covers in a row, want 3", m.cols)
	}
	if want := (float32(390) - 2*m.padding - 2*m.coverGap) / 3; m.coverW != want {
		t.Errorf("a cover is %.1f wide, want %.1f", m.coverW, want)
	}
	if m.coverH != m.coverW*coverAspect {
		t.Errorf("the cover's height %.1f does not keep its %.2f aspect", m.coverH, coverAspect)
	}
	if narrow := phoneMetricsFor(320); narrow.cols != 2 {
		t.Errorf("a 320 DIP phone puts %d covers in a row, want 2", narrow.cols)
	}
}
