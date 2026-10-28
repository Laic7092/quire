package main

import (
	"context"
	"image"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laix/quire/internal/appstate"
	"github.com/laix/quire/internal/book"
	"github.com/laix/quire/internal/library"
	"github.com/laix/quire/internal/reader"
	"github.com/laix/quire/internal/search"
	"github.com/laix/quire/internal/store"
	"github.com/laix/quire/locales"
	"github.com/laix/layx/render/paint"
)

// newWindow builds an app with a real book, a real layout and a real shelf,
// ready for the tester: the tests below only differ in what they ask it to
// show.
func newWindow(t *testing.T) *app {
	t.Helper()
	fontPath, err := reader.FindFont("")
	if err != nil {
		t.Skipf("no system font: %v", err)
	}
	fonts, err := reader.OpenFont(fontPath)
	if err != nil {
		t.Fatal(err)
	}
	bk, err := book.Open("internal/book/testdata/samples/aq-zhuan.epub")
	if err != nil {
		t.Fatal(err)
	}
	doc, images, err := bk.Build(reader.DefaultFontSize)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.TempDir() + "/state.json")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	reader.SetPalette(reader.Quiet)
	t.Cleanup(func() { reader.SetPalette(reader.Quiet) })

	lib := &library.Library{}
	lib.Entries, err = library.Scan("internal/book/testdata/samples")
	if err != nil {
		t.Fatal(err)
	}

	paint := reader.NewUIPainter(nil, fonts)
	for id, data := range images {
		paint.SetImage(id, data)
	}
	pan := appstate.New(st)
	pan.Library = lib
	pan.Shelf.Lib = lib
	pan.Opened(bk, "internal/book/testdata/samples/aq-zhuan.epub")

	a := &app{
		fonts:   fonts,
		paint:   paint,
		book:    bk,
		doc:     doc,
		docAt:   reader.DefaultFontSize,
		opts:    reader.DefaultOptions(),
		theme:   reader.ThemeQuiet,
		pan:     pan,
		st:      st,
		instant: true,
	}
	a.syncSettingsFields()
	pan.Jump = a.gotoLoc
	pan.Open = func(string) {}
	pan.Cover = a.coverBitmap
	a.covers = map[coverKey]*ui.Bitmap{}

	// The paper the draw callback gets: the window minus the bars and the
	// padding, capped at the page's measure.
	w := float32(pageMaxWidth)
	h := float32(800 - headerHeight - footerHeight - 2*pagePadding)
	layout, err := reader.BuildLayout(context.Background(), doc, fonts, w, h, reader.Options{FontSize: reader.DefaultFontSize})
	if err != nil {
		t.Fatal(err)
	}
	a.layout, a.layW, a.layH = layout, w, h
	a.page = 4
	pan.Refresh(bk, layout, a.page)
	pan.Reading = a.currentLoc
	pan.OnNotes = a.refreshMarks
	return a
}

// The whole window renders in every state its chrome can be in: the page, each
// floating panel, the cover, the home screen, and the night paper. Rendered
// frames are written to PNG when SNAP=1, which is how the layout was checked by
// eye.
func TestWindowRendersEveryScreen(t *testing.T) {
	a := newWindow(t)
	bk, doc := a.book, a.doc
	paperW := float32(pageMaxWidth)
	paperH := float32(800 - headerHeight - footerHeight - 2*pagePadding)
	pan := a.pan

	shots := []struct {
		name string
		dark bool
		// want is the text the screen must show, read after render: a page
		// label or a message of the catalogs, in the language the app shows.
		want   func(a *app) string
		w, h   int // 0 is 1180×800
		render func(t *testing.T)
	}{
		// One page on the sheet, as the reader shows a book in a window too
		// narrow for the pair.
		{name: "reader", want: func(a *app) string { return a.pageLabel() }, render: func(t *testing.T) {
			a.opts.Spread = false
			one, err := reader.BuildLayout(context.Background(), doc, a.fonts, paperW, paperH,
				singleOptions())
			if err != nil {
				t.Fatal(err)
			}
			a.layout, a.page = one, 4
			a.clearSelection()
			pan.Refresh(bk, one, 4)
		}},
		{name: "toc", want: func(*app) string { return bk.Chapters[0].Title }, render: func(*testing.T) {
			pan.Show(appstate.SidebarToc)
		}},
		{name: "bookmarks", want: func(*app) string { return locales.EmptyBookmarks() }, render: func(*testing.T) {
			pan.Show(appstate.SidebarBookmarks)
		}},
		// One bookmark, added at the page being read: the ribbon in the bar is
		// lit, and the panel lists the row it made.
		{name: "bookmark", want: func(*app) string { return locales.BookmarkCount(1) }, render: func(t *testing.T) {
			pan.Reading = a.currentLoc
			if !pan.BookmarkHere() {
				t.Fatal("BookmarkHere added no bookmark")
			}
			pan.Show(appstate.SidebarBookmarks)
		}},
		{name: "notes", want: func(*app) string { return locales.EmptyNotes() }, render: func(*testing.T) {
			pan.Show(appstate.SidebarNotes)
		}},
		{name: "search", want: func(*app) string { return locales.SearchFieldLabel() }, render: func(t *testing.T) {
			pan.Show(appstate.SidebarSearch)
			ix, err := search.NewIndex(bk, a.doc)
			if err != nil {
				t.Fatal(err)
			}
			pan.SetIndex(ix)
			pan.Search.Query = "阿"
			pan.Search.OnSearch("阿")
			if len(pan.Search.Hits) == 0 {
				t.Fatal("the sample book matched nothing")
			}
		}},
		// The veils over the page: a search hit in amber, the hit the panel
		// points at darker, and a drag selection over the lines below it.
		{name: "highlight", want: func(*app) string { return locales.SelectionSelected() }, render: func(t *testing.T) {
			if len(pan.Search.Hits) == 0 {
				t.Fatal("the search shot left no hits")
			}
			page, ok := search.Page(a.layout, pan.Index, pan.Search.Hits[0])
			if !ok {
				t.Fatal("the first hit has no page")
			}
			a.page, a.showCover = page, false
			pan.Search.Selected = 0
			pan.Refresh(bk, a.layout, page)
			a.refreshMarks()
			if p := a.layout.Page(page); p != nil {
				from, ok1 := p.HitTest(paint.Point{X: reader.Margin + 2, Y: reader.Margin + 150})
				to, ok2 := p.HitTest(paint.Point{X: 420, Y: reader.Margin + 210})
				if ok1 && ok2 {
					a.sel.Start(from)
					a.sel.Extend(to)
					a.selQuads = a.sel.Quads(a.layout)
				}
			}
		}},
		{name: "cover", want: func(*app) string { return locales.PageLabelCover() }, render: func(*testing.T) {
			pan.Show(appstate.SidebarNone)
			a.showCover = true
		}}, {name: "home", want: func(*app) string { return locales.LibraryCount(3) }, render: func(t *testing.T) {
			a.showCover = false
			a.book, a.status = nil, ""
			for i, e := range pan.Library.Entries {
				key := store.Key(e.Path, e.Size, e.Modified)
				a.st.TouchBook(store.Book{Key: key, Path: e.Path, Title: e.Title,
					Format: e.Format, Size: e.Size, Modified: e.Modified,
					LastOpened: time.Now().Add(-time.Duration(i) * time.Hour)})
				// Two books part read and one finished, so the shot shows both
				// bands and a year's progress in the goals ring.
				progress := []float32{0.42, 0.18, 1}[i]
				a.st.SetPosition(key, store.Position{
					Progress: progress,
					Updated:  time.Now().Add(-time.Duration(i) * 24 * time.Hour),
				})
			}
		}},
		// The home page at the window size the macOS prototype is drawn at:
		// the shot the layout is measured against.
		{name: "home-prototype", w: 818, h: 612, want: func(*app) string { return locales.LibraryCount(3) }, render: func(*testing.T) {}},
		// The same page at a wide window and near the minimum: the measures follow
		// the width, so the covers and margins grow or shrink instead of staying
		// at the reference window's numbers.
		{name: "home-wide", w: 1440, h: 900, want: func(*app) string { return locales.LibraryCount(3) }, render: func(*testing.T) {}},
		{name: "home-narrow", w: 700, h: 520, want: func(*app) string { return locales.ContinueReading() }, render: func(*testing.T) {}},
		// The display settings: the four papers, the size, the leading, the
		// face, the alignment, the margins and the spread.
		{name: "settings", want: func(*app) string { return locales.SettingsTwoPages() }, render: func(t *testing.T) {
			a.book, a.page = bk, 4
			a.showCover = false
			a.aaOpen, a.aaMore = true, true
			a.syncSettingsFields()
			pan.Show(appstate.SidebarNone)
		}},
		// An open book: two pages side by side once the window is wide enough.
		{name: "spread", w: 1280, want: func(a *app) string { return a.pageLabel() }, render: func(t *testing.T) {
			a.aaOpen = false
			a.opts.Spread = true
			sw := float32((1280 - 2*pagePadding) / 2)
			sh := float32(800 - headerHeight - footerHeight - 2*pagePadding)
			two, err := reader.BuildLayout(context.Background(), doc, a.fonts, sw, sh,
				reader.Options{FontSize: reader.DefaultFontSize, Spread: true})
			if err != nil {
				t.Fatal(err)
			}
			a.book, a.layout, a.page = bk, two, 4
			a.showCover = false
			a.clearSelection()
			pan.Refresh(bk, two, 4)
			pan.Show(appstate.SidebarNone)
		}},
		// A window near the minimum size, with a panel floating over it, still
		// lays the page out at the whole window's measure and says where it is.
		{name: "narrow", w: 700, h: 520, want: func(a *app) string { return a.pageLabel() }, render: func(t *testing.T) {
			w := float32(700 - 2*pagePadding)
			ph := float32(520 - headerHeight - footerHeight - 2*pagePadding)
			narrow, err := reader.BuildLayout(context.Background(), doc, a.fonts, w, ph, reader.Options{FontSize: reader.DefaultFontSize})
			if err != nil {
				t.Fatal(err)
			}
			a.book, a.layout, a.page = bk, narrow, 4
			a.showCover = false
			a.clearSelection()
			pan.Show(appstate.SidebarToc)
			pan.Refresh(bk, narrow, 4)
		}},
		{name: "dark", dark: true, want: func(a *app) string { return a.pageLabel() }, render: func(t *testing.T) {
			reader.SetPalette(reader.Night)
			a.theme, a.themeSys = reader.ThemeNight, false
			// Lay the book out again on the night paper at this window's size:
			// a shot must show the layout the reader would really build.
			a.opts.Spread = false
			w := float32(1180 - 2*pagePadding)
			h := float32(800 - headerHeight - footerHeight - 2*pagePadding)
			one, err := reader.BuildLayout(context.Background(), doc, a.fonts, w, h,
				reader.Options{FontSize: reader.DefaultFontSize})
			if err != nil {
				t.Fatal(err)
			}
			a.layout, a.page = one, 4
			a.clearSelection()
			pan.Refresh(bk, one, 4)
			t.Cleanup(func() {
				reader.SetPalette(reader.Sepia)
				a.theme, a.themeSys = reader.ThemeSepia, false
			})
			night, err := reader.BuildLayout(context.Background(), doc, a.fonts, paperW, paperH, reader.Options{FontSize: reader.DefaultFontSize})
			if err != nil {
				t.Fatal(err)
			}
			a.book, a.layout, a.page = bk, night, 4
			a.clearSelection()
			pan.Refresh(bk, night, a.page)
			pan.Show(appstate.SidebarNone)
		}},
		// The scrolling column: the same book as one flow, its first viewport
		// of tiles on the sheet and the folio counting in percent.
		{name: "scroll", want: func(*app) string { return locales.ScrollPercent(0) }, render: func(t *testing.T) {
			reader.SetPalette(reader.Quiet)
			a.theme, a.themeSys = reader.ThemeQuiet, false
			a.opts.Scroll, a.opts.Spread = true, false
			col, err := reader.BuildLayout(context.Background(), doc, a.fonts, paperW, paperH,
				reader.Options{FontSize: reader.DefaultFontSize, Scroll: true})
			if err != nil {
				t.Fatal(err)
			}
			a.book, a.layout, a.page = bk, col, 0
			a.layScroll, a.tileH, a.scrollY = true, paperH, 0
			a.aaOpen, a.showCover = false, false
			a.clearSelection()
			if loc, ok := col.LocationAt(0); ok {
				pan.RefreshAt(bk, col, loc)
			}
			pan.Show(appstate.SidebarNone)
		}},
		// A note at the reading place: the marked passage is veiled on the page
		// and the panel lists it with the editor for its comment. It is the
		// last shot, so its rectangles cannot outlive their layout.
		{name: "note", want: func(*app) string { return locales.NoteCount(1) }, render: func(t *testing.T) {
			a.opts.Scroll, a.opts.Spread = false, false
			one, err := reader.BuildLayout(context.Background(), doc, a.fonts, paperW, paperH,
				singleOptions())
			if err != nil {
				t.Fatal(err)
			}
			a.book, a.layout, a.page = bk, one, 4
			a.layScroll, a.showCover = false, false
			a.clearSelection()
			pan.Refresh(bk, one, a.page)
			a.refreshMarks()
			pg := one.Page(a.page)
			if pg == nil {
				t.Fatal("no page to take a passage from")
			}
			from, ok := pg.HitTest(paint.Point{X: 90, Y: 70})
			if !ok {
				t.Fatal("no location at the first lines")
			}
			to, ok := pg.HitTest(paint.Point{X: 420, Y: 140})
			if !ok {
				t.Fatal("no location a few lines down")
			}
			if _, ok := pan.AddNote(from, to, "阿Q便全疤通红的发起怒来，估量了对手，口讷的他便骂，气力小的他便打。"); !ok {
				t.Fatal("AddNote stored nothing")
			}
			pan.Show(appstate.SidebarNotes)
		}},
	}

	for _, s := range shots {
		w, h := s.w, s.h
		if w == 0 || h == 0 {
			w, h = 1180, 800
		}
		tt := ui.NewTester(a.view, w, h)
		if s.dark {
			tt.SetDark(true)
		}
		s.render(t)
		// Two frames: the page area's measure is a frame behind the layout it is
		// drawn from, and a shot that changes how wide the sheet is (a spread, a
		// narrower window) is only drawn at its own measure on the second. An
		// ask for a frame that only paints is not what settles the view, so the
		// snapshot would otherwise show the frame that measured it.
		tt.Frame()
		tt.Frame()
		if want := s.want(a); !tt.HasText(want) {
			t.Errorf("%s: %q is missing from %q", s.name, want, tt.Texts())
		}
		if os.Getenv("SNAP") != "" {
			f, err := os.Create("/tmp/snap-" + s.name + ".png")
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, tt.Image()); err != nil {
				t.Fatal(err)
			}
			f.Close()
		}
	}
}

// A window squeezed narrower than the page's margins says so instead of asking
// for a layout there is no room for.
func TestNarrowWindowExplainsItself(t *testing.T) {
	a := &app{opts: reader.DefaultOptions(), layout: nil}
	a.ensureLayout(40, 400)
	if !a.failed || a.status == "" {
		t.Errorf("no message for a 40 DIP page: failed=%v status=%q", a.failed, a.status)
	}
	if a.layout != nil {
		t.Error("a layout was published for a page with no room")
	}
}

// A drag across the sheet selects text, paints its rectangles on the page, and
// the platform's copy shortcut puts the text on the clipboard.
func TestDragSelectsTextAndCopiesIt(t *testing.T) {
	a := newWindow(t)
	tt := ui.NewTester(a.view, 1180, 800)
	tt.Frame()

	box, ok := tt.Find(locales.PageLabel())
	if !ok {
		t.Fatalf("the page box is not labelled: %q", tt.Texts())
	}
	// From the first lines down a few of them, inside the page's margins.
	tt.Press(box.X+90, box.Y+80)
	tt.Move(box.X+100, box.Y+124)
	tt.Move(box.X+150, box.Y+160)
	tt.Release(box.X+150, box.Y+160)
	tt.Frame()

	if len(a.selQuads) == 0 {
		t.Fatalf("the drag selected nothing on page %d", a.page+1)
	}
	for _, q := range a.selQuads {
		if q.Page != a.page {
			t.Errorf("quad on page %d, reading page %d", q.Page, a.page)
		}
	}
	want := strings.TrimSpace(a.sel.Text(a.doc))
	if want == "" {
		t.Fatal("the selection reads no text")
	}

	tt.Key(ui.Cmd, ui.KeyC)
	tt.Frame()
	if got := tt.Clipboard(); got != want {
		t.Errorf("clipboard = %q, want %q", got, want)
	}
	if a.selCopied == 0 {
		t.Error("the copy was not reported to the footer")
	}

	// A click is a zero-length selection: it clears what the drag selected,
	// and, as every click on the sheet does, turns the page.
	tt.ClickAt(box.X+90, box.Y+80)
	tt.Frame()
	if len(a.selQuads) != 0 {
		t.Errorf("the selection survived a click: %d quads", len(a.selQuads))
	}

	// So does Escape, which the page does not otherwise use.
	tt.Press(box.X+90, box.Y+80)
	tt.Move(box.X+140, box.Y+150)
	tt.Release(box.X+140, box.Y+150)
	tt.Frame()
	if len(a.selQuads) == 0 {
		t.Fatal("the second drag selected nothing")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if len(a.selQuads) != 0 {
		t.Errorf("Escape left %d quads", len(a.selQuads))
	}

	// The page holds the keyboard focus after a press, and the window's keys
	// still reach it: a focused sheet must not swallow page turns.
	before := a.page
	tt.Key(0, ui.KeyRight)
	tt.Frame()
	if a.page == before {
		t.Errorf("page stayed at %d after →", before)
	}
}

// Search hits are painted on the page they fall on, and the hit the panel
// points at is painted in its own colour.
func TestSearchHitsAreHighlighted(t *testing.T) {
	a := newWindow(t)
	ix, err := search.NewIndex(a.book, a.doc)
	if err != nil {
		t.Fatal(err)
	}
	a.pan.SetIndex(ix)
	a.pan.Search.Query = "阿"
	a.pan.Search.OnSearch("阿")
	if len(a.pan.Search.Hits) == 0 {
		t.Fatal("the sample book matched nothing")
	}

	// Put the reader on a page that has a hit.
	page, ok := search.Page(a.layout, ix, a.pan.Search.Hits[0])
	if !ok {
		t.Fatal("the first hit has no page")
	}
	a.page = page
	a.pan.Refresh(a.book, a.layout, page)
	a.refreshMarks()

	if len(a.hitQuads) != len(a.pan.Search.Hits) {
		t.Fatalf("%d quads for %d hits", len(a.hitQuads), len(a.pan.Search.Hits))
	}
	var onPage int
	for _, q := range a.hitQuads[0] {
		if q.Page == page {
			onPage++
		}
	}
	if onPage == 0 {
		t.Fatalf("the first hit painted nothing on page %d", page+1)
	}

	tt := ui.NewTester(a.view, 1180, 800)
	tt.Frame()
	if !tt.HasText(a.pageLabel()) {
		t.Errorf("page label missing: %q", tt.Texts())
	}
}

// The page's context menu is where a selection is acted on: copy it, search it,
// select the page, clear it. It opens on the secondary button and, as the page
// takes the focus, on the menu key too.
func TestPageContextMenuActsOnTheSelection(t *testing.T) {
	a := newWindow(t)
	ix, err := search.NewIndex(a.book, a.doc)
	if err != nil {
		t.Fatal(err)
	}
	a.pan.SetIndex(ix)

	tt := ui.NewTester(a.view, 1180, 800)
	tt.Frame()

	box, ok := tt.Find(locales.PageLabel())
	if !ok {
		t.Fatalf("the page box is not labelled: %q", tt.Texts())
	}

	// Nothing selected: the menu is there, but copying is not.
	tt.RightClickAt(box.X+120, box.Y+120)
	menu := strings.Join(tt.Menu(), "|")
	if !strings.Contains(menu, locales.MenuCopy()) || !strings.Contains(menu, locales.MenuSelectPage()) {
		t.Fatalf("the page menu has no selection commands: %q", menu)
	}
	if err := tt.ChooseMenuItem(locales.MenuCopy()); err == nil {
		t.Error("the menu copied with nothing selected")
	}

	// A drag, then copy from the menu.
	tt.Press(box.X+90, box.Y+80)
	tt.Move(box.X+180, box.Y+96)
	tt.Release(box.X+180, box.Y+96)
	tt.Frame()
	want := a.selectedText()
	if want == "" {
		t.Fatal("the drag selected nothing")
	}
	tt.RightClickAt(box.X+120, box.Y+120)
	tt.Frame()
	if err := tt.ChooseMenuItem(locales.MenuCopy()); err != nil {
		t.Fatalf("copy from the menu: %v (menu %q)", err, strings.Join(tt.Menu(), "|"))
	}
	if got := tt.Clipboard(); got != want {
		t.Errorf("clipboard = %q, want %q", got, want)
	}

	// 选中本页 covers the page, 清除选区 drops it.
	tt.RightClickAt(box.X+120, box.Y+120)
	tt.Frame()
	if err := tt.ChooseMenuItem(locales.MenuSelectPage()); err != nil {
		t.Fatalf("select page: %v (menu %q)", err, strings.Join(tt.Menu(), "|"))
	}
	if len(a.selQuads) == 0 {
		t.Fatal("选中本页 selected no rectangles")
	}
	if strings.TrimSpace(a.sel.Text(a.doc)) == "" {
		t.Error("选中本页 reads no text")
	}
	tt.RightClickAt(box.X+120, box.Y+120)
	tt.Frame()
	if err := tt.ChooseMenuItem(locales.MenuClearSelection()); err != nil {
		t.Fatalf("clear selection: %v (menu %q)", err, strings.Join(tt.Menu(), "|"))
	}
	if len(a.selQuads) != 0 {
		t.Errorf("清除选区 left %d quads", len(a.selQuads))
	}

	// Searching the selection fills the search panel with it.
	tt.Press(box.X+90, box.Y+80)
	tt.Move(box.X+180, box.Y+96)
	tt.Release(box.X+180, box.Y+96)
	tt.Frame()
	text := a.selectedText()
	label := searchLabel(text)
	if label == "" {
		t.Fatal("no search item for a selection")
	}
	tt.Key(ui.Shift, ui.KeyF10) // the page took the focus with the press
	tt.Frame()
	if menu := strings.Join(tt.Menu(), "|"); !strings.Contains(menu, label) {
		t.Fatalf("no %q in %q", label, menu)
	}
	if err := tt.ChooseMenuItem(label); err != nil {
		t.Fatalf("search the selection: %v", err)
	}
	tt.Frame()
	if a.pan.Sidebar != appstate.SidebarSearch {
		t.Errorf("sidebar %q after searching the selection", a.pan.Sidebar)
	}
	if got, want := a.pan.Search.Query, searchQuery(text); got != want {
		t.Errorf("query = %q, want %q", got, want)
	}
	if len(a.pan.Search.Hits) == 0 {
		t.Errorf("searching the selected %q found nothing", a.pan.Search.Query)
	}
}

// The display settings panel changes the page: the paper, the face, the
// alignment and the spread, each saved as it is chosen.
func TestDisplaySettingsApplyAndPersist(t *testing.T) {
	a := newWindow(t)
	tt := ui.NewTester(a.view, 1180, 800)
	tt.Frame()

	if err := tt.Click("Aa"); err != nil {
		t.Fatalf("no display settings button: %v (%q)", err, tt.Texts())
	}
	// The papers and the size are on the panel itself; the rest is one press
	// away, under 自定义, as Books arranges it.
	for _, want := range []string{locales.ThemesSettings(), locales.ThemeOriginal(), locales.ThemeCalm(), locales.ThemePaper(), locales.ThemeBold(), locales.SettingsCustomize()} {
		if !tt.HasText(want) {
			t.Fatalf("the panel has no %q: %q", want, tt.Texts())
		}
	}
	if err := tt.Click(locales.SettingsCustomize()); err != nil {
		t.Fatalf("no 自定义 row: %v", err)
	}
	tt.Frame()
	for _, want := range []string{locales.SettingsSize(), locales.SettingsLeading(), locales.SettingsTypeface(), locales.SettingsAlignment(), locales.SettingsMargins(), locales.SettingsTwoPages()} {
		if !tt.HasText(want) {
			t.Fatalf("the panel has no %q: %q", want, tt.Texts())
		}
	}

	// The paper: the swatch is the page's own pair of colours.
	if err := tt.Click(locales.ThemeOriginal()); err != nil {
		t.Fatalf("no white theme: %v", err)
	}
	if got := reader.Current().Theme; got != reader.ThemeWhite {
		t.Errorf("paper is %q after choosing 白色", got)
	}
	if got := a.st.State().Prefs.Theme; got != string(reader.ThemeWhite) {
		t.Errorf("saved theme %q, want white", got)
	}

	if err := tt.Click(locales.SettingsSans()); err != nil {
		t.Fatalf("no sans choice: %v", err)
	}
	if a.opts.Font != reader.FontSans {
		t.Errorf("face %q after choosing 黑体", a.opts.Font)
	}
	if err := tt.Click(locales.SettingsLeft()); err != nil {
		t.Fatalf("no left alignment: %v", err)
	}
	if a.opts.Justify {
		t.Error("still justified after choosing Left")
	}

	// A settings change lands in the store, so the next launch looks the same.
	prefs := a.st.State().Prefs
	if prefs.Font != reader.FontSans || prefs.Justify {
		t.Errorf("saved prefs %+v", prefs)
	}
	reader.SetPalette(reader.Sepia)
	// The app opens a book as an open book, so the switch starts on and the
	// press takes the second page away.
	if !a.opts.Spread {
		t.Fatal("a book does not open as a spread by default")
	}
	if err := tt.Click(locales.SettingsTwoPages()); err != nil {
		t.Fatalf("no spread switch: %v", err)
	}
	if a.opts.Spread {
		t.Errorf("spread still on after choosing 双页显示")
	}
}

// A book is laid out at the sheet the page area has, never at a guess: the
// width decides whether the sheet holds one page or two, so a layout started
// before the area is measured lays the whole book out at a width the reader
// never sees, and the next frame lays it out again.
func TestTheSheetWaitsForThePageArea(t *testing.T) {
	a := newWindow(t)
	a.opts.Spread = true
	a.areaW = 0
	if _, _, measured := a.layoutSheet(960, 594); measured {
		t.Fatal("a sheet was chosen before the page area was measured")
	}

	a.areaW = 1180
	sw, sh, measured := a.layoutSheet(960, 594)
	if !measured {
		t.Fatal("the measured page area gave no sheet")
	}
	if want := (a.areaW - 2*pagePadding) / 2; sw != want {
		t.Errorf("sheet %.0f wide, want the spread's half %.0f", sw, want)
	}
	if sh != 594 {
		t.Errorf("sheet %.0f high, want the box's 594", sh)
	}

	// And the reading view measures the area itself: after the frame that has
	// committed geometry, the width is the window's, which is what the sheet is
	// sized from on the very next build.
	a.areaW = 0
	tt := ui.NewTester(a.view, 1180, 800)
	tt.Frame()
	tt.Frame()
	if got := a.areaW; got != 1180 {
		t.Errorf("page area %.0f wide, want the window's 1180", got)
	}
}

// "+" is Shift and the equal key on most layouts, and it is the same command as
// "=": both make the type larger, and "-" makes it smaller. A size already at
// the end of its range stays there.
func TestFontKeysTakeThePlusKey(t *testing.T) {
	a := newWindow(t)
	tt := ui.NewTester(a.view, 1180, 800)
	tt.Frame()
	base := a.opts.FontSize

	tt.Key(ui.Shift, ui.KeyEqual)
	tt.Frame()
	if a.opts.FontSize != base+1 {
		t.Errorf("Shift+= left the size at %g, want %g", a.opts.FontSize, base+1)
	}
	tt.Key(0, ui.KeyMinus)
	tt.Frame()
	if a.opts.FontSize != base {
		t.Errorf("minus left the size at %g, want %g", a.opts.FontSize, base)
	}
	tt.Key(0, ui.KeyEqual)
	tt.Frame()
	if a.opts.FontSize != base+1 {
		t.Errorf("= left the size at %g, want %g", a.opts.FontSize, base+1)
	}

	a.opts.FontSize = reader.MaxFontSize
	a.syncSettingsFields()
	tt.Key(ui.Shift, ui.KeyEqual)
	tt.Frame()
	if a.opts.FontSize != reader.MaxFontSize {
		t.Errorf("the size went to %g past its maximum %d", a.opts.FontSize, reader.MaxFontSize)
	}
}

// The library's own search opens on ⌘F, and the reader's floating panels are not
// opened from the library, where there is no page for them to lie over.
func TestLibrarySearchOpensOnCmdF(t *testing.T) {
	a := &app{opts: reader.DefaultOptions(), instant: true, pan: appstate.New(nil)}

	tt := ui.NewTester(a.view, 900, 700)
	tt.Frame()
	if a.libSearch {
		t.Fatal("the library opened with its search showing")
	}

	// The reader's panels are the book's: ⌘T and ⌘F from the library must not
	// leave one waiting behind the shelf, to open over the next book.
	tt.Key(ui.Cmd, ui.KeyT)
	tt.Frame()
	if a.pan.Showing() {
		t.Fatalf("⌘T opened the reader's %q panel over the library", a.pan.Sidebar)
	}

	tt.Key(ui.Cmd, ui.KeyF)
	tt.Frame()
	if !a.libSearch {
		t.Fatalf("⌘F did not open the library's search: %q", tt.Texts())
	}
	if a.pan.Showing() {
		t.Errorf("⌘F opened the reader's %q panel over the library", a.pan.Sidebar)
	}

	// The field it opens takes a query.
	if err := tt.Click(locales.LibrarySearchLabel()); err != nil {
		t.Fatalf("the search field: %v (%q)", err, tt.Texts())
	}
	tt.Type("aq")
	tt.Frame()
	if a.libQuery != "aq" {
		t.Errorf("the query is %q after typing, want %q", a.libQuery, "aq")
	}
}

// An open book: two pages side by side on a wide window, and a turn that moves
// by the pair, as Books pages an open book.
func TestSpreadShowsTwoPagesAndTurnsByThePair(t *testing.T) {
	a := newWindow(t)
	a.opts.Spread = true
	a.areaW = 1180 - 2*pagePadding
	sw := a.areaW / 2
	sh := float32(800 - headerHeight - footerHeight - 2*pagePadding)
	two, err := reader.BuildLayout(context.Background(), a.doc, a.fonts, sw, sh,
		reader.Options{FontSize: reader.DefaultFontSize, Spread: true})
	if err != nil {
		t.Fatal(err)
	}
	a.layout, a.page = two, 4
	a.pan.Refresh(a.book, two, 4)

	tt := ui.NewTester(a.view, 1180, 800)
	tt.Frame()
	if got := a.pagesOnSheet(); got != 2 {
		t.Fatalf("%d pages on a 1180 wide sheet", got)
	}
	if got, want := a.pageLabel(), locales.PageLabelPages(5, two.PageCount()); got != want {
		t.Errorf("page label %q, want %q", got, want)
	}

	// A turn moves the pair, not the page.
	tt.Key(0, ui.KeyRight)
	tt.Frame()
	if a.page != 6 {
		t.Errorf("page %d after turning a spread from 4", a.page)
	}
	tt.Key(0, ui.KeyLeft)
	tt.Frame()
	if a.page != 4 {
		t.Errorf("page %d after turning back", a.page)
	}

	// The right half of the open book is the second page.
	right, ok := a.pageLocation(a.boxW*0.8, reader.Margin+40)
	left, ok2 := a.pageLocation(reader.Margin+8, reader.Margin+40)
	if !ok || !ok2 {
		t.Fatalf("the sheet has no text: %v %v", ok, ok2)
	}
	if right == left {
		t.Errorf("both halves of the open book hit %v", left)
	}
	if p, ok := a.layout.PageOf(right); !ok || p != a.page+1 {
		t.Errorf("the right half hits page %d, want %d", p, a.page+1)
	}
}

// A book whose pages do not pair up ends on a single page: End turns to it, and a
// forward turn there stays put instead of stepping back to the pair before it.
func TestLastPageOfAnOddBookTurnsNoFurther(t *testing.T) {
	a := newWindow(t)
	a.opts.Spread = true
	sw := float32(340) // a half sheet whose page count is odd
	sh := float32(500)
	two, err := reader.BuildLayout(context.Background(), a.doc, a.fonts, sw, sh,
		reader.Options{FontSize: reader.DefaultFontSize, Spread: true})
	if err != nil {
		t.Fatal(err)
	}
	if two.PageCount()%2 == 0 || two.PageCount() < 3 {
		t.Skipf("the sample book lays out in %d pages", two.PageCount())
	}
	a.areaW = 2 * sw
	a.layout = two
	a.pan.Refresh(a.book, two, 0)

	tt := ui.NewTester(a.view, 1180, 800)
	tt.Frame()
	last := two.PageCount() - 1
	tt.Key(0, ui.KeyEnd)
	tt.Frame()
	if a.page != last {
		t.Fatalf("End left the reader on page %d, want the last one %d", a.page, last)
	}

	// Turning forward at the end is not a turn: the page stays, and above all it
	// does not slide back to the pair before the last page.
	tt.Key(0, ui.KeyRight)
	tt.Frame()
	if a.page != last {
		t.Errorf("a forward turn at the last page %d moved to %d", last, a.page)
	}
	if a.canTurn(1) {
		t.Errorf("the last page %d still reports a page to turn to", last)
	}

	// And a turn back from it goes to the pair before.
	tt.Key(0, ui.KeyLeft)
	tt.Frame()
	if want := last - 2; a.page != want {
		t.Errorf("a turn back from the last page %d went to %d, want %d", last, a.page, want)
	}
}

// The page slides: the turn reports its progress from the clock, the sheet draws
// both spreads while it is under way, and it lands once.
func TestTurnSlidesAndLands(t *testing.T) {
	a := newWindow(t)
	a.instant = false
	tt := ui.NewTester(a.view, 1180, 800)
	tt.Frame()

	at := time.Now()
	a.now = func() time.Time { return at }
	turn := func(age time.Duration) *turnState {
		return &turnState{from: 4, to: 5, dir: 1, at: at.Add(-age)}
	}
	a.turning = turn(turnDuration / 2)
	if progress, moving := a.turnProgress(); !moving || progress < 0.45 || progress > 0.55 {
		t.Errorf("half way turn progress %.2f, moving %v", progress, moving)
	}
	// The frame draws the leaving and the arriving spread, and never lays
	// anything out: drawing is a replay.
	a.turning = turn(turnDuration / 4)
	tt.Frame()

	a.turning = turn(turnDuration + time.Millisecond)
	if _, moving := a.turnProgress(); moving {
		t.Error("the turn never lands")
	}
	tt.Frame()
	if a.turning != nil {
		t.Error("the landed turn still asks for frames")
	}

	if got := easeOut(0); got != 0 {
		t.Errorf("easeOut(0) = %g", got)
	}
	if got := easeOut(1); got != 1 {
		t.Errorf("easeOut(1) = %g", got)
	}
	if got := easeOut(0.5); got < 0.8 || got > 1 {
		t.Errorf("easeOut(0.5) = %g, want most of the way", got)
	}
}

// Holding the turn key flicks through the book. A repeat that arrives while a
// slide is under way waits for it to land instead of restarting it, and the
// slide that follows takes the short pace a held key asks for.
func TestHeldTurnWaitsForTheSlideThenFlicks(t *testing.T) {
	a := newWindow(t)
	a.instant = false
	a.opts.Spread = false // one page a turn, so the page number says where it went
	tt := ui.NewTester(a.view, 1180, 800)
	tt.Frame()

	at := time.Now()
	a.now = func() time.Time { return at }

	// A press on its own slides at the reading pace.
	a.turn(1)
	first := a.turning
	if first == nil {
		t.Fatal("the first press put no turn on the sheet")
	}
	if got := first.duration(); got != turnDuration {
		t.Errorf("the first slide lasts %s, want %s", got, turnDuration)
	}
	if a.page != 5 {
		t.Fatalf("page %d after the first press, want 5", a.page)
	}

	// A repeat while it is under way must not restart what the reader is
	// watching: the page it is going to stays, and the slide keeps its clock.
	at = at.Add(turnDuration / 3)
	a.turn(1)
	if a.turning != first {
		t.Error("the repeat restarted the slide in flight")
	}
	if a.page != 5 {
		t.Errorf("page %d while the slide is under way, want 5", a.page)
	}

	// The waiting ask is served when that slide lands, and as the short slide.
	at = at.Add(turnDuration)
	tt.Frame()
	if a.turning == nil {
		t.Fatal("the held ask never became a turn")
	}
	if got := a.turning.duration(); got != turnFlickDuration {
		t.Errorf("the held slide lasts %s, want %s", got, turnFlickDuration)
	}
	if got := a.turning.from; got != 5 {
		t.Errorf("the held slide leaves page %d, want 5", got)
	}
	if a.page != 6 {
		t.Errorf("page %d after the held slide started, want 6", a.page)
	}

	// And it lands by itself a slide later: a held key is a queue of one turn,
	// not one turn restarted every repeat.
	at = at.Add(turnFlickDuration)
	tt.Frame()
	if a.turning != nil {
		t.Error("the short slide never landed")
	}
	if a.page != 6 {
		t.Errorf("page %d after the short slide landed, want 6", a.page)
	}
}

// A fade turn paints both spreads in place: the one leaving fading out, the one
// arriving fading in, so half way through each shows the other through it. It
// moves nothing and lays nothing out.
func TestTurnFadesInPlace(t *testing.T) {
	a := newWindow(t)
	a.instant = false
	warm := ui.NewTester(a.view, 1180, 800)
	warm.Frame()

	at := time.Now()
	a.now = func() time.Time { return at }
	a.setTurn(reader.TurnFade)
	if a.opts.Turn != reader.TurnFade {
		t.Fatalf("turn = %q, want the fade", a.opts.Turn)
	}
	if !a.optsAt.IsZero() {
		t.Error("choosing the turn asked for a new layout")
	}
	if got := a.st.State().Prefs.Turn; got != string(reader.TurnFade) {
		t.Errorf("saved turn = %q, want it kept for the next launch", got)
	}

	from := a.snapPage(4)
	to := from + a.pageStep()
	shot := func(page int, turn *turnState) *image.RGBA {
		a.page, a.turning = page, turn
		tt := ui.NewTester(a.view, 1180, 800)
		tt.Frame()
		return tt.Image()
	}
	leaving := shot(from, nil)
	arriving := shot(to, nil)
	// Half way through the turn is where the two printings are on the sheet
	// together: one fade has given way to the next.
	mid := shot(to, &turnState{from: from, to: to, dir: 1,
		at: at.Add(-turnDuration / 2)})

	paper := reader.Current().Paper
	ink := func(img *image.RGBA, x, y int) int {
		r, g, b, _ := img.At(x, y).RGBA()
		dr, dg, db := int(r>>8)-int(paper.R), int(g>>8)-int(paper.G), int(b>>8)-int(paper.B)
		return max(dr, -dr) + max(dg, -dg) + max(db, -db)
	}
	// How much of a settled page's ink is still where it was. A cross-fade
	// keeps both spreads in place, so most of each one's marks are there, only
	// fainter; a slide would leave the one leaving's places empty.
	kept := func(settled *image.RGBA) int {
		var n int
		for y := 46; y < 756; y += 2 {
			for x := 21; x < 1159; x += 2 {
				if ink(settled, x, y) > 40 && ink(mid, x, y) > 20 {
					n++
				}
			}
		}
		return n
	}
	if got := kept(leaving); got < 1000 {
		t.Errorf("only %d of the marks of the page leaving are still in place half way through", got)
	}
	if got := kept(arriving); got < 1000 {
		t.Errorf("only %d of the marks of the page arriving are in place half way through", got)
	}
}

// A fade fades out and back in: the printing leaving is gone before the turn
// ends, the one arriving is not on the sheet before a fifth of it, and the two
// are never both faint enough to leave the sheet looking blank.
func TestFadeRampsOverlapWithoutBlanking(t *testing.T) {
	if leaving, arriving := fadeAlpha(0); leaving != 1 || arriving != 0 {
		t.Errorf("fadeAlpha(0) = (%v, %v), want the leaving printing whole", leaving, arriving)
	}
	if leaving, arriving := fadeAlpha(1); leaving != 0 || arriving != 1 {
		t.Errorf("fadeAlpha(1) = (%v, %v), want the arriving printing whole", leaving, arriving)
	}
	prevLeaving, prevArriving := fadeAlpha(0)
	for i := 1; i <= 100; i++ {
		p := float32(i) / 100
		leaving, arriving := fadeAlpha(p)
		if leaving < 0 || leaving > 1 || arriving < 0 || arriving > 1 {
			t.Fatalf("fadeAlpha(%v) = (%v, %v), want both in 0..1", p, leaving, arriving)
		}
		if leaving > prevLeaving || arriving < prevArriving {
			t.Errorf("fadeAlpha(%v) = (%v, %v) does not carry on from (%v, %v)",
				p, leaving, arriving, prevLeaving, prevArriving)
		}
		if leaving+arriving < 0.25 {
			t.Errorf("fadeAlpha(%v) = (%v, %v): the sheet would look blank", p, leaving, arriving)
		}
		prevLeaving, prevArriving = leaving, arriving
	}
}

// The display settings offer the turn with the other choices, and picking one
// there is what the reader gets on the next turn.
func TestSettingsOfferTheTurn(t *testing.T) {
	a := newWindow(t)
	a.page = 4
	a.showCover = false
	a.aaOpen, a.aaMore = true, true
	a.syncSettingsFields()
	tt := ui.NewTester(a.view, 1180, 800)
	tt.Frame()
	for _, want := range []string{
		locales.SettingsTurn(), locales.SettingsTurnSlide(), locales.SettingsTurnFade(),
	} {
		if !tt.HasText(want) {
			t.Errorf("the display settings do not say %q: %q", want, tt.Texts())
		}
	}
	if err := tt.Click(locales.SettingsTurnFade()); err != nil {
		t.Fatalf("the fade is not pressable: %v", err)
	}
	tt.Frame()
	if a.opts.Turn != reader.TurnFade {
		t.Errorf("turn = %q after picking the fade", a.opts.Turn)
	}
	if !a.optsAt.IsZero() {
		t.Error("picking the turn asked for a new layout")
	}
	if got := a.st.State().Prefs.Turn; got != string(reader.TurnFade) {
		t.Errorf("saved turn = %q", got)
	}
	if err := tt.Click(locales.SettingsTurnSlide()); err != nil {
		t.Fatalf("the slide is not pressable: %v", err)
	}
	tt.Frame()
	if a.opts.Turn != reader.TurnSlide {
		t.Errorf("turn = %q after picking the slide", a.opts.Turn)
	}
}

// The slide turns one sheet of paper: no edge and no shade travel with the
// pages, so the paper beside the text is the paper all the way across.
func TestSlidePaintsOneSheet(t *testing.T) {
	a := newWindow(t)
	a.instant = false
	tt := ui.NewTester(a.view, 1180, 800)
	tt.Frame()

	at := time.Now()
	a.now = func() time.Time { return at }
	a.setTurn(reader.TurnSlide)
	from := a.snapPage(4)
	a.page = from + a.pageStep()
	a.turning = &turnState{from: from, to: a.page, dir: 1,
		at: at.Add(-turnDuration * 15 / 100)}
	tt.Frame()

	paper := reader.Current().Paper
	img := tt.Image()
	// The page's foot, well below its text: the band the edge shade used to
	// darken on a turn this early in the slide.
	for _, p := range [][2]int{{300, 740}, {420, 750}} {
		r, g, b, _ := img.At(p[0], p[1]).RGBA()
		if uint8(r>>8) != paper.R || uint8(g>>8) != paper.G || uint8(b>>8) != paper.B {
			t.Errorf("(%d,%d) is #%02x%02x%02x, not the paper #%02x%02x%02x",
				p[0], p[1], r>>8, g>>8, b>>8, paper.R, paper.G, paper.B)
		}
	}
}

// Escape closes what is open and, with nothing left, goes back to the library,
// as Books does.
func TestEscapeGoesBackToTheLibrary(t *testing.T) {
	a := newWindow(t)
	tt := ui.NewTester(a.view, 1180, 800)
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.book != nil {
		t.Errorf("the book is still open after Escape: %q", tt.Texts())
	}
	if !tt.HasText(locales.SidebarLibraryHeading()) {
		t.Errorf("no library after Escape: %q", tt.Texts())
	}
}

// A window with a hidden title bar hands the view the room the buttons take: the
// reader's bar starts to the right of the traffic lights and the library's source
// list below them. Windows and some Linux desktops put the buttons on the right
// instead, so the settings button and the reading ring keep out of their way
// there too. With no title bar the view reserves nothing and keeps its own margin.
func TestWindowControlsLeaveRoomInTheView(t *testing.T) {
	// The library is another screen, with no book open.
	newLibrary := func(t *testing.T, w, h int) *ui.Tester {
		t.Helper()
		lib := &app{opts: reader.DefaultOptions(), instant: true}
		return ui.NewTester(lib.view, w, h)
	}

	t.Run("left", func(t *testing.T) {
		bar := ui.TitleBar{Height: 28, Left: 78}
		a := newWindow(t)
		tt := ui.NewTester(a.view, 1180, 800)
		tt.SetTitleBar(bar)
		btn, ok := tt.Find(locales.PanelContents())
		if !ok {
			t.Fatalf("the reader's bar is missing: %q", tt.Texts())
		}
		if btn.X < bar.Left {
			t.Errorf("the reader's bar starts at x=%g, under the traffic lights (left %g)", btn.X, bar.Left)
		}

		lt := newLibrary(t, 900, 700)
		lt.SetTitleBar(bar)
		row, ok := lt.Find(locales.Search())
		if !ok {
			t.Fatalf("the library's source list is missing: %q", lt.Texts())
		}
		if row.Y < bar.Height {
			t.Errorf("the source list starts at y=%g, under the traffic lights (height %g)", row.Y, bar.Height)
		}
	})

	t.Run("right", func(t *testing.T) {
		// The buttons fill the header's height, as windowOptions asks, and the
		// toolbar's controls centre on that same line.
		bar := ui.TitleBar{Height: headerHeight, Right: 138}
		a := newWindow(t)
		rt := ui.NewTester(a.view, 1180, 800)
		rt.SetTitleBar(bar)
		aa, ok := rt.Find("Aa")
		if !ok {
			t.Fatalf("the reader's settings button is missing: %q", rt.Texts())
		}
		if aa.X+aa.W > 1180-bar.Right {
			t.Errorf("the reader's bar ends at x=%g, under the caption buttons (right %g)", aa.X+aa.W, bar.Right)
		}
		if mid, want := aa.Y+aa.H/2, float32(headerHeight)/2; mid != want {
			t.Errorf("the reader's settings sit at y=%g, off the caption buttons' line y=%g", mid, want)
		}

		rlt := newLibrary(t, 900, 700)
		rlt.SetTitleBar(bar)
		ring, ok := rlt.Find(locales.WeekRing())
		if !ok {
			t.Fatalf("the library's reading ring is missing: %q", rlt.Texts())
		}
		if ring.X+ring.W > 900-bar.Right {
			t.Errorf("the library's reading ring ends at x=%g, under the caption buttons (right %g)", ring.X+ring.W, bar.Right)
		}
	})

	t.Run("none", func(t *testing.T) {
		a := newWindow(t)
		tt := ui.NewTester(a.view, 1180, 800)
		got, ok := tt.Find(locales.PanelContents())
		if !ok {
			t.Fatalf("the reader's bar is missing: %q", tt.Texts())
		}
		if got.X <= 0 || got.X >= 78 {
			t.Errorf("the reader's bar starts at x=%g with no title bar: the view reserves room for buttons it was not given", got.X)
		}
	})
}

// The reading bar's first cluster is the three panels, as Books has them:
// contents, bookmarks and notes. A click opens the panel over the page and a
// second click on the same button puts it away.
func TestReadingBarOpensThePanels(t *testing.T) {
	a := newWindow(t)
	tt := ui.NewTester(a.view, 1180, 800)
	for _, label := range []string{locales.PanelContents(), locales.PanelBookmarks(), locales.PanelNotes()} {
		if _, ok := tt.Find(label); !ok {
			t.Fatalf("no %s button in the reading bar: %q", label, tt.Texts())
		}
	}

	if err := tt.Click(locales.PanelBookmarks()); err != nil {
		t.Fatalf("bookmarks button: %v (%q)", err, tt.Texts())
	}
	if a.pan.Sidebar != appstate.SidebarBookmarks {
		t.Fatalf("panel %q after clicking 书签", a.pan.Sidebar)
	}
	if !tt.HasText(locales.EmptyBookmarks()) {
		t.Errorf("the bookmarks panel is not drawn: %q", tt.Texts())
	}
	if err := tt.Click(locales.PanelBookmarks()); err != nil {
		t.Fatalf("bookmarks button again: %v (%q)", err, tt.Texts())
	}
	if a.pan.Showing() {
		t.Errorf("panel %q after a second click on 书签", a.pan.Sidebar)
	}

	if err := tt.Click(locales.PanelNotes()); err != nil {
		t.Fatalf("notes button: %v (%q)", err, tt.Texts())
	}
	if a.pan.Sidebar != appstate.SidebarNotes || !tt.HasText(locales.EmptyNotes()) {
		t.Errorf("panel %q, texts %q after clicking 笔记", a.pan.Sidebar, tt.Texts())
	}
}

// A panel floats over the page rather than narrowing it: the sheet keeps its
// geometry while one is open, and the panel is drawn in the page's left margin.
func TestPanelsFloatOverThePage(t *testing.T) {
	a := newWindow(t)
	tt := ui.NewTester(a.view, 1180, 800)
	a.pan.Show(appstate.SidebarNone)
	// The sheet takes a few frames to settle on its final measure, as the
	// area's width is only known after the first one.
	for range 4 {
		tt.Frame()
	}
	before, ok := tt.Find(locales.PageLabel())
	if !ok {
		t.Fatalf("no page to measure: %q", tt.Texts())
	}
	if err := tt.Click(locales.PanelContents()); err != nil {
		t.Fatalf("contents button: %v (%q)", err, tt.Texts())
	}
	after, ok := tt.Find(locales.PageLabel())
	if !ok {
		t.Fatalf("the page is gone while the panel is open: %q", tt.Texts())
	}
	if after != before {
		t.Errorf("the page moved when the panel opened: %+v -> %+v", before, after)
	}
	// The panel's own header says how many rows it lists; it is the one text
	// that is only in the card, not in the window's bar.
	count, ok := tt.Find(locales.TocCount(len(a.pan.Toc.Entries)))
	if !ok {
		t.Fatalf("the contents did not build: %q", tt.Texts())
	}
	if count.X < panelMargin || count.X > before.X+panelWidth {
		t.Errorf("the contents card starts at x=%g, not left of the page at x=%g", count.X, before.X)
	}
}

// The page's left edge, and only the left, carries the way back to the library.
// The chevron shows while the pointer rests over the margin, and it floats, so
// nothing about the page moves as it appears.
func TestTheLeftEdgeGoesBackToTheLibrary(t *testing.T) {
	a := newWindow(t)
	tt := ui.NewTester(a.view, 1180, 800)
	for range 4 {
		tt.Frame()
	}
	if _, ok := tt.Find(locales.BackToLibrary()); !ok {
		t.Fatalf("no way back at the page's left edge: %q", tt.Texts())
	}
	if _, ok := tt.Find("Next Page"); ok {
		t.Errorf("the right edge still turns pages: %q", tt.Texts())
	}
	if _, ok := tt.Find("Previous Page"); ok {
		t.Errorf("the left edge still turns pages: %q", tt.Texts())
	}

	before, ok := tt.Find(locales.PageLabel())
	if !ok {
		t.Fatalf("no page to measure: %q", tt.Texts())
	}
	tt.Move(8, 400)
	tt.Frame()
	after, ok := tt.Find(locales.PageLabel())
	if !ok || after != before {
		t.Errorf("the page moved when the chevron showed: %+v -> %+v", before, after)
	}

	tt.ClickAt(8, 400)
	if a.book != nil {
		t.Errorf("the book is still open after the left edge click")
	}
	if !tt.HasText(locales.SidebarHome()) {
		t.Errorf("no library after the left edge click: %q", tt.Texts())
	}
}

// A click on the sheet turns it, and a press that moves selects text instead:
// the gesture is decided on release, so neither one has to claim a strip of the
// text. The left half goes back and the right half on.
func TestClickTurnsAndDragSelects(t *testing.T) {
	a := newWindow(t)
	tt := ui.NewTester(a.view, 1180, 800)
	for range 4 {
		tt.Frame()
	}
	body, ok := tt.Find(locales.PageLabel())
	if !ok || a.layout.PageCount() < 3 {
		t.Fatalf("no page to turn: %q", tt.Texts())
	}

	before := a.page
	tt.ClickAt(body.X+body.W*3/4, body.Y+body.H/2)
	if a.page <= before {
		t.Errorf("a click on the right half left the page at %d, from %d", a.page, before)
	}

	on := a.page
	tt.ClickAt(body.X+body.W/4, body.Y+body.H/2)
	if a.page >= on {
		t.Errorf("a click on the left half left the page at %d, from %d", a.page, on)
	}

	// A drag is a selection, not a turn, wherever it starts.
	on = a.page
	tt.Press(body.X+body.W*3/4, body.Y+80)
	tt.Move(body.X+body.W*3/4, body.Y+160)
	tt.Release(body.X+body.W*3/4, body.Y+160)
	if a.page != on {
		t.Errorf("a drag turned the page from %d to %d", on, a.page)
	}
	if len(a.selQuads) == 0 {
		t.Error("the drag selected nothing")
	}

	// A hand may wobble during a click without meaning to select.
	on = a.page
	tt.Press(body.X+body.W*3/4, body.Y+220)
	tt.Move(body.X+body.W*3/4+2, body.Y+220+2)
	tt.Release(body.X+body.W*3/4+2, body.Y+220+2)
	if a.page == on {
		t.Errorf("a click with a 2 DIP wobble did not turn the page at %d", on)
	}
}

// The cover is the book's own front page: a click on it opens the book at the
// place the reader left, and does not turn a page or start a selection.
// A book the reader is done with leaves the shelf through the control that
// shows on its cover, and through the cover's own menu; both ask first, and
// cancelling keeps the book where it was. The file stays on disk.
func TestRemovingABookFromTheShelfAsksFirst(t *testing.T) {
	a := newWindow(t)
	a.book, a.layout, a.status = nil, nil, ""
	a.showCover = false
	a.pan.Show(appstate.SidebarNone)
	gone := a.pan.Library.Entries[0]

	tt := ui.NewTester(a.view, 1180, 800)
	tt.Frame()
	if !tt.HasText(locales.AllBooks()) {
		t.Fatalf("the shelf did not build: %q", tt.Texts())
	}
	// The control is not on a cover until the pointer rests on it: a click on a
	// cover opens the book.
	if _, ok := tt.Find(locales.ShelfRemove()); ok {
		t.Fatalf("the remove control shows without the pointer: %q", tt.Texts())
	}
	cover, ok := tt.Find(gone.Title)
	if !ok {
		t.Fatalf("no cover for %q: %q", gone.Title, tt.Texts())
	}
	tt.Move(cover.X+cover.W/2, cover.Y+cover.H/2)
	tt.Frame()
	control, ok := tt.Find(locales.ShelfRemove())
	if !ok {
		t.Fatalf("the pointer on the cover shows no remove control: %q", tt.Texts())
	}

	// The click asks; it does not remove the book.
	tt.ClickAt(control.X+control.W/2, control.Y+control.H/2)
	if !tt.HasText(locales.ShelfRemoveTitle(gone.Title)) {
		t.Fatalf("the click asked nothing: %q", tt.Texts())
	}
	if a.book != nil {
		t.Fatal("the remove control opened the book instead")
	}
	if err := tt.Click(locales.Cancel()); err != nil {
		t.Fatalf("cancel: %v (texts %q)", err, tt.Texts())
	}
	tt.Frame()
	if tt.HasText(locales.ShelfRemoveHint()) {
		t.Fatalf("the question stayed after Cancel: %q", tt.Texts())
	}
	if got := len(a.pan.Library.Entries); got != 3 {
		t.Fatalf("cancelling left %d books on the shelf, want 3", got)
	}
	if _, ok := tt.Find(gone.Title); !ok {
		t.Fatalf("the cancelled book is off the shelf: %q", tt.Texts())
	}

	// The cover's menu takes the same way in, and answering it removes the book:
	// the row goes and the store forgets what it kept for the file.
	if err := tt.RightClick(gone.Title); err != nil {
		t.Fatalf("secondary click: %v", err)
	}
	if err := tt.ChooseMenuItem(locales.ShelfRemove()); err != nil {
		t.Fatalf("choose the removal: %v (menu %q)", err, tt.Menu())
	}
	if !tt.HasText(locales.ShelfRemoveTitle(gone.Title)) {
		t.Fatalf("the menu item asked nothing: %q", tt.Texts())
	}
	if err := tt.Click(locales.ShelfRemoveConfirm()); err != nil {
		t.Fatalf("remove: %v (texts %q)", err, tt.Texts())
	}
	tt.Frame()
	if got := len(a.pan.Library.Entries); got != 2 {
		t.Fatalf("the shelf lists %d books after removing one, want 2", got)
	}
	for _, b := range a.st.Recent(0) {
		if b.Path == gone.Path {
			t.Error("the store still remembers the removed book")
		}
	}
	if _, err := os.Stat(gone.Path); err != nil {
		t.Errorf("removing a book deleted its file: %v", err)
	}
	if !tt.HasText(locales.ShelfRemoved(gone.Title)) {
		t.Errorf("the home page does not say the book is off the shelf: %q", tt.Texts())
	}
	// The row stays gone in the frames after it, and the rest of the shelf does
	// not change under it.
	for range 2 {
		tt.Frame()
		if _, ok := tt.Find(gone.Title); ok {
			t.Fatalf("the removed cover came back: %q", tt.Texts())
		}
		if !tt.HasText(locales.LibraryCount(2)) {
			t.Errorf("the shelf does not count the 2 books left: %q", tt.Texts())
		}
	}
}

func TestClickOnTheCoverOpensTheBook(t *testing.T) {
	a := newWindow(t)
	a.showCover = true
	tt := ui.NewTester(a.view, 1180, 800)
	tt.Frame()
	if !tt.HasText(locales.PageLabelCover()) {
		t.Fatalf("the cover is not on the sheet: %q", tt.Texts())
	}
	sheet, ok := tt.Find(locales.PageLabel())
	if !ok {
		t.Fatalf("no sheet to click: %q", tt.Texts())
	}
	page := a.page
	tt.ClickAt(sheet.X+sheet.W/2, sheet.Y+sheet.H/2)
	if a.showCover {
		t.Error("the cover stayed after a click on it")
	}
	if a.page != page {
		t.Errorf("the click moved the page from %d to %d", page, a.page)
	}
	if len(a.selQuads) > 0 {
		t.Error("the click on the cover selected text")
	}
	if want := locales.PageLabelPages(page+1, a.layout.PageCount()); !tt.HasText(want) {
		t.Errorf("the folio does not show %q after the cover: %q", want, tt.Texts())
	}
}

// The sheet turns into a single page exactly where two pages would each be
// narrower than the narrowest readable page: the count of pages on it and their
// width come from the same sum, so a spread never hands the layout a page below
// reader.SpreadMinWidth and the setting holds at the boundary.
func TestTheSheetSplitsWhereAPageStopsFitting(t *testing.T) {
	a := newWindow(t)
	a.opts.Spread = true
	narrowest := float32(2*reader.SpreadMinWidth + 2*pagePadding)
	cases := []struct {
		area  float32
		pages int
	}{
		{narrowest - 1, 1},
		{narrowest, 2},
		{narrowest + 40, 2},
	}
	for _, c := range cases {
		a.areaW = c.area
		if got := a.pagesOnSheet(); got != c.pages {
			t.Errorf("a page area of %.0f DIPs holds %d pages, want %d", c.area, got, c.pages)
		}
		sw, _ := a.sheetSize(0, 500, a.opts)
		if c.pages == 2 {
			if sw < reader.SpreadMinWidth {
				t.Errorf("a %.0f DIP page area gives two pages of %.1f, under the %d a page may be",
					c.area, sw, reader.SpreadMinWidth)
			}
			continue
		}
		if want := c.area - 2*pagePadding; sw != want {
			t.Errorf("a single sheet in a %.0f DIP area is %.1f wide, want %.1f", c.area, sw, want)
		}
	}
}

// The bar's controls keep their room whatever the book is called: a name too
// long for the window is cut short of the clusters at either end, and shows
// whole on its tooltip, while one that fits is drawn entire and centred — in
// the window while that is where it belongs, between the controls when the
// window is too narrow for both.
func TestHeaderKeepsTheNameClearOfTheControls(t *testing.T) {
	long := strings.Repeat("很长的书名", 12)
	for _, tc := range []struct {
		name   string
		width  int
		bar    ui.TitleBar
		failed bool
	}{
		{"a window with room to spare", 1200, ui.TitleBar{Height: headerHeight}, false},
		{"a mac's traffic lights", 640, ui.TitleBar{Height: headerHeight, Left: 78, Right: 8}, false},
		{"window buttons on both sides", 640, ui.TitleBar{Height: headerHeight, Left: 100, Right: 100}, false},
		{"window buttons at the right", 640, ui.TitleBar{Height: headerHeight, Right: 100}, false},
		{"a message beside the controls", 640, ui.TitleBar{Height: headerHeight, Right: 100}, true},
	} {
		a := &app{opts: reader.DefaultOptions(), instant: true}
		a.book = &book.Book{Title: long}
		a.failed, a.status = tc.failed, "这一页放不下"
		var leftEnd, rightStart float32
		var place titlePlace
		tt := ui.NewTester(func(c *ui.Context) {
			a.header(c, c.Theme())
			left, right := a.headLeft.Bounds(c), a.headRight.Bounds(c)
			leftEnd, rightStart, place = left.X+left.W, right.X, a.headPlace
		}, tc.width, 60)
		tt.SetTitleBar(tc.bar)
		tt.Frame()

		name, ok := tt.Find(long)
		if !ok {
			t.Fatalf("%s: the bar shows no name: %q", tc.name, tt.Texts())
		}
		if name.X < leftEnd || name.X+name.W > rightStart {
			t.Errorf("%s: the name spans %v to %v, over controls from %v to %v",
				tc.name, name.X, name.X+name.W, leftEnd, rightStart)
		}
		if place.w > titleMaxWidth {
			t.Errorf("%s: the name was given %v, over the %v cap", tc.name, place.w, titleMaxWidth)
		}
		// The long name takes all the room it is given: the band between the
		// controls is what limits it, not the cap.
		want := min(float32(titleMaxWidth), rightStart-leftEnd-titleGap)
		if name.W != want || place.w != want {
			t.Errorf("%s: the long name is %v wide in %v of room, want %v",
				tc.name, name.W, place.w, want)
		}

		// The same bar shows a short name whole, and centres it where it
		// belongs: in the window while that keeps it clear of the controls,
		// between them otherwise.
		a.book.Title = "小书"
		tt.Frame()
		short, ok := tt.Find("小书")
		if !ok {
			t.Fatalf("%s: the short name is missing: %q", tc.name, tt.Texts())
		}
		if short.W >= place.w {
			t.Errorf("%s: the short name is %v wide, as wide as the %v it was given",
				tc.name, short.W, place.w)
		}
		centre := short.X + short.W/2
		if got, want := absf(centre-float32(tc.width)/2), absf(centre-(leftEnd+rightStart)/2); got > 1 && want > 1 {
			t.Errorf("%s: the short name is centred at %v, neither the window's %v nor the controls' %v",
				tc.name, centre, float32(tc.width)/2, (leftEnd+rightStart)/2)
		}
		// The room settles: a bar that measured itself again every frame would
		// ask for frames it never draws differently.
		if tt.FramePending() {
			t.Errorf("%s: the bar keeps asking for frames", tc.name)
		}
	}
}
