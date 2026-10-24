package main

import (
	"context"
	"fmt"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/appstate"
	"github.com/laix/layx-reader/internal/book"
	"github.com/laix/layx-reader/internal/library"
	"github.com/laix/layx-reader/internal/reader"
	"github.com/laix/layx-reader/internal/search"
	"github.com/laix/layx-reader/internal/store"
	"github.com/laix/layx/render/paint"
	"strconv"
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

	lib := &library.Library{Dir: "internal/book/testdata/samples"}
	if err := lib.Reload(); err != nil {
		t.Fatal(err)
	}

	paint := reader.NewUIPainter(nil, fonts)
	for id, img := range images {
		paint.SetImage(id, img.Data)
	}
	pan := appstate.New(st)
	pan.Library = lib
	pan.Shelf.Lib = lib

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
	pan.Jump = a.gotoPage
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
		name   string
		dark   bool
		want   string
		w, h   int // 0 is 1180×800
		render func(t *testing.T)
	}{
		// One page on the sheet, as the reader shows a book in a window too
		// narrow for the pair.
		{name: "reader", want: "5 of ", render: func(t *testing.T) {
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
		{name: "toc", want: bk.Chapters[0].Title, render: func(*testing.T) {
			pan.Show(appstate.SidebarToc)
		}},
		{name: "bookmarks", want: "还没有书签", render: func(*testing.T) {
			pan.Show(appstate.SidebarBookmarks)
		}},
		{name: "notes", want: "还没有笔记", render: func(*testing.T) {
			pan.Show(appstate.SidebarNotes)
		}},
		{name: "search", want: "条结果", render: func(t *testing.T) {
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
		{name: "highlight", want: "Selected", render: func(t *testing.T) {
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
		{name: "cover", want: "Cover", render: func(*testing.T) {
			pan.Show(appstate.SidebarNone)
			a.showCover = true
		}}, {name: "home", want: "3 本书", render: func(t *testing.T) {
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
		{name: "home-prototype", w: 818, h: 612, want: "3 本书", render: func(*testing.T) {}},
		// The same page at a wide window and near the minimum: the measures follow
		// the width, so the covers and margins grow or shrink instead of staying
		// at the reference window's numbers.
		{name: "home-wide", w: 1440, h: 900, want: "3 本书", render: func(*testing.T) {}},
		{name: "home-narrow", w: 700, h: 520, want: "继续阅读", render: func(*testing.T) {}},
		// The display settings: the four papers, the size, the leading, the
		// face, the alignment, the margins and the spread.
		{name: "settings", want: "Two Pages", render: func(t *testing.T) {
			a.book, a.page = bk, 4
			a.showCover = false
			a.aaOpen, a.aaMore = true, true
			a.syncSettingsFields()
			pan.Show(appstate.SidebarNone)
		}},
		// An open book: two pages side by side once the window is wide enough.
		{name: "spread", w: 1280, want: "5 of ", render: func(t *testing.T) {
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
		{name: "narrow", w: 700, h: 520, want: "5 of ", render: func(t *testing.T) {
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
		{name: "dark", dark: true, want: a.pageLabel(), render: func(t *testing.T) {
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
	}

	for _, s := range shots {
		w, h := s.w, s.h
		if w == 0 || h == 0 {
			w, h = 1180, 800
		}
		tt := ui.NewTester(a.view, w, h)
		// The window declares Chinese as the app's language, so MyGo's own
		// strings follow it; the tester starts in English.
		tt.SetLocale("zh-Hans")
		if s.dark {
			tt.SetDark(true)
		}
		s.render(t)
		tt.Frame()
		if !tt.HasText(s.want) {
			t.Errorf("%s: %q is missing from %q", s.name, s.want, tt.Texts())
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
	tt.SetLocale("zh-Hans")
	tt.Frame()

	box, ok := tt.Find("书页")
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
	tt.SetLocale("zh-Hans")
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
	tt.SetLocale("zh-Hans")
	tt.Frame()

	box, ok := tt.Find("书页")
	if !ok {
		t.Fatalf("the page box is not labelled: %q", tt.Texts())
	}

	// Nothing selected: the menu is there, but copying is not.
	tt.RightClickAt(box.X+120, box.Y+120)
	menu := strings.Join(tt.Menu(), "|")
	if !strings.Contains(menu, "复制") || !strings.Contains(menu, "选中本页") {
		t.Fatalf("the page menu has no selection commands: %q", menu)
	}
	if err := tt.ChooseMenuItem("复制"); err == nil {
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
	if err := tt.ChooseMenuItem("复制"); err != nil {
		t.Fatalf("copy from the menu: %v (menu %q)", err, strings.Join(tt.Menu(), "|"))
	}
	if got := tt.Clipboard(); got != want {
		t.Errorf("clipboard = %q, want %q", got, want)
	}

	// 选中本页 covers the page, 清除选区 drops it.
	tt.RightClickAt(box.X+120, box.Y+120)
	tt.Frame()
	if err := tt.ChooseMenuItem("选中本页"); err != nil {
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
	if err := tt.ChooseMenuItem("清除选区"); err != nil {
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
	tt.SetLocale("zh-Hans")
	tt.Frame()

	if err := tt.Click("Aa"); err != nil {
		t.Fatalf("no display settings button: %v (%q)", err, tt.Texts())
	}
	// The papers and the size are on the panel itself; the rest is one press
	// away, under 自定义, as Books arranges it.
	for _, want := range []string{"Themes & Settings", "Original", "Calm", "Paper", "Bold", "Customize"} {
		if !tt.HasText(want) {
			t.Fatalf("the panel has no %q: %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Customize"); err != nil {
		t.Fatalf("no 自定义 row: %v", err)
	}
	tt.Frame()
	for _, want := range []string{"Size", "Leading", "Typeface", "Alignment", "Margins", "Two Pages"} {
		if !tt.HasText(want) {
			t.Fatalf("the panel has no %q: %q", want, tt.Texts())
		}
	}

	// The paper: the swatch is the page's own pair of colours.
	if err := tt.Click("Original"); err != nil {
		t.Fatalf("no white theme: %v", err)
	}
	if got := reader.Current().Theme; got != reader.ThemeWhite {
		t.Errorf("paper is %q after choosing 白色", got)
	}
	if got := a.st.State().Prefs.Theme; got != string(reader.ThemeWhite) {
		t.Errorf("saved theme %q, want white", got)
	}

	if err := tt.Click("Sans"); err != nil {
		t.Fatalf("no sans choice: %v", err)
	}
	if a.opts.Font != reader.FontSans {
		t.Errorf("face %q after choosing 黑体", a.opts.Font)
	}
	if err := tt.Click("Left"); err != nil {
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
	if err := tt.Click("Two Pages"); err != nil {
		t.Fatalf("no spread switch: %v", err)
	}
	if a.opts.Spread {
		t.Errorf("spread still on after choosing 双页显示")
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
	tt.SetLocale("zh-Hans")
	tt.Frame()
	if got := a.pagesOnSheet(); got != 2 {
		t.Fatalf("%d pages on a 1180 wide sheet", got)
	}
	if got, want := a.pageLabel(), "5 of "+strconv.Itoa(two.PageCount()); got != want {
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

// The page slides: the turn reports its progress from the clock, the sheet draws
// both spreads while it is under way, and it lands once.
func TestTurnSlidesAndLands(t *testing.T) {
	a := newWindow(t)
	a.instant = false
	tt := ui.NewTester(a.view, 1180, 800)
	tt.SetLocale("zh-Hans")
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

// Escape closes what is open and, with nothing left, goes back to the library,
// as Books does.
func TestEscapeGoesBackToTheLibrary(t *testing.T) {
	a := newWindow(t)
	tt := ui.NewTester(a.view, 1180, 800)
	tt.SetLocale("zh-Hans")
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.book != nil {
		t.Errorf("the book is still open after Escape: %q", tt.Texts())
	}
	if !tt.HasText("书库") {
		t.Errorf("no library after Escape: %q", tt.Texts())
	}
}

// A window with a hidden title bar hands the view the room the traffic lights
// take: the reader's bar starts to their right, and the library's source list
// starts below them.
func TestWindowControlsLeaveRoomInTheView(t *testing.T) {
	bar := ui.TitleBar{Height: 28, Left: 78}

	a := newWindow(t)
	tt := ui.NewTester(a.view, 1180, 800)
	tt.SetTitleBar(bar)
	btn, ok := tt.Find("目录")
	if !ok {
		t.Fatalf("the reader's bar is missing: %q", tt.Texts())
	}
	if btn.X < bar.Left {
		t.Errorf("the reader's bar starts at x=%g, under the traffic lights (left %g)", btn.X, bar.Left)
	}

	lib := &app{opts: reader.DefaultOptions(), instant: true}
	lt := ui.NewTester(lib.view, 900, 700)
	lt.SetTitleBar(bar)
	row, ok := lt.Find("搜索")
	if !ok {
		t.Fatalf("the library's source list is missing: %q", lt.Texts())
	}
	if row.Y < bar.Height {
		t.Errorf("the source list starts at y=%g, under the traffic lights (height %g)", row.Y, bar.Height)
	}

	// Windows and some Linux desktops put the buttons at the right instead:
	// the reader's settings and the library's toolbar keep out of their way
	// there too. The buttons fill the header's height, as windowOptions asks,
	// and the toolbar's controls centre on that same line.
	right := ui.TitleBar{Height: headerHeight, Right: 138}

	rt := ui.NewTester(a.view, 1180, 800)
	rt.SetTitleBar(right)
	aa, ok := rt.Find("Aa")
	if !ok {
		t.Fatalf("the reader's settings button is missing: %q", rt.Texts())
	}
	if aa.X+aa.W > 1180-right.Right {
		t.Errorf("the reader's bar ends at x=%g, under the caption buttons (right %g)", aa.X+aa.W, right.Right)
	}
	if mid, want := aa.Y+aa.H/2, float32(headerHeight)/2; mid != want {
		t.Errorf("the reader's settings sit at y=%g, off the caption buttons' line y=%g", mid, want)
	}

	rlt := ui.NewTester(lib.view, 900, 700)
	rlt.SetTitleBar(right)
	ring, ok := rlt.Find("本周阅读")
	if !ok {
		t.Fatalf("the library's reading ring is missing: %q", rlt.Texts())
	}
	if ring.X+ring.W > 900-right.Right {
		t.Errorf("the library's reading ring ends at x=%g, under the caption buttons (right %g)", ring.X+ring.W, right.Right)
	}
}

// With no hidden title bar, the view keeps its own margins: the controls the
// view receives are zero and nothing moves.
func TestNoTitleBarKeepsTheViewMargins(t *testing.T) {
	a := newWindow(t)
	tt := ui.NewTester(a.view, 1180, 800)
	got, ok := tt.Find("目录")
	if !ok {
		t.Fatalf("the reader's bar is missing: %q", tt.Texts())
	}
	want := float32(9) // cluster padding + header padding
	if got.X != want {
		t.Errorf("the reader's bar starts at x=%g, want %g", got.X, want)
	}
}

// The reading bar's first cluster is the three panels, as Books has them:
// contents, bookmarks and notes. A click opens the panel over the page and a
// second click on the same button puts it away.
func TestReadingBarOpensThePanels(t *testing.T) {
	a := newWindow(t)
	tt := ui.NewTester(a.view, 1180, 800)
	for _, label := range []string{"目录", "书签", "笔记"} {
		if _, ok := tt.Find(label); !ok {
			t.Fatalf("no %s button in the reading bar: %q", label, tt.Texts())
		}
	}

	if err := tt.Click("书签"); err != nil {
		t.Fatalf("bookmarks button: %v (%q)", err, tt.Texts())
	}
	if a.pan.Sidebar != appstate.SidebarBookmarks {
		t.Fatalf("panel %q after clicking 书签", a.pan.Sidebar)
	}
	if !tt.HasText("还没有书签") {
		t.Errorf("the bookmarks panel is not drawn: %q", tt.Texts())
	}
	if err := tt.Click("书签"); err != nil {
		t.Fatalf("bookmarks button again: %v (%q)", err, tt.Texts())
	}
	if a.pan.Showing() {
		t.Errorf("panel %q after a second click on 书签", a.pan.Sidebar)
	}

	if err := tt.Click("笔记"); err != nil {
		t.Fatalf("notes button: %v (%q)", err, tt.Texts())
	}
	if a.pan.Sidebar != appstate.SidebarNotes || !tt.HasText("还没有笔记") {
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
	before, ok := tt.Find("书页")
	if !ok {
		t.Fatalf("no page to measure: %q", tt.Texts())
	}
	if err := tt.Click("目录"); err != nil {
		t.Fatalf("contents button: %v (%q)", err, tt.Texts())
	}
	after, ok := tt.Find("书页")
	if !ok {
		t.Fatalf("the page is gone while the panel is open: %q", tt.Texts())
	}
	if after != before {
		t.Errorf("the page moved when the panel opened: %+v -> %+v", before, after)
	}
	// The panel's own header says how many chapters it lists; it is the one
	// text that is only in the card, not in the window's bar.
	count, ok := tt.Find(fmt.Sprintf("%d 章", len(a.pan.Toc.Entries)))
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
	if _, ok := tt.Find("返回书库"); !ok {
		t.Fatalf("no way back at the page's left edge: %q", tt.Texts())
	}
	if _, ok := tt.Find("下一页"); ok {
		t.Errorf("the right edge still turns pages: %q", tt.Texts())
	}
	if _, ok := tt.Find("上一页"); ok {
		t.Errorf("the left edge still turns pages: %q", tt.Texts())
	}

	before, ok := tt.Find("书页")
	if !ok {
		t.Fatalf("no page to measure: %q", tt.Texts())
	}
	tt.Move(8, 400)
	tt.Frame()
	after, ok := tt.Find("书页")
	if !ok || after != before {
		t.Errorf("the page moved when the chevron showed: %+v -> %+v", before, after)
	}

	tt.ClickAt(8, 400)
	if a.book != nil {
		t.Errorf("the book is still open after the left edge click")
	}
	if !tt.HasText("首页") {
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
	body, ok := tt.Find("书页")
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
