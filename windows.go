package main

// A desk is the set of windows the reader has open. Every book is read in its
// own window, as Books does: the reader can keep a shelf open, put a novel down
// and open another beside it, and each window keeps its own pages, panels and
// place. What belongs to the app rather than to a window — the store, the
// reading faces, the paper, the goals and the menu bar — lives on the desk, and
// a window reads it through its desk pointer.
//
// A window is an *app. The two are one value on purpose: every method that
// builds a page or answers a shortcut is written against *app, and giving each
// window its own app is what makes those methods mean "this window" again.

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"

	"github.com/egoist/mygo"
	"github.com/laix/quire/internal/acquire"
	"github.com/laix/quire/internal/appstate"
	"github.com/laix/quire/internal/reader"
	"github.com/laix/quire/internal/source"
	"github.com/laix/quire/internal/sourceconf"
	"github.com/laix/quire/internal/store"
)

// desk is the app's shared state and the list of its windows. It is built once
// on the main thread; the windows it makes all live there too.
type desk struct {
	// st is the store every window writes through: the shelf and each book's
	// reading position are the app's, not a window's.
	st   *store.Store
	wins []*app
	// library is the one window that shows the shelf. Books are read in
	// windows of their own, so it stays behind them; it is made again from a
	// book when the reader closed it.
	library *app
	// ready is set once MyGo is up and a window can be made. Open-file events
	// that arrive during launch wait in pendingOpen until then.
	ready       bool
	pendingOpen string
	// seq numbers the book windows, so each one remembers its own frame and
	// size in the app's state (WindowOptions.StateKey) instead of fighting over
	// one. The library always keeps the first key.
	seq int

	// The settings a window starts from, and the ones that belong to the whole
	// app: the paper and the reading goals are changed for every window at
	// once, while the typography of a book (size, margins, mode) stays with the
	// window it was set in. opts is the last setting saved, which is what a new
	// window opens with.
	opts     reader.Options
	theme    reader.Theme
	themeSys bool
	// systemDark is the desktop's appearance as last seen while MyGo followed
	// it. A focused book window may force the process-wide appearance so its
	// title buttons match the paper; the library still draws its own chrome on
	// this remembered desktop appearance.
	systemDark    bool
	systemDarkSet bool
	// goalBooks and goalDays are the reading goals the home screen measures
	// against; every window shows the same year.
	goalBooks int
	goalDays  int

	// fontSans and fontSerif are the faces every window is read in, fontErr why
	// there is none, and fonts the set parsed once for the whole app: a second
	// window opening a book does not pay for the system CJK face again.
	fontSans, fontSerif string
	fontErr             error
	fontMu              sync.Mutex
	fonts               *reader.Fonts

	// Book sources: configuration, registry of sources, and download manager.
	// These are shared by every window; the source sheet reads them through the
	// app's desk pointer.
	srcConf *sourceconf.Store
	sources *source.Registry
	acq     *acquire.Manager

	// The menu bar is the app's, not a window's: its items name the focused
	// window's selection and reading mode. menuOwner is the app whose menuBar
	// call installed them.
	menuOwner   *app
	menuCopy    *mygo.MenuItem
	menuAddNote *mygo.MenuItem
	menuScroll  *mygo.MenuItem
	// menuCopyOn is the enabled state the shared copy item was last given; the
	// focused window's frames compare against it rather than touching the native
	// menu every frame.
	menuCopyOn bool
}

// add puts a window's reader on the desk.
func (d *desk) add(a *app) {
	d.wins = append(d.wins, a)
}

// remove drops a window whose own (or last) window was closed.
func (d *desk) remove(a *app) {
	for i, w := range d.wins {
		if w == a {
			d.wins = append(d.wins[:i], d.wins[i+1:]...)
			return
		}
	}
}

// byWindow is the reader a MyGo window draws.
func (d *desk) byWindow(win *mygo.Window) *app {
	if win == nil {
		return nil
	}
	for _, a := range d.wins {
		if a.win == win {
			return a
		}
	}
	return nil
}

// focused is the window the reader is looking at: MyGo's focused one, or the
// last one still open when the app itself is not focused.
func (d *desk) focused() *app {
	if a := d.byWindow(mygo.FocusedWindow()); a != nil {
		return a
	}
	for i := len(d.wins) - 1; i >= 0; i-- {
		if a := d.wins[i]; a.live() {
			return a
		}
	}
	return nil
}

// emptyBookWindow is a book window showing neither a book nor an opening: a
// file it could not read, or one whose book never arrived. The next book opened
// from its menu belongs there rather than in yet another window.
func (d *desk) emptyBookWindow() *app {
	if a := d.focused(); a != nil && a.bookWindow && a.book == nil && a.opening == "" {
		return a
	}
	return nil
}

// holding reports the window that already has path open or on its way. A
// launch argument and an OpenFile event name the same file; this is what makes
// them one window.
func (d *desk) holding(path string) *app {
	for _, a := range d.wins {
		if a.opening == path || (a.file == path && a.hasBook()) {
			return a
		}
	}
	return nil
}

// nextBookKey names the book window being made. The library owns "main"; each
// book gets a key of its own, so the windows remember where they were put
// without overwriting each other.
func (d *desk) nextBookKey() string {
	d.seq++
	return fmt.Sprintf("book-%d", d.seq)
}

// newApp builds one window's reader from the desk's shared state. It is added
// with add, given its window, and marked as the library or as a book window by
// the caller.
func (d *desk) newApp() *app {
	a := &app{
		desk:        d,
		phone:       onPhone(),
		st:          d.st,
		opts:        d.opts,
		theme:       d.theme,
		themeSys:    d.themeSys,
		goalBooks:   d.goalBooks,
		goalDays:    d.goalDays,
		fontSans:    d.fontSans,
		fontSerif:   d.fontSerif,
		fontErr:     d.fontErr,
		fonts:       d.fontsNow(),
		instant:     os.Getenv("LAYX_NO_ANIM") != "",
		pan:         appstate.New(d.st),
		menuCopy:    d.menuCopy,
		menuAddNote: d.menuAddNote,
		menuScroll:  d.menuScroll,
		srcConf:     d.srcConf,
		sources:     d.sources,
		acq:         d.acq,
		find:        &sourceSheet{},
	}
	a.menuCopyOn = d.menuCopyOn
	// The scope belongs to this window even before it has been made: the first
	// search or download starts only after the window exists, but every task
	// must be cancelable by the time it does.
	a.tasks.Store(newTaskScope(nil, a.post))
	if a.fonts != nil {
		// A window made after the faces were parsed can read at once: the
		// painter is per window because it holds this window's glyphs and
		// images, and the faces themselves are read-only and shared.
		a.paint = reader.NewUIPainterWithPalette(nil, a.fonts, a.palette())
	}
	a.pan.OnPickDir = a.chooseDir
	a.pan.Reading = a.currentLoc
	a.pan.Cover = a.coverBitmap
	a.pan.Open = a.openPath
	a.pan.Jump = a.gotoLoc
	a.pan.OnHits = a.refreshMarks
	a.wireNoteActions()
	a.pan.OnNotes = a.refreshMarks
	a.pan.Post = a.post
	a.syncSettingsFields()
	return a
}

// openReader makes the window for one book. It is the only way a book is
// opened: the shelf, the File menu, the Finder and Open Recent all come here,
// and the library window stays where it is.
func (d *desk) openReader(path string) *app {
	a := d.newApp()
	a.bookWindow = true
	d.add(a)
	a.newWindow()
	a.openHere(path)
	return a
}

// showLibrary brings the library window forward, making it when the reader
// closed it. The app's own window is the shelf; a book is read in its own.
func (d *desk) showLibrary() *app {
	if d.library != nil && d.library.live() {
		d.library.focusWindow()
		return d.library
	}
	if !d.ready {
		return nil
	}
	a := d.newApp()
	d.add(a)
	d.library = a
	a.newWindow()
	if a.pan != nil {
		a.pan.ReloadShelf()
	}
	return a
}

// focusOrOpen returns a window, making the library when none is left. The app
// stays in the Dock on macOS, so a menu command can run with no window at all.
func (d *desk) focusOrOpen() *app {
	if a := d.focused(); a != nil {
		return a
	}
	return d.showLibrary()
}

// menuApp is the window a menu-bar command acts on: the one the reader is
// looking at, or a new one when the last window was closed.
func (d *desk) menuApp() *app {
	if a := d.focused(); a != nil {
		return a
	}
	if d.menuOwner != nil && d.menuOwner.live() {
		return d.menuOwner
	}
	return d.focusOrOpen()
}

// fontsNow is the set parsed so far, for a window made while another reads.
func (d *desk) fontsNow() *reader.Fonts {
	d.fontMu.Lock()
	defer d.fontMu.Unlock()
	return d.fonts
}

// loadFonts parses the faces a book is set in, once for the whole app. A
// window waits for another's parse rather than parsing the same system face
// again, which costs tens of megabytes.
func (d *desk) loadFonts(face, sans, serifPath string) (*reader.Fonts, error) {
	d.fontMu.Lock()
	defer d.fontMu.Unlock()
	if d.fonts != nil && (face != reader.FontSerif || d.fonts.HasSerif()) {
		return d.fonts, nil
	}
	serif := serifPath
	if face != reader.FontSerif {
		serif = ""
	}
	fonts, err := reader.OpenFonts(sans, serif)
	if err != nil {
		return nil, err
	}
	log.Printf("quire: font %s, serif %q", fonts.Path, fonts.SerifPath)
	// Parsing the faces leaves the tables the layout keeps and, beside them, the
	// copies and scratch the parse was made with. Hand that garbage back to the
	// system now, while no page waits for it, rather than at the next collection:
	// it is most of what a freshly opened book adds to the process.
	debug.FreeOSMemory()
	d.fonts = fonts
	return fonts, nil
}

// refreshShelves republishes the shelf in every window showing it, so a book
// opened, imported or removed in one window appears in all of them. The window
// that made the change is skipped: its own panels already know.
func (d *desk) refreshShelves(from *app) {
	for _, a := range d.wins {
		if a == from || a.pan == nil || a.hasBook() {
			continue
		}
		a.pan.ReloadShelf()
	}
}

// setTheme turns every window to another paper. The palette the pages are laid
// out with is the process's, not a window's, so a window with its own paper
// would repaint in the other window's colours; the app keeps one paper for that
// reason, as Books does.
func (d *desk) setTheme(t reader.Theme, from *app) {
	if d.theme == t && !d.themeSys {
		return
	}
	perf.event("paper %q -> %q for %d window(s) (a repaint, no layout)", string(d.theme), string(t), len(d.wins))
	d.theme, d.themeSys = t, false
	for _, a := range d.wins {
		a.theme, a.themeSys = t, false
		if a.win != nil {
			a.win.Invalidate()
		}
	}
	if from != nil {
		// Saves the reading settings with the paper, through the window that
		// asked for the change.
		from.theme, from.themeSys = t, false
		from.savePrefs()
	}
}

// libraryChromeDark returns the light/dark appearance the library's chrome
// should use. current is the frame's default theme, which MyGo derives from the
// process-wide appearance a focused book window may have forced. followsSystem
// says whether that appearance is the desktop's own; only then is current
// allowed to replace what the library remembered.
func (d *desk) libraryChromeDark(current, followsSystem bool) bool {
	if followsSystem {
		d.systemDark, d.systemDarkSet = current, true
		return current
	}
	if d.systemDarkSet {
		return d.systemDark
	}
	return current
}

// hasBook reports whether this window is reading or opening a book, as opposed
// to showing the library. It is what keeps a book from being opened over the
// shelf and what keeps the shelf from being reloaded under a page.
func (a *app) hasBook() bool {
	return a.book != nil || a.loading || a.opening != ""
}

// live reports whether this reader still has a window.
func (a *app) live() bool {
	return a.win != nil && !a.win.IsDestroyed()
}

// focusWindow brings this reader's window to the front.
func (a *app) focusWindow() {
	if !a.live() {
		return
	}
	a.win.Restore()
	a.win.Focus()
}

// openPath opens a book in its own window: the shelf, the File menu and the
// Finder all come through here. The parsing happens off the UI thread (see
// openAsync).
func (a *app) openPath(path string) {
	if path == "" {
		return
	}
	if a.desk == nil {
		// A reader built by hand, as the tests do: it owns its only window.
		a.openHere(path)
		return
	}
	a.desk.openPath(path)
}

// openHere starts reading path in this window. It is what the desk calls once
// it has chosen the window; the shelf's own rows never bypass that choice.
func (a *app) openHere(path string) {
	if path == "" {
		return
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	win := a.showWindow()
	if win == nil {
		if a.desk != nil {
			a.desk.pendingOpen = path
		}
		return
	}
	if a.opening == path || (a.file == path && (a.loading || a.layout != nil)) {
		return
	}
	a.opening = path
	// An error from the book before this one is not about this one, and neither
	// is the place the bars were left in while it was read.
	a.status = ""
	a.failed = false
	a.chromeAway = false
	// The faces a book is set in are read with it, the first time a book is
	// read: a shelf alone never pays for the system's CJK face, which costs
	// tens of megabytes once parsed. The serif file is only read for a book set
	// in the serif face.
	face := a.opts.Font
	load := !a.fontsLoading && (a.fonts == nil || face == reader.FontSerif && !a.fonts.HasSerif())
	if load {
		a.fontsLoading = true
	}
	go a.openAsync(win, path, a.fonts, face, load)
}

// openPath opens a book in a window of its own, whatever window asked for it.
// The library is the app's shelf, not a place to read; a book already open is
// brought forward rather than opened twice.
func (d *desk) openPath(path string) {
	if path == "" {
		return
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if !d.ready {
		d.pendingOpen = path
		return
	}
	if a := d.holding(path); a != nil {
		a.focusWindow()
		return
	}
	if a := d.emptyBookWindow(); a != nil {
		a.openHere(path)
		return
	}
	d.openReader(path)
}

// openExternal opens a book the system handed the app — a Finder double-click,
// Open Recent, a second launch — in a window of its own. The library window is
// left alone, as it is for a book picked from its shelf.
func (d *desk) openExternal(path string) {
	if path == "" {
		return
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if !d.ready {
		d.pendingOpen = path
		return
	}
	if a := d.holding(path); a != nil {
		a.focusWindow()
		return
	}
	if a := d.emptyBookWindow(); a != nil {
		a.openHere(path)
		return
	}
	d.openReader(path)
}

// newWindow makes this reader's window and keeps a.win pointing at it. A book
// window's key belongs to that book; the library keeps the first key, so its
// frame comes back when it is made again.
func (a *app) newWindow() {
	key := "main"
	if a.desk != nil && a.bookWindow {
		key = a.desk.nextBookKey()
	}
	opts := windowOptions(a.view, key)
	if a.phone {
		// A phone's window is the screen: there is no desk to size, and the
		// edges around the view take the colour of what it shows rather than a
		// desktop's grey.
		opts.MinWidth, opts.MinHeight = 0, 0
		opts.Width, opts.Height = 390, 844
		opts.BackgroundColor = a.phoneStartColor()
	}
	win := mygo.NewWindow(opts)
	a.win = win
	a.winShared.Store(win)
	a.ready = true
	win.OnClose(func(*mygo.CloseEvent) {
		// Nothing this window started may land in the next one. Cancel it
		// before the window and its shared pointer go away.
		a.closeTasks()
		// Flush the debounced writing on the way out, and let the next window
		// carry the app.
		if a.st != nil {
			_ = a.st.Flush()
		}
		if a.win == win {
			a.win = nil
			a.winShared.Store(nil)
			a.ready = false
		}
		if a.desk != nil {
			a.desk.remove(a)
			if a.desk.library == a {
				a.desk.library = nil
			}
		}
	})
	win.OnFocus(func() {
		// The menu bar is one bar for every window: the window that was just
		// focused says what its copy and scrolling items are.
		a.syncMenu()
		a.refresh(win)
	})
}

// showWindow returns the window this reader draws in, making one when the app
// is up and the window is gone. Before Run there is no window to make.
func (a *app) showWindow() *mygo.Window {
	if !a.ready {
		return nil
	}
	if a.win == nil || a.win.IsDestroyed() {
		a.newWindow()
	}
	return a.win
}

// refresh asks a window for a frame. Work started in a reader can land after
// its window has gone; a destroyed window takes the ask quietly.
func (a *app) refresh(win *mygo.Window) {
	if win == nil || win.IsDestroyed() {
		return
	}
	win.Invalidate()
}
