// layx-reader reads EPUB files with MyGo's native UI: the window is built from
// Go with package ui, page breaking and glyph positions come from the layx
// render engine, and a paint.Painter replays layx's display list through
// ui.Painter. There is no webview and no frontend.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/appstate"
	"github.com/laix/layx-reader/internal/book"
	"github.com/laix/layx-reader/internal/reader"
	"github.com/laix/layx-reader/internal/search"
	"github.com/laix/layx-reader/internal/store"
	"github.com/laix/layx/render"
)

// app is the reader's state. The view builds the interface from it on the UI
// thread; background work publishes results through Window.Update.
type app struct {
	win *mygo.Window
	// winShared is the same window for background goroutines, which must not
	// read the UI thread's field. The UI thread writes both together.
	winShared atomic.Pointer[mygo.Window]
	fonts     *reader.Fonts

	// ready is set once the first window exists. Open-file events that arrive
	// during launch wait in pendingOpen until then.
	ready       bool
	pendingOpen string
	// opening is the path being read off the UI thread, so the same file
	// reported twice (a launch argument and OpenFile) opens once.
	opening string
	// services are the window's clipboard and redraw handles, kept from the
	// last frame for menu commands that run outside a build.
	services ui.Services
	// menuCopy is the Edit menu's page-copy item, and menuCopyOn the enabled
	// state it was last given: the view only touches the native menu when the
	// selection changes.
	menuCopy   *mygo.MenuItem
	menuCopyOn bool

	book  *book.Book
	doc   *render.Document
	docAt float32 // base font size doc was built with
	// layout is immutable once published; the draw callback only reads it.
	layout *render.Layout
	paint  *reader.UIPainter

	page int

	// opts are the reading settings; a change waits settingsDelay so a dragged
	// slider costs one layout rather than thirty, and layOpts is what the
	// published layout was built with.
	opts    reader.Options
	optsAt  time.Time
	layOpts reader.Options
	// theme is the paper being read on; "" follows the system's appearance
	// until the reader picks one.
	theme     reader.Theme
	themeSys  bool
	status    string
	file      string
	startPath string

	// cover is the book's cover image; showCover keeps it in front of page 0
	// until the reader turns a page.
	cover     render.ImageID
	showCover bool

	// Layout job bookkeeping. Only touched on the UI thread.
	layW, layH float32
	loading    bool
	job        int
	failed     bool
	failW      float32
	failH      float32
	failSize   float32

	body ui.Handle
	area ui.Handle

	// sel is the text the reader dragged over, and selQuads its rectangles for
	// the current layout; selDragging marks a press that still selects, and
	// selCopied how many runes the last copy took (0 for none). Only the UI
	// thread touches them, and only the draw callback reads the rectangles.
	sel         reader.Selection
	selQuads    []render.SelectionQuad
	selDragging bool
	selCopied   int
	// hitQuads holds the rectangles of each search hit, by hit index, for the
	// match veils: empty until a search has results on this layout.
	hitQuads [][]render.SelectionQuad

	// pan is the side panels and the persisted state; it is nil only in tests
	// that build the reader alone. st is the same store, for the close flush.
	pan *appstate.App
	st  *store.Store
	// pendingResume restores the saved position once, after the first layout
	// of a newly opened book.
	pendingResume bool
	resumeNote    string

	// skin is the window's own look, rebuilt when the paper or the system's
	// appearance changes; the inputs it was built from are kept so it is not
	// rebuilt per frame.
	skin    ui.Theme
	skinSet bool
	skinKey skinKey
	// chrome is the palette the window's own furniture is drawn in this frame:
	// the reading theme with a book open, the system's own with nothing open.
	chrome reader.Palette

	// sidebarW is the width of the open panel; the user may drag it.
	sidebarW float32
	// areaW is the width of the page area and boxW the width of the sheet as
	// the last frame laid them out: how many pages fit on the sheet, and so how
	// far a turn goes, follow them.
	areaW float32
	boxW  float32
	// turning is the page turn being shown, nil when the sheet is still.
	turning *turnState
	// instant turns the page without sliding, as tests and reduced-motion
	// readers need.
	instant bool

	// now is the clock the page turn is measured against; nil means time.Now,
	// and a test that wants a fixed slide pins it.
	now func() time.Time

	// Library state: which filter the source list has chosen, whether the
	// search field is open and what it holds, whether the source list is shown,
	// and whether the shelf is ordered by title rather than by when it was read.
	libFilter   int
	libSearch   bool
	libQuery    string
	libSideOff  bool
	sortByTitle bool

	// goalBooks and goalDays are the reading goals the home screen measures
	// against: books a year, days a week. goalsOpen is the small panel that
	// sets them, and goalCopied when the share control last copied the record.
	goalBooks  int
	goalDays   int
	goalsOpen  bool
	goalCopied time.Time
	// shelfScroll keeps each cover row's place while the reader scrolls it.
	shelfScroll [2]ui.ScrollState

	// aaOpen is the display settings panel, and its open state. The ui fields
	// mirror the settings the sliders write, which work in float64.
	aaOpen bool
	// aaMore shows the rest of the settings under the theme cards, as Books
	// opens them from "Customize".
	aaMore    bool
	uiSize    float64
	uiLeading float64
	uiMargin  float64
	// covers caches decoded shelf covers, keyed by path and size.
	covers map[coverKey]*ui.Bitmap
}

// pageLabel is the folio's name for where the reader is: the cover, or the
// page and how many the book has, as Books prints "161 of 418".
func (a *app) pageLabel() string {
	if a.showCover {
		return "Cover"
	}
	if a.layout == nil {
		return "— of —"
	}
	return fmt.Sprintf("%d of %d", a.page+1, a.layout.PageCount())
}

// progress is how much of the book has been read, from 0 to 1, for the footer.
func (a *app) progress() float64 {
	if a.layout == nil {
		return 0
	}
	n := a.layout.PageCount()
	if n <= 0 {
		return 0
	}
	read := min(a.page+a.pageStep(), n)
	return float64(read) / float64(n)
}

func (a *app) chapterLabel() string {
	if a.book == nil {
		return ""
	}
	if a.layout == nil || a.showCover {
		return a.book.Title
	}
	i := reader.ChapterOfPage(a.layout, len(a.book.Chapters), a.page)
	if i < 0 || i >= len(a.book.Chapters) {
		return a.book.Title
	}
	return a.book.Chapters[i].Title
}

// canTurn reports whether turn(delta) would do something.
func (a *app) canTurn(delta int) bool {
	if a.showCover {
		return true
	}
	if a.layout == nil {
		return false
	}
	return a.turnTarget(a.page, delta) != a.page
}

// turnTarget is the page turn(delta) goes to, clamped to the book and kept at
// the start of a spread.
func (a *app) turnTarget(page, delta int) int {
	if a.layout == nil {
		return page
	}
	n := a.layout.PageCount()
	step := a.pageStep()
	next := page + delta*step
	if next < 0 {
		next = 0
	}
	if next >= n {
		next = n - step
	}
	return a.snapPage(max(next, 0))
}

func (a *app) turn(delta int) {
	if a.showCover {
		// The first turn puts the cover away and keeps the reading position.
		a.showCover = false
		return
	}
	if a.layout == nil {
		return
	}
	page := a.turnTarget(a.page, delta)
	if page == a.page {
		return
	}
	a.turning = &turnState{from: a.page, to: page, dir: delta, at: a.clock()}
	if a.instant {
		a.turning = nil
	}
	a.page = page
	a.afterPage()
}

// afterPage publishes the reading position to the panels and the store.
func (a *app) afterPage() {
	if a.pan == nil {
		return
	}
	a.pan.Refresh(a.book, a.layout, a.page)
	a.pan.RecordPage(a.layout)
}

// gotoPage is the panels' jump callback: a table of contents row or a search
// hit asked for a page.
func (a *app) gotoPage(page int) {
	if a.layout == nil {
		return
	}
	if page < 0 {
		page = 0
	}
	if n := a.layout.PageCount(); page >= n {
		page = n - 1
	}
	a.page = a.snapPage(page)
	a.showCover = false
	a.turning = nil
	a.afterPage()
}

func (a *app) changeFont(delta float32) {
	o := a.opts
	o.FontSize += delta
	a.setOptions(o)
}

// setOptions publishes reading settings. The layout they need waits until the
// reader stops dragging: settle() starts it settingsDelay after the last change,
// so a slider costs one build rather than one per pixel.
func (a *app) setOptions(o reader.Options) {
	o = o.Normalized()
	if o == a.opts {
		return
	}
	a.opts = o
	a.optsAt = time.Now()
	a.failed = false
	// The sliders read these back: a change made with the keys has to move them
	// too.
	a.uiSize = float64(o.FontSize)
	a.uiLeading = float64(o.LineHeight)
	a.uiMargin = float64(o.Margin)
	a.savePrefs()
}

// settingsDelay is how long a settings change waits before it relayouts.
const settingsDelay = 160 * time.Millisecond

// settle starts the layout a settings change asked for, once the reader has
// stopped moving whatever changed it.
func (a *app) settle(c *ui.Context) {
	if a.optsAt.IsZero() {
		return
	}
	if wait := time.Until(a.optsAt.Add(settingsDelay)); wait > 0 {
		c.After(wait)
		return
	}
	a.optsAt = time.Time{}
	a.layW, a.layH = 0, 0
}

// savePrefs writes the reading settings to the store.
func (a *app) savePrefs() {
	if a.st == nil {
		return
	}
	o, theme := a.opts, a.theme
	a.st.Update(func(s *store.State) {
		s.Prefs.FontSize = o.FontSize
		s.Prefs.LineHeight = o.LineHeight
		s.Prefs.Font = o.Font
		s.Prefs.Justify = o.Justify
		s.Prefs.Margin = o.Margin
		s.Prefs.Spread = o.Spread
		s.Prefs.Theme = string(theme)
	})
	a.st.SaveSoon(0)
}

// setTheme turns the page to another paper. The ink and the sheet are baked into
// the document, so the next layout rebuilds it; the chrome follows at once.
func (a *app) setTheme(t reader.Theme) {
	if t == a.theme {
		return
	}
	a.theme = t
	a.themeSys = false
	a.doc = nil
	a.layW, a.layH = 0, 0
	a.savePrefs()
}

// ensureLayout starts background layout when the sheet, the reading settings or
// the paper changed. It runs on the UI thread and passes only copies to the
// goroutine; Window.Update publishes the result back on the UI thread. The width
// is the whole sheet: two pages share it when the window is wide enough.
func (a *app) ensureLayout(w, h float32) {
	// A settings change still in its quiet period is not laid out yet: settle
	// starts the build once the reader stops moving the slider.
	if !a.optsAt.IsZero() {
		return
	}
	opts := a.opts
	sw, sh := a.sheetSize(w, h)
	// The page keeps its margins whatever the window does: below three of them
	// there is no line to lay out, so say so instead of failing the job.
	if sw < 3*opts.Margin || sh < 3*opts.Margin {
		a.loading = false
		a.failed, a.failW, a.failH, a.failSize = true, w, h, opts.FontSize
		a.status = "窗口太窄：关掉侧栏或把窗口拉宽一些"
		return
	}
	if a.win == nil || a.book == nil || a.fonts == nil {
		return
	}
	if a.failed && w == a.failW && h == a.failH && opts.FontSize == a.failSize {
		return
	}
	if a.loading && sw == a.layW && sh == a.layH && opts.Same(a.layOpts) {
		return
	}
	if !a.loading && a.layout != nil && sw == a.layW && sh == a.layH &&
		a.docAt == opts.FontSize && opts.Same(a.layOpts) {
		return
	}

	a.job++
	job := a.job
	a.loading = true
	a.layW, a.layH = sw, sh
	a.layOpts = opts
	a.failed = false

	bookPtr, fonts, win := a.book, a.fonts, a.win
	doc, docAt := a.doc, a.docAt
	size := opts.FontSize
	ctx := context.Background()
	// The index belongs to a document; a rebuild replaces both. Reading the
	// field here is the UI thread's job, the goroutine only sees the copy.
	var prevIndex *search.Index
	if a.pan != nil {
		prevIndex = a.pan.Index
	}

	go func() {
		var images map[render.ImageID]book.Image
		index := prevIndex
		rebuilt := false
		if doc == nil || docAt != size {
			d, imgs, err := bookPtr.Build(size)
			if err != nil {
				win.Update(func() {
					defer a.refresh(win)
					if job != a.job {
						return
					}
					a.loading = false
					a.failed, a.failW, a.failH, a.failSize = true, w, h, size
					a.status = "排版失败：" + err.Error()
				})
				return
			}
			doc, docAt = d, size
			images = imgs
			rebuilt = true
			// Indexing walks the same nodes the layout will, so it is done
			// with the build and published with it.
			if ix, err := search.NewIndex(bookPtr, d); err == nil {
				index = ix
			} else {
				index = nil
			}
		}
		layout, err := reader.BuildLayout(ctx, doc, fonts, sw, sh, opts)
		win.Update(func() {
			defer a.refresh(win)
			if job != a.job {
				return
			}
			a.loading = false
			if err != nil {
				a.failed, a.failW, a.failH, a.failSize = true, w, h, size
				a.status = "排版失败：" + err.Error()
				return
			}
			// Decoding belongs on the UI thread and happens once per build.
			if rebuilt && a.paint != nil {
				for id, img := range images {
					a.paint.SetImage(id, img.Data)
				}
			}
			a.doc, a.docAt = doc, docAt
			a.layout = layout
			// Node ids belong to one layout: a selection from the old one would
			// point at nothing, and a slide in flight at a page of it.
			a.clearSelection()
			a.turning = nil
			a.page = a.snapPage(min(a.page, layout.PageCount()-1))
			a.page = max(a.page, 0)
			a.status = ""
			if a.pan != nil {
				if rebuilt {
					a.pan.SetIndex(index)
				}
				if a.pendingResume {
					a.pendingResume = false
					if page, how := a.pan.Resume(layout); how != "" {
						a.page = a.snapPage(page)
						a.resumeNote = "上次读到第 " + fmt.Sprint(page+1) + " 页（" + how + "）"
						a.status = a.resumeNote
					}
				}
				a.pan.Refresh(a.book, layout, a.page)
				a.pan.RecordPage(layout)
			}
			// Last, so the marks follow the new geometry and the new index: a
			// rebuild has just cleared the hits.
			a.refreshMarks()
		})
	}()
}

// chooseFile shows the open dialog. It runs on the UI thread and hands the
// blocking dialog to a goroutine; the chosen file comes back to the UI thread.
// The menu bar calls it too, so it makes the window first when the reader
// closed it.
func (a *app) chooseFile() {
	win := a.showWindow()
	if win == nil {
		return
	}
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:  win,
			Title:   "打开 EPUB 或 TXT",
			Filters: []mygo.FileFilter{{Name: "书籍", Extensions: []string{"epub", "txt"}}},
		})
		if err != nil {
			win.Update(func() {
				defer a.refresh(win)
				a.status = "打开失败：" + err.Error()
			})
			return
		}
		if len(paths) == 0 {
			return
		}
		win.Update(func() {
			defer a.refresh(win)
			a.openPath(paths[0])
		})
	}()
}

// chooseDir shows the directory dialog, as chooseFile does for a book.
func (a *app) chooseDir() {
	win := a.showWindow()
	if win == nil || a.pan == nil {
		return
	}
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:    win,
			Title:     "选择书库目录",
			Directory: true,
		})
		if err != nil {
			win.Update(func() {
				defer a.refresh(win)
				a.pan.Shelf.Status = "打开目录失败：" + err.Error()
			})
			return
		}
		if len(paths) == 0 {
			return
		}
		dir := paths[0]
		win.Update(func() {
			defer a.refresh(win)
			a.pan.SetLibraryDir(dir)
		})
	}()
}

// openPath starts reading a book. It runs on the UI thread — the shelf, the
// menu bar and the system's open-file events all call it — and hands the
// parsing to a goroutine. A book that is already open or opening is left alone:
// a launch argument and an OpenFile event name the same file.
func (a *app) openPath(path string) {
	if path == "" {
		return
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	win := a.showWindow()
	if win == nil {
		a.pendingOpen = path
		return
	}
	if a.opening == path || (a.file == path && (a.loading || a.layout != nil)) {
		return
	}
	a.opening = path
	go a.openAsync(win, path)
}

// openAsync parses a book off the UI thread and publishes it through
// Window.Update.
func (a *app) openAsync(win *mygo.Window, path string) {
	b, err := book.Open(path)
	if err != nil {
		win.Update(func() {
			defer a.refresh(win)
			if a.opening == path {
				a.opening = ""
			}
			a.status = "无法读取：" + err.Error()
		})
		return
	}
	win.Update(func() {
		defer a.refresh(win)
		if a.opening == path {
			a.opening = ""
		}
		a.job++ // drop any layout still in flight
		a.loading = false
		a.failed = false
		a.book = b
		a.cover = b.Cover
		a.showCover = b.HasCover
		if a.paint != nil {
			a.paint.ClearImages()
		}
		a.doc = nil
		a.docAt = 0
		a.layout = nil
		a.page = 0
		a.layW, a.layH = 0, 0
		a.turning = nil
		a.clearSelection()
		a.hitQuads = nil
		a.file = path
		a.status = "《" + b.Title + "》"
		a.pendingResume = true
		a.resumeNote = ""
		if a.pan != nil {
			a.pan.Opened(b, path)
			a.pan.Refresh(b, nil, 0)
		}
		win.SetTitle("《" + b.Title + "》 — layx-reader")
		// The proxy icon in the title bar, the Window menu and the Dock's
		// recent list all follow the file being read.
		win.SetRepresentedFile(path)
		_ = mygo.App.AddRecentDocument(path)
	})
}

// windowOptions are the reader's window. Every platform gets the Books-style
// shell: no system title bar, the view runs to the top edge, and its own bars
// drag the window and leave the system's buttons their room. Only where those
// buttons go is the platform's own business — traffic lights at the left on
// macOS, caption buttons at the right on Windows, whichever side the desktop
// configures on Linux — and c.TitleBar() reports it.
func windowOptions(view func(*ui.Context)) mygo.WindowOptions {
	o := mygo.WindowOptions{
		Title:         "layx-reader",
		Width:         787,
		Height:        590,
		MinWidth:      640,
		MinHeight:     420,
		StateKey:      "main",
		TitleBarStyle: mygo.TitleBarHidden,
		// The desk the first frame lands on, so the window does not flash
		// white before the view paints (the system palette's two desks).
		BackgroundColor: "light-dark(#f1f1f1, #4a4a4d)",
		Content:         ui.View(view),
	}
	if runtime.GOOS == "darwin" {
		// Centre the traffic lights in the reader's 46 DIP bar rather than in
		// a standard 28 DIP title bar.
		o.TrafficLightPosition = &mygo.Point{X: 13, Y: 13}
	}
	return o
}

// newWindow makes the reader's window and keeps a.win pointing at it. macOS
// keeps the app running when its last window closes, so this runs again from a
// Dock click.
func (a *app) newWindow() {
	win := mygo.NewWindow(windowOptions(a.view))
	a.win = win
	a.winShared.Store(win)
	win.OnClose(func(*mygo.CloseEvent) {
		// Flush the debounced writing on the way out, and let showWindow make
		// another window if the app stays up.
		if a.st != nil {
			_ = a.st.Flush()
		}
		if a.win == win {
			a.win = nil
			a.winShared.Store(nil)
		}
	})
}

// showWindow returns the window, making one when there is none. Before Run
// there is no window to make: open-file events wait in pendingOpen.
func (a *app) showWindow() *mygo.Window {
	if !a.ready {
		return nil
	}
	if a.win == nil || a.win.IsDestroyed() {
		a.newWindow()
	}
	return a.win
}

// refresh redraws the window the reader is showing now. Work started in one
// window can land after the reader closed it and opened another, whose Update
// the old window cannot ask for a frame.
func (a *app) refresh(win *mygo.Window) {
	if a.win != nil && a.win != win {
		a.win.Invalidate()
	}
}

// menuAction runs a menu command on the main thread and asks the view for a
// frame. The menu bar works with the window closed, and brings it back.
func (a *app) menuAction(fn func()) {
	win := a.showWindow()
	if win == nil {
		return
	}
	fn()
	win.Invalidate()
}

// menuBar is the macOS menu bar. The other platforms keep the window's own
// shortcuts: a native UI window has no menu bar there.
func (a *app) menuBar() *mygo.Menu {
	copySelection := &mygo.MenuItem{
		ID:       "copySelection",
		Label:    "复制选中的文字",
		Disabled: true, // the first frame enables it as the selection appears
		// No accelerator: ⌘C must reach the view for the page selection and
		// the focused search field. An enabled menu item would take the key
		// first and copy neither of them.
		Click: func(*mygo.MenuItem, *mygo.Window) { a.menuAction(a.copyPageText) },
	}
	a.menuCopy = copySelection
	show := func(sidebar string) func(*mygo.MenuItem, *mygo.Window) {
		return func(*mygo.MenuItem, *mygo.Window) {
			a.menuAction(func() {
				if a.pan != nil {
					a.pan.Show(sidebar)
				}
			})
		}
	}
	return mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu},
		{Label: "文件", Submenu: []*mygo.MenuItem{
			{Label: "打开…", Accelerator: "CmdOrCtrl+O", Click: func(*mygo.MenuItem, *mygo.Window) { a.chooseFile() }},
			{Role: mygo.RoleOpenRecent},
			mygo.Separator(),
			{Role: mygo.RoleClose},
		}},
		{Label: "编辑", Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleUndo},
			{Role: mygo.RoleRedo},
			mygo.Separator(),
			{Role: mygo.RoleCut},
			{Role: mygo.RolePaste},
			{Role: mygo.RoleSelectAll},
			mygo.Separator(),
			copySelection,
		}},
		{Label: "显示", Submenu: []*mygo.MenuItem{
			{Label: "侧栏", Accelerator: "CmdOrCtrl+B", Click: func(*mygo.MenuItem, *mygo.Window) { a.menuAction(a.toggleSidebar) }},
			{Label: "按书名排序", Type: mygo.MenuItemCheckbox, Checked: a.sortByTitle, Click: func(*mygo.MenuItem, *mygo.Window) {
				a.menuAction(func() { a.sortByTitle = !a.sortByTitle })
			}},
			{Label: "目录", Accelerator: "CmdOrCtrl+T", Click: show(appstate.SidebarToc)},
			{Label: "搜索", Accelerator: "CmdOrCtrl+F", Click: show(appstate.SidebarSearch)},
			{Label: "显示设置…", Accelerator: "CmdOrCtrl+,", Click: func(*mygo.MenuItem, *mygo.Window) {
				a.menuAction(func() { a.aaOpen = true })
			}},
			mygo.Separator(),
			{Role: mygo.RoleToggleFullScreen},
		}},
		{Role: mygo.RoleWindowMenu},
		{Role: mygo.RoleHelp},
	})
}

func main() {
	fontFlag := flag.String("font", "", "path to a .ttf/.otf/.ttc font (default: $LAYX_FONT or a system CJK font)")
	shelfFlag := flag.String("shelf", "", "directory of EPUB/TXT files to show in the shelf")
	serifFlag := flag.String("serif", "", "path to the serif face (default: $LAYX_SERIF or a system serif)")
	stateFlag := flag.String("state", "", "state file (default: the config directory)")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: layx-reader [-font path] [-shelf dir] [book.epub]\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	// A second launch — a file double-clicked in the Finder while the reader
	// runs — hands its files to the instance that is already up.
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}

	// The store is optional: a broken config directory must not stop the
	// reader from opening a book from the command line.
	statePath := *stateFlag
	if statePath == "" {
		if p, err := store.DefaultPath(); err == nil {
			statePath = p
		} else {
			log.Println(err)
		}
	}
	var st *store.Store
	if statePath != "" {
		s, err := store.Open(statePath)
		if err != nil {
			log.Println(err)
		} else {
			st = s
		}
	}

	a := &app{
		opts:      reader.DefaultOptions(),
		instant:   os.Getenv("LAYX_NO_ANIM") != "",
		status:    "",
		startPath: flag.Arg(0),
		st:        st,
		pan:       appstate.New(st),
	}
	a.goalBooks, a.goalDays = defaultGoalBooks, defaultGoalDays
	// The window opens on Books' own reading paper, the graphite one; the
	// display settings switch it to any of the other five.
	a.theme = reader.ThemeQuiet
	a.themeSys = false
	if st != nil {
		prefs := st.State().Prefs
		if prefs.GoalBooks > 0 {
			a.goalBooks = prefs.GoalBooks
		}
		if prefs.GoalDays > 0 {
			a.goalDays = prefs.GoalDays
		}
		if prefs.FontSize == 0 && prefs.Theme == "" {
			// Record the defaults once, so the state file shows the shape.
			a.savePrefs()
		}
		a.opts = reader.Options{
			FontSize:   prefs.FontSize,
			LineHeight: prefs.LineHeight,
			Font:       prefs.Font,
			Justify:    prefs.Justify,
			Margin:     prefs.Margin,
			Spread:     prefs.Spread,
		}.Normalized()
		if prefs.Theme != "" {
			a.theme = reader.ParseTheme(prefs.Theme)
			a.themeSys = false
		}
	}
	a.pan.OnPickDir = a.chooseDir
	a.pan.Cover = a.coverBitmap
	a.pan.Open = a.openPath
	a.pan.Jump = a.gotoPage
	a.pan.OnHits = a.refreshMarks
	a.pan.Post = func(fn func()) {
		if win := a.winShared.Load(); win != nil {
			win.Update(func() {
				defer a.refresh(win)
				fn()
			})
		}
	}
	a.syncSettingsFields()
	if a.pan.SidebarWidth > 0 {
		a.sidebarW = a.pan.SidebarWidth
	}
	// A remembered table of contents or search panel has nothing to show
	// before a book is open: start on the shelf instead.
	if a.startPath == "" && a.pan.Showing() && a.pan.Sidebar != appstate.SidebarShelf {
		a.pan.Show(appstate.SidebarShelf)
	}
	if *shelfFlag != "" {
		a.pan.SetLibraryDir(*shelfFlag)
	}

	if fontPath, err := reader.FindFont(*fontFlag); err != nil {
		a.status = err.Error()
		log.Println(err)
	} else if fonts, err := reader.OpenFonts(fontPath, reader.FindSerif(*serifFlag)); err != nil {
		a.status = err.Error()
		log.Println(err)
	} else {
		a.fonts = fonts
		a.paint = reader.NewUIPainter(nil, fonts)
		log.Printf("layx-reader: font %s", fontPath)
	}
	// A file opened with the app — from the Finder, the Dock, Open Recent or a
	// second instance — may arrive before the window exists.
	mygo.App.OnOpenFile(a.openPath)
	// A second instance only brings the reader forward; MyGo passes its files
	// to OnOpenFile above.
	mygo.App.OnSecondInstance(func([]string, string) {
		if win := a.showWindow(); win != nil {
			win.Restore()
			win.Focus()
		}
	})
	// The app speaks Chinese: MyGo's own strings, including the menu roles',
	// follow. Set before the menu is built.
	mygo.App.SetLanguages("zh-Hans")
	if runtime.GOOS == "darwin" {
		// Closing the window leaves the app in the Dock, as every Mac app;
		// clicking the icon brings back a window with the reader as it was.
		mygo.App.OnWindowAllClosed(func() {})
		mygo.App.OnActivate(func(hasVisibleWindows bool) {
			if hasVisibleWindows {
				return
			}
			if win := a.showWindow(); win != nil {
				win.Show()
				win.Focus()
			}
		})
		mygo.App.SetMenu(a.menuBar())
	}

	mygo.App.WhenReady(func() {
		a.ready = true
		a.newWindow()
		if a.pendingOpen == "" && a.startPath != "" {
			if abs, err := filepath.Abs(a.startPath); err == nil {
				a.startPath = abs
			}
			a.pendingOpen = a.startPath
		}
		if a.pendingOpen != "" {
			path := a.pendingOpen
			a.pendingOpen = ""
			a.openPath(path)
		}
	})
	if err := mygo.App.Run(); err != nil {
		if st != nil {
			_ = st.Close()
		}
		log.Fatal(err)
	}
	if st != nil {
		if err := st.Close(); err != nil {
			log.Println(err)
		}
	}
}
