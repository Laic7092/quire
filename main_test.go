package main

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/laix/quire/internal/appstate"
	"github.com/laix/quire/internal/book"
	"github.com/laix/quire/internal/reader"
	"github.com/laix/quire/locales"
	"github.com/laic7092/layx/render"
)

// The view builds headlessly, as MyGo's tester draws frames without a window.
// Without a book the window shows the home screen: the way in, and no page
// counter or reading controls.
func TestViewBuildsWithoutABook(t *testing.T) {
	a := &app{opts: reader.DefaultOptions(), instant: true, status: locales.ReaderEmptyHint()}
	tt := ui.NewTester(a.view, 900, 700)
	if !tt.HasText(locales.SidebarLibraryHeading()) {
		t.Fatalf("home screen not built: %q", tt.Texts())
	}
	if !tt.HasText(locales.MenuOpen()) {
		t.Errorf("home screen has no way to open a book: %q", tt.Texts())
	}
	if tt.HasText("第 1 页") {
		t.Errorf("reading controls shown without a book: %q", tt.Texts())
	}
	// Keys must not panic while nothing is open.
	tt.Key(0, ui.KeyLeft)
	tt.Key(0, ui.KeyRight)
	tt.Key(0, ui.KeyEqual)
	tt.Key(0, ui.KeyHome)
}

// singleOptions is the defaults with one page on the sheet: the page-turning
// tests need the page to move one at a time whatever the window's size, while
// the app itself opens a book as an open book, two pages at a time.
func singleOptions() reader.Options {
	o := reader.DefaultOptions()
	o.Spread = false
	return o
}

// With a laid-out book the tester exercises the real drawing callback and the
// page-turning controls.
func TestViewShowsALaidOutPage(t *testing.T) {
	fontPath, err := reader.FindFont("")
	if err != nil {
		t.Skipf("no system font: %v", err)
	}
	fonts, err := reader.OpenFont(fontPath)
	if err != nil {
		t.Fatal(err)
	}
	bk, err := book.Open("internal/book/testdata/mini.epub")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := bk.Document()
	if err != nil {
		t.Fatal(err)
	}
	layout, err := reader.BuildLayout(context.Background(), doc, fonts, 900, 600, reader.Options{FontSize: reader.DefaultFontSize})
	if err != nil {
		t.Fatal(err)
	}

	a := &app{
		fonts:   fonts,
		paint:   reader.NewUIPainter(nil, fonts),
		book:    bk,
		doc:     doc,
		docAt:   reader.DefaultFontSize,
		layout:  layout,
		opts:    singleOptions(),
		instant: true,
		layW:    900,
		layH:    600,
	}
	tt := ui.NewTester(a.view, 1000, 760)
	if !tt.HasText(locales.PageLabelPages(1, layout.PageCount())) {
		t.Fatalf("page counter missing: %q", tt.Texts())
	}
	if !tt.HasText(a.chapterPagesLeft()) {
		t.Errorf("the folio says nothing of the chapter: %q", tt.Texts())
	}

	if layout.PageCount() > 1 {
		tt.Key(0, ui.KeyRight)
		if !tt.HasText(locales.PageLabelPages(2, layout.PageCount())) {
			t.Errorf("page did not turn: %q", tt.Texts())
		}
		tt.Key(0, ui.KeyLeft)
		if !tt.HasText(locales.PageLabelPages(1, layout.PageCount())) {
			t.Errorf("page did not turn back: %q", tt.Texts())
		}
	}

	// The window will not own a background job in a tester (a.win is nil), but
	// the font keys still update the state without panicking.
	tt.Key(0, ui.KeyEqual)
	if a.opts.FontSize <= reader.DefaultFontSize {
		t.Errorf("font size %g after +", a.opts.FontSize)
	}
	tt.Key(0, ui.KeyMinus)
	if a.opts.FontSize != reader.DefaultFontSize {
		t.Errorf("font size %g after -", a.opts.FontSize)
	}
}

// The cover sits in front of page 0: the first turn puts it away and keeps
// the reading position.
func TestCoverTurnsAwayWithoutMovingThePage(t *testing.T) {
	a := &app{showCover: true, page: 3, opts: reader.DefaultOptions(), instant: true}
	if got := a.pageLabel(); got != locales.PageLabelCover() {
		t.Errorf("page label %q while the cover shows", got)
	}
	if !a.canTurn(1) || !a.canTurn(-1) {
		t.Error("the cover cannot be turned away")
	}
	a.turn(1)
	if a.showCover {
		t.Error("cover still showing")
	}
	if a.page != 3 {
		t.Errorf("page moved to %d while closing the cover", a.page)
	}
	a.turn(1) // no layout yet: must not panic or move the page
	if a.page != 3 {
		t.Errorf("page moved to %d without a layout", a.page)
	}
}

// The side panels are part of the window: the table of contents opens from the
// toolbar and a row jumps the reader to that chapter's page.
func TestSidePanelsBuildAndJump(t *testing.T) {
	fontPath, err := reader.FindFont("")
	if err != nil {
		t.Skipf("no system font: %v", err)
	}
	fonts, err := reader.OpenFont(fontPath)
	if err != nil {
		t.Fatal(err)
	}
	bk, err := book.Open("internal/book/testdata/mini.epub")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := bk.DocumentSized(reader.DefaultFontSize)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := reader.BuildLayout(context.Background(), doc, fonts, 600, 400, reader.Options{FontSize: reader.DefaultFontSize})
	if err != nil {
		t.Fatal(err)
	}

	a := &app{
		fonts:   fonts,
		paint:   reader.NewUIPainter(nil, fonts),
		book:    bk,
		doc:     doc,
		docAt:   reader.DefaultFontSize,
		layout:  layout,
		opts:    singleOptions(),
		instant: true,
		layW:    600,
		layH:    400,
		pan:     appstate.New(nil),
	}
	a.pan.Jump = a.gotoLoc
	a.pan.Refresh(bk, layout, 0)

	tt := ui.NewTester(a.view, 1200, 800)
	if err := tt.Click(locales.PanelContents()); err != nil {
		t.Fatalf("contents button: %v (%q)", err, tt.Texts())
	}
	if a.pan.Sidebar != appstate.SidebarToc {
		t.Fatalf("panel %q after clicking 目录", a.pan.Sidebar)
	}
	// The reader still draws its page, which the floating panel lies over
	// rather than sharing the width with.
	if !tt.HasText(a.chapterLabel()) {
		t.Errorf("chapter label missing: %q", tt.Texts())
	}
	if !tt.HasText(bk.Chapters[0].Title) {
		t.Fatalf("toc row missing: %q", tt.Texts())
	}

	// With one chapter per page the second row is a different page; clicking
	// it is what a reader does to jump.
	if layout.PageCount() > 1 && len(a.pan.Toc.Entries) > 1 {
		want := a.pan.Toc.Entries[1].Page
		before := a.page
		if err := tt.Click(bk.Chapters[1].Title); err != nil {
			t.Fatalf("toc row: %v (%q)", err, tt.Texts())
		}
		if a.page == before {
			t.Errorf("page stayed at %d after a toc jump", before)
		}
		if want >= 0 && a.page != want {
			t.Errorf("jumped to page %d, want %d", a.page, want)
		}
	}
}

// The macOS menu bar carries the reader's commands, and the page-copy item
// starts disabled because nothing is selected.
func TestMenuBarCommands(t *testing.T) {
	a := &app{opts: reader.DefaultOptions(), pan: appstate.New(nil)}
	menu := a.menuBar()

	var files, views *mygo.MenuItem
	for _, it := range menu.Items() {
		switch it.Label {
		case locales.MenuFile():
			files = it
		case locales.MenuView():
			views = it
		}
	}
	if files == nil || views == nil {
		t.Fatalf("menu bar is missing its File or View menu: %v", menu.Items())
	}
	open := findMenuItem(files.Submenu, locales.MenuOpen())
	if open == nil || open.Accelerator != "CmdOrCtrl+O" {
		t.Fatalf("no Open command in the File menu: %+v", files.Submenu)
	}
	for _, label := range []string{locales.MenuSidebar(), locales.MenuContents(), locales.MenuSearch(), locales.MenuDisplaySettings(), locales.MenuScrollReading()} {
		if findMenuItem(views.Submenu, label) == nil {
			t.Errorf("the View menu has no %s", label)
		}
	}
	// The scrolling item follows the setting, however it was changed: the menu
	// toggles itself, and the display settings only set the option.
	if a.menuScroll == nil {
		t.Fatal("the scrolling menu item is not kept")
	}
	a.menuScroll.SetChecked(false)
	a.opts.Scroll = true
	a.syncMenu()
	if !a.menuScroll.IsChecked() {
		t.Error("the scrolling menu item did not follow the setting")
	}
	if a.menuCopy == nil || !a.menuCopy.Disabled {
		t.Fatalf("page copy starts enabled without a selection: %+v", a.menuCopy)
	}
	// The first frame with a selection enables it, and clearing disables it
	// again without rebuilding the menu.
	a.selQuads = []render.SelectionQuad{{}}
	a.syncMenu()
	if a.menuCopy.Disabled {
		t.Error("page copy stayed disabled with a selection")
	}
	a.selQuads = nil
	a.syncMenu()
	if !a.menuCopy.Disabled {
		t.Error("page copy stayed enabled after the selection cleared")
	}
}

// The Books-style shell is the window on every platform, not a macOS-only
// arrangement: the same binary hides the system title bar whether it runs on
// macOS, Windows or Linux, and only the buttons' side differs. The buttons share
// the reader's bar so their glyphs sit on the toolbar's line, which means the
// shell takes the header's height: a 32 DIP bar would ride above it.
func TestTheShellIsTheReaderHeaderEverywhere(t *testing.T) {
	o := windowOptions(func(*ui.Context) {}, "main")
	if o.TitleBarStyle != mygo.TitleBarHidden {
		t.Errorf("TitleBarStyle = %v, want TitleBarHidden on every platform", o.TitleBarStyle)
	}
	if o.TitleBarHeight != headerHeight {
		t.Errorf("TitleBarHeight = %d, want the header's %d", o.TitleBarHeight, headerHeight)
	}
}

func findMenuItem(items []*mygo.MenuItem, label string) *mygo.MenuItem {
	for _, it := range items {
		if it.Label == label {
			return it
		}
	}
	return nil
}

// A page is clamped into the book whenever one is named from outside: a location
// past the end is the last page, and a book with no pages keeps the folio at its
// first page rather than at page -1.
func TestPageClampStaysInsideTheBook(t *testing.T) {
	cases := []struct{ page, n, want int }{
		{0, 0, 0},  // an empty book
		{-3, 0, 0}, // and one named from outside it
		{7, 0, 0},  //
		{-1, 5, 0}, // before the first page
		{4, 5, 4},  // the last page
		{9, 5, 4},  // past the end
		{0, 1, 0},  // a one-page book
	}
	for _, c := range cases {
		if got := clampPage(c.page, c.n); got != c.want {
			t.Errorf("clampPage(%d of %d) = %d, want %d", c.page, c.n, got, c.want)
		}
	}
}

// Closing a book forgets the places that name the book being left: a mode
// switch's pending jump and the pointer's hover on a link belong to its pages, not
// to the library or to the next book opened.
func TestClosingABookForgetsItsPlaces(t *testing.T) {
	a := newWindow(t)
	a.jumpLoc, a.jumpAt = render.Location{Chapter: 3}, true
	a.hoverLink = true
	a.closeBook()
	if a.jumpAt {
		t.Error("the pending jump outlived the book it names")
	}
	if a.hoverLink {
		t.Error("the link hover outlived the page it was on")
	}
}

// A damaged state file is backed up rather than silently replaced, and the
// window says where the old copy went: the message is a toast, not a line the
// home page keeps for the rest of its life.
func TestHomeShowsARecoveredStateNotice(t *testing.T) {
	notice := locales.StateRecovered("/tmp/state.json.bak")
	a := &app{opts: reader.DefaultOptions(), instant: true}
	a.notify(notice)
	tt := ui.NewTester(a.view, 900, 700)
	tt.Frame()
	if !tt.HasText(notice) {
		t.Errorf("the window does not show %q: %q", notice, tt.Texts())
	}
}

// An action's confirmation lives in the folio for a moment and then gives the
// page number back; it does not stay there for the rest of the book.
func TestTransientStatusExpires(t *testing.T) {
	now := time.Unix(1000, 0)
	a := &app{now: func() time.Time { return now }}
	a.say("Bookmark added")
	if got := a.transientStatus(); got != "Bookmark added" {
		t.Errorf("just after the action: %q, want the message", got)
	}
	now = now.Add(3 * time.Second)
	if got := a.transientStatus(); got != "" {
		t.Errorf("after three seconds: %q, want the folio back", got)
	}
	a.failed = true
	a.say("still not shown as a toast")
	if got := a.transientStatus(); got != "" {
		t.Errorf("an error is drawn by the toolbar, not the folio: %q", got)
	}
}
