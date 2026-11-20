// quire reads EPUB files with MyGo's native UI: the window is built from
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
	"runtime/debug"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/laic7092/layx/render"
	"github.com/laix/quire/internal/acquire"
	"github.com/laix/quire/internal/appstate"
	"github.com/laix/quire/internal/book"
	"github.com/laix/quire/internal/reader"
	"github.com/laix/quire/internal/search"
	"github.com/laix/quire/internal/source"
	"github.com/laix/quire/internal/sourceconf"
	"github.com/laix/quire/internal/store"
	"github.com/laix/quire/locales"
)

// app is one window's reader state. The view builds that window's interface
// from it on the UI thread; background work publishes results through
// Window.Update. The state the app shares between windows lives on its desk.
type app struct {
	// desk is the app-wide state this window belongs to: the store, the reading
	// faces, the paper and the windows beside this one. It is nil only in a
	// reader built by hand, as the tests do.
	desk *desk
	win  *mygo.Window
	// winShared is the same window for background goroutines, which must not
	// read the UI thread's field. The UI thread writes both together.
	winShared atomic.Pointer[mygo.Window]
	// tasks owns work started for this window. It is canceled when the window
	// closes so a late search or download result cannot reach the next reader.
	tasks atomic.Pointer[taskScope]
	fonts *reader.Fonts
	// fontSans and fontSerif are the files the reading faces come from, resolved
	// at launch and opened with the first book (see loadFonts). fontErr is why
	// there is no face to open, and fontsLoading marks a load in flight so a
	// second one waits for it instead of parsing the faces again.
	fontSans, fontSerif string
	fontErr             error
	fontsLoading        bool

	// ready is set once this reader's window exists. Until then an open-file
	// event waits on the desk (see desk.pendingOpen).
	ready bool
	// opening is the path being read off the UI thread, so the same file
	// reported twice (a launch argument and OpenFile) opens once.
	opening string
	// services are the window's clipboard and redraw handles, kept from the
	// last frame for menu commands that run outside a build.
	services ui.Services
	// menuCopy is the Edit menu's page-copy item, and menuCopyOn the enabled
	// state it was last given: the view only touches the native menu when the
	// selection changes.
	menuCopy    *mygo.MenuItem
	menuAddNote *mygo.MenuItem
	menuCopyOn  bool
	// menuScroll is the View menu's scrolling item, kept so the menu follows a
	// change made in the display settings.
	menuScroll *mygo.MenuItem

	book  *book.Book
	doc   *render.Document
	docAt float32 // base font size doc was built with
	// bookWindow marks a window made to read one book. It opens on a sheet of
	// paper while the book is parsed and never shows the library; leaving the
	// book closes the window, and the library window behind it comes forward.
	bookWindow bool
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
	// layPaper is the sheet, ink and link colour the published layout's pages
	// were drawn with. They live in the page display lists, so a new paper costs
	// a repaint — a paint-only relayout — and never a rebuild of the book.
	layPaper pagePaper
	// theme is the paper being read on; "" follows the system's appearance
	// until the reader picks one.
	theme    reader.Theme
	themeSys bool
	status   string
	statusAt time.Time
	// notices are the short-lived messages waiting for the next frame, which is
	// the only place a toast can be added: a task that finishes off the UI
	// thread, or a launch that has not built a window yet, queues here.
	notices   []toastNotice
	file      string
	startPath string

	// Book sources: the shared configuration, registry and download manager,
	// and the source search sheet. These are filled from the desk in newApp.
	srcConf *sourceconf.Store
	sources *source.Registry
	acq     *acquire.Manager
	find    *sourceSheet

	// cover is the book's cover image; showCover keeps it in front of the book
	// until the reader moves on, or until coverIntro dissolves it into the place
	// a resumed book opens at.
	cover      render.ImageID
	showCover  bool
	coverIntro *coverIntro

	// Layout job bookkeeping. Only touched on the UI thread.
	layW, layH float32
	loading    bool
	job        int
	// layCancel stops the layout in flight when a newer one supersedes it: its
	// result names a sheet or a document the reader has already left, and
	// letting it run to the end costs a whole book of shaping for nothing.
	layCancel context.CancelFunc
	// sheetPending marks a resize waiting for the sheet to hold still, sheetAt is
	// when it last moved, and sheetPreviewed that this resize has already taken
	// the reader's place and cancelled the job it supersedes: the chapters on
	// screen are laid out at every size (buildPreview), and the whole book only
	// once the sheet has been still for resizeDelay (settleSheet).
	sheetPending   bool
	sheetPreviewed bool
	sheetAt        time.Time
	// preview is the chapters on screen laid out at the sheet being dragged to,
	// and previewW and previewH the size they were laid out at. It is drawn
	// until the whole book lands, and it is never named by a page number: see
	// preview.go.
	preview  *render.Layout
	previewW float32
	previewH float32
	failed   bool
	failW    float32
	failH    float32
	failSize float32

	body ui.Handle
	area ui.Handle
	// surface is the paper the reading view prints on: the column that covers the
	// whole window, under the bars and the panels. It is what says the sheet
	// reaches the window's edges, whatever the chrome over it is doing.
	surface ui.Handle
	// headBar is the reading bar, and headLeft and headRight the groups of
	// controls at its ends: the book's name in the middle is given the room
	// they leave it, measured on the committed frame. headPlace is the place
	// it was last given, so a frame that measures a different one asks for the
	// frame that draws it.
	headBar   ui.Handle
	headLeft  ui.Handle
	headRight ui.Handle
	headPlace titlePlace

	// sel is the text the reader dragged over, and selQuads its rectangles for
	// the current layout; selDragging marks a press that is still down,
	// selMoved that it left its press far enough to select, and selCopied how
	// many runes the last copy took (0 for none). pressX and pressY are where
	// that press began, which a released click turns the page from. Only the
	// UI thread touches them, and only the draw callback reads the rectangles.
	hoverLink   bool
	sel         reader.Selection
	selQuads    []render.SelectionQuad
	selDragging bool
	selMoved    bool
	selCopied   int
	pressX      float32
	pressY      float32
	// hitQuads holds the rectangles of each search hit, by hit index, for the
	// match veils: empty until a search has results on this layout.
	hitQuads [][]render.SelectionQuad
	// noteQuads holds the rectangles of every note's passage on this layout,
	// flattened: the draw callback fills them and never computes them.
	noteQuads []render.SelectionQuad
	// marksOn is the layout hitQuads, noteQuads and selQuads were built for. A
	// frame that draws another one — the published layout while a preview holds
	// another place — paints no veils rather than these in the wrong place.
	marksOn *render.Layout

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
	// pagePal is the reading palette captured for this window: the page, ink and
	// link colours passed explicitly to layout, painting and export. applyTheme
	// refreshes it; unlike reader.Current it is not process-wide mutable state.
	pagePal reader.Palette
	// chrome is the palette the window's own furniture is drawn in this frame:
	// the reading theme with a book open, the system's own with nothing open.
	chrome reader.Palette
	// winSrc is the appearance the window's own controls — the system's caption
	// buttons, or GTK's title buttons — were last drawn in, as handed to MyGo:
	// the desktop's while the window follows it, the paper's while a book is
	// open.
	winSrc mygo.ThemeSource

	// areaW is the width of the page area and boxW the width of the sheet as
	// the last frame laid them out: how many pages fit on the sheet, and so how
	// far a turn goes, follow them. boxH is the page area's height, which a
	// scroll viewport measures itself against.
	areaW float32
	boxW  float32
	boxH  float32
	// layScroll is the mode of the layout on screen; scrollY is where the
	// scrolling column's viewport starts in the flow's content coordinates, and
	// tileH the height of one of its tiles; scrollChapter is the chapter the
	// panels were last rebuilt for.
	layScroll bool
	// layGen counts the layouts published for the open book, so a place the
	// trail remembers can tell whether its scrolling offset still names the
	// same spot or the book has been laid out again since.
	layGen  int
	scrollY float32
	tileH   float32
	// tiles holds the scrolling column's tiles as canvases, drawn once for the
	// layout, paper and screen scale and composited after that (see tileCache).
	tiles tileCache
	// viewTop and devScale are where the column's viewport was drawn and at what
	// ratio, as of the last frame: a glide is over when the place it owes draws
	// the same picture as the place it stands at (see (*app).landed).
	viewTop  float32
	devScale float32
	// scrollVel is how fast the column is moving, in DIPs a second: a glide comes
	// up to the rate its debt implies over scrollRamp, and a frame moves what
	// that speed covers (see (*app).step).
	scrollVel float32
	// scrollRate is the same speed for a key that keeps asking: the arrow keys
	// set a rate rather than a place, so the column holds one speed while the key
	// repeats instead of gliding once per repeat (see (*app).scrollKey).
	scrollRate bool
	// A planned glide: the place it started from, when, and how long it takes. One
	// ask from a column standing still is a bell over these (see (*app).plan).
	scrollFrom    float32
	scrollStart   time.Time
	scrollDur     time.Duration
	scrollPlan    bool
	scrollChapter int
	// scrollGoal is the place the scrolls so far asked for, and scrollAt the time
	// the column last moved toward it: zero while it stands at scrollGoal. The
	// frames between the two cover the distance at one rate, whatever asked.
	scrollGoal float32
	scrollAt   time.Time
	// flingV is the speed a lifted finger left the column with, in DIPs a
	// second, and flingAt when the frames last took it: the flick's own motion,
	// decaying over flingTau (see (*app).stepFling). Zero while none is running.
	flingV  float32
	flingAt time.Time
	// frameBuilt tells the draw callback that this frame's build pass ran, and so
	// that it already stepped the column and named the frame for the log. The
	// frames of a glide and of a page turn build nothing: the draw callback steps
	// and asks for the next one itself, which is the whole of what a moving frame
	// costs once the view is out of the way.
	frameBuilt bool
	// posedAt is the frame whose time the column was last moved for: the engine
	// paints a frame again when its glyph atlas had to make room, and a step
	// taken twice would move the column twice. folioAt is when a glide last asked
	// for the build that keeps the folio on the place the column reached (see
	// folioEvery).
	posedAt time.Time
	folioAt time.Time
	// jumpLoc is a place to return to after a change of mode: switching between
	// pages and scrolling keeps the reader where it was, and only the way the
	// place is named changes. jumpAt marks it pending.
	jumpLoc render.Location
	jumpAt  bool
	// nav is the trail of places the reader jumped to — a table of contents
	// row, a search hit, a link, a footnote — which the folio's chevrons step
	// back and forward through. Page turns and scrolling are not on it.
	nav navHistory
	// turning is the page turn being shown, nil when the sheet is still, and
	// turnNext the direction a key still held asked for while that turn is on
	// its way: it waits for the slide to land rather than restarting it.
	turning  *turnState
	turnNext int
	// instant turns the page without sliding, as tests and reduced-motion
	// readers need.
	instant bool
	// chromeAway puts the reading bar and the folio away while the reader moves
	// down a scrolling column, or when the reader taps the middle of a phone's
	// page, and the frames between fade them (see chromeTarget). Nothing is laid
	// out from it: both are a layer over the sheet, never a row of it.
	chromeAway bool

	// phone marks a window on a phone's system (iOS or Android), where the
	// library drops the Mac source list for a tab bar, the reading bars take a
	// finger's sizes, and the middle of the page toggles the bars. It is set
	// once from the platform, so a test can turn it on in a desktop build.
	phone bool
	// pressAt, swipePage and dragScroll are the phone's finger gestures on the
	// sheet: when the press began, whether it has become a page swipe, and
	// whether it is dragging the scrolling column instead of selecting text.
	// selHold is whether this press earned its long press and so may mark text:
	// until it does, a finger scrolls, swipes or taps and selects nothing.
	// pressDead marks a quick drag that is none of those, and so does nothing
	// until the finger lifts.
	pressAt    time.Time
	swipePage  bool
	dragScroll bool
	selHold    bool
	pressDead  bool
	dragFrom   float32
	dragAt     time.Time
	dragY      float32
	dragV      float32
	// hapticPlayed is whether this press already played the long-press tick, so
	// one press plays at most one.
	hapticPlayed bool
	// selMenu is the phone's card of commands over a finished selection. A
	// phone has no secondary button, so the selection itself asks for it.
	selMenu bool
	// winBG and sysBar are what the native window was last told: the colour that
	// fills the safe-area edges and the style of the system's bar icons.
	winBG  string
	sysBar mygo.BarStyle

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
	// libTab is the phone library's bottom bar: home, the shelf, the shops or the
	// shelf's search. It chooses which screen shows; the library's own filter
	// narrows the shelf and is not a destination of its own.
	libTab int
	// searchFocus asks the search screen's field to take the keys on the frame
	// after its tab was chosen, once, so the keyboard comes up as it opens.
	searchFocus bool
	// panSheet is the phone's reading panel, opened as a sheet: mobile.Sheet
	// closes it by clearing this, and the panel follows (see panels). panSheetSide
	// is the panel the sheet was opened for, so a rebuild does not reopen what
	// the reader just closed.
	panSheet     bool
	panSheetSide string

	// goalBooks and goalDays are the reading goals the home screen measures
	// against: books a year, days a week. goalsOpen is the small panel that
	// sets them, and goalCopied when the share control last copied the record.
	goalBooks  int
	goalDays   int
	goalsOpen  bool
	goalCopied time.Time
	// shelfScroll keeps each cover row's place while the reader scrolls it.
	shelfScroll [2]ui.ScrollState
	// removeOpen shows the question asked before a book leaves the shelf, and
	// removePath and removeTitle name the book it is about: the dialog must
	// still know the book after the row it came from has gone.
	removeOpen  bool
	removePath  string
	removeTitle string

	// aaOpen is the display settings panel, and its open state. The ui fields
	// mirror the settings the sliders write, which work in float64.
	aaOpen bool
	// aaMore shows the rest of the settings under the theme cards, as Books
	// opens them from "Customize".
	aaMore    bool
	uiSize    float64
	uiLeading float64
	uiMargin  float64
	// covers caches decoded shelf covers, keyed by path and size. coversOrder
	// lists the same keys from least to most recently used; both are owned by
	// the UI thread, like the bitmaps themselves.
	covers      map[coverKey]*ui.Bitmap
	coversOrder []coverKey
}

// pageLabel is the folio's name for where the reader is: the cover, the page
// and how many the book has, as Books prints "161 of 418", or how far down the
// scrolling column the reader is.
func (a *app) pageLabel() string {
	if a.showCover {
		return locales.PageLabelCover()
	}
	if a.scrolling() {
		return a.scrollLabel()
	}
	if a.layout == nil {
		return locales.PageLabelNone()
	}
	return locales.PageLabelPages(a.page+1, a.layout.PageCount())
}

// progress is how much of the book has been read, from 0 to 1, for the footer:
// the page shown, or the place the scrolling column has reached.
func (a *app) progress() float64 {
	if a.scrolling() {
		return a.scrollProgress()
	}
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
	if a.scrolling() {
		i = a.currentChapter()
	}
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
	if n <= 0 {
		return page
	}
	step := a.pageStep()
	next := page + delta*step
	if next < 0 {
		next = 0
	}
	if next > n-1 {
		// The last spread starts at the book's last page: a book with an odd
		// number of pages ends on a single page, and keeping the pairs aligned
		// there would send a forward turn backwards.
		next = n - 1
	}
	return a.snapPage(max(next, 0))
}

func (a *app) turn(delta int) {
	// Turning is the reader moving on: whatever place a pending mode switch
	// meant to restore is stale now.
	a.jumpAt = false
	if a.scrolling() {
		// A scrolling book has no pages to turn: a turn is a screenful of
		// the column.
		a.scrollScreens(delta)
		return
	}
	if a.showCover {
		// The first turn puts the cover away and keeps the reading position.
		a.dismissCover()
		return
	}
	if a.layout == nil {
		return
	}
	// A slide already on the sheet is not restarted under the reader's eyes: a
	// key repeat would move the pages back to the start of the slide every
	// 30 ms, so the turn would never land and the pages would jump. The ask
	// waits for that slide to land instead.
	if a.turning != nil {
		a.turnNext = delta
		return
	}
	a.startTurn(delta, false)
}

// startTurn puts one turn on the sheet and moves the reading position to the
// spread it arrives at. flick is the short slide a held key's turns take: a
// repeat arrives every 30 ms or so, so a slide at the reading pace would still
// be under way when the next one was asked for, and the run would never settle
// on a page. At the short pace each turn lands between repeats, which is what
// makes holding the key flick through the book.
func (a *app) startTurn(delta int, flick bool) {
	a.turnNext = 0
	page := a.turnTarget(a.page, delta)
	if page == a.page {
		return
	}
	dur := turnDuration
	if a.opts.Turn == reader.TurnFade {
		dur = turnFadeDuration
	}
	if flick {
		dur = turnFlickDuration
	}
	a.turning = &turnState{from: a.page, to: page, dir: delta, at: a.clock(), dur: dur}
	if a.instant {
		a.turning = nil
	} else {
		perf.turnStart(a.page, page)
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

// gotoPage is the reader's jump to a page of a paged layout.
func (a *app) gotoPage(page int) {
	if a.layout == nil || a.scrolling() {
		return
	}
	a.page = a.snapPage(clampPage(page, a.layout.PageCount()))
	a.dismissCover()
	a.turning = nil
	a.afterPage()
}

// gotoLoc is the panels' jump: a table of contents row, a search hit, a link
// or a footnote, named by a document location. It leaves a stop on the trail
// the folio's chevrons step through, so the reader can come back to where the
// jump was made from.
func (a *app) gotoLoc(loc render.Location) {
	from, hadFrom := a.currentPlace()
	to, ok := a.moveTo(loc)
	if !hadFrom || !ok {
		return
	}
	a.nav.jump(from, to)
}

// moveTo moves the reader to a location without touching the trail, and names
// the place it landed at. In a scrolling layout it moves the column; in a paged
// one it turns to the page the location falls on, so the place it names is the
// top of that page.
func (a *app) moveTo(loc render.Location) (navPlace, bool) {
	if a.layout == nil {
		return navPlace{}, false
	}
	a.dismissCover()
	if a.scrolling() {
		if y, ok := a.layout.OffsetOf(loc); ok {
			// The column lands on the line the location falls on, exactly as
			// asked: the place to come back to is the location itself, which
			// survives a rebuild of the layout. Only a place clamped at the end
			// of the book is named by where the column actually stands.
			if resting := clampf(y, 0, a.maxScroll()); resting != y {
				a.scrollTo(resting)
				return a.currentPlace()
			}
			a.scrollTo(y)
			return navPlace{loc: loc, gen: a.layGen, scroll: y, col: true}, true
		}
		a.gotoChapter(loc.Chapter)
		return a.currentPlace()
	}
	if page, ok := a.layout.PageOf(loc); ok {
		a.gotoPage(page)
		return a.currentPlace()
	}
	a.gotoChapter(loc.Chapter)
	return a.currentPlace()
}

// gotoChapter jumps to the start of a chapter, which is where a table of
// contents row points when the location it carries cannot be resolved.
func (a *app) gotoChapter(chapter int) {
	if a.layout == nil {
		return
	}
	if a.scrolling() {
		if top, ok := a.layout.ChapterContentTop(chapter); ok {
			a.scrollTo(top)
		}
		return
	}
	if starts := a.layout.ChapterPages(); chapter >= 0 && chapter < len(starts) {
		a.gotoPage(starts[chapter])
	}
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
	prev := a.opts
	if o.Scroll != a.opts.Scroll {
		// The reading place survives the switch: remember where the reader is
		// before the layout that names it gives way.
		if loc, ok := a.currentLoc(); ok {
			a.jumpLoc, a.jumpAt = loc, true
		}
	}
	a.opts = o
	if a.desk != nil {
		// The window a reader opens next starts from the settings the last one
		// was given, as the store's preferences are the app's.
		a.desk.opts = o
	}
	a.optsAt = time.Now()
	a.sheetSettled()
	a.failed = false
	// A resize preview laid out for the settings before these names pages that
	// are about to be built again, and a job in flight shapes the same.
	a.dropPreview()
	a.cancelLayout()
	// A face the session has not read in yet — the serif one, after reading sans
	// — is opened now, off the UI thread, so the layout that follows is set in it.
	if prev.Font != o.Font {
		a.reloadFonts()
	}
	// The log names the setting that moved, before the layout it costs.
	perf.event("reading settings: %s (laying out again)", optionChanges(prev, o))
	// The sliders read these back: a change made with the keys has to move them
	// too.
	a.uiSize = float64(o.FontSize)
	a.uiLeading = float64(o.LineHeight)
	a.uiMargin = float64(o.Margin)
	a.savePrefs()
}

// settingsDelay is how long a settings change waits before it relayouts.
const settingsDelay = 160 * time.Millisecond

// frameScene names what the frame being built is doing, for the performance
// log: a slow line says "while scrolling" half a year later, when the frame
// number would mean nothing.
func (a *app) frameScene() string {
	switch {
	case a.turning != nil:
		return "turning a page"
	case a.gliding():
		return "scrolling"
	case a.loading:
		return "laying out"
	case a.book == nil && a.bookWindow:
		return "opening a book"
	case a.book == nil:
		return "in the library"
	}
	return "standing still"
}

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

// settleSheet asks for the frame that lays the whole book out again, once the
// window has stopped being dragged. The chapters on screen are already laid out
// at the new sheet (buildPreview), so the reader sees the reflow at once; this is
// the rest of the book, off the UI thread.
func (a *app) settleSheet(c *ui.Context) {
	if a.sheetAt.IsZero() {
		return
	}
	if wait := time.Until(a.sheetAt.Add(resizeDelay)); wait > 0 {
		c.After(wait)
	}
}

// sheetSettled forgets a resize that has been answered: either the sheet came
// back to the size the published layout already has, or the whole book has been
// asked for at the size the window stopped at.
func (a *app) sheetSettled() {
	a.sheetPending = false
	a.sheetPreviewed = false
	a.sheetAt = time.Time{}
}

// cancelLayout stops the layout in flight, if one is running. Its result names
// a sheet, a setting or a book the reader has already left, so it is dropped
// rather than published; cancelling is what stops it from shaping a whole book
// for nothing and from holding the machine the frames are drawn on.
func (a *app) cancelLayout() {
	if a.layCancel == nil {
		return
	}
	a.layCancel()
	a.layCancel = nil
	// The goroutine's publish finds itself superseded, and a cancelled job's
	// context error is not the reader's failure to report.
	a.job++
	a.loading = false
}

// setTurn changes how a page gives way to the next. It is not a layout
// setting: the pages on screen already hold both spreads the turn draws, so
// the choice costs nothing until the next turn.
func (a *app) setTurn(t reader.Turn) {
	t = reader.Options{Turn: t}.Normalized().Turn
	if t == a.opts.Turn {
		return
	}
	a.opts.Turn = t
	if a.desk != nil {
		a.desk.opts = a.opts
	}
	a.savePrefs()
}

// say puts a short-lived message in the folio: the answer to an action the
// reader just took, such as a bookmark or a saved comment. Errors stay in
// status and are drawn by the toolbar; this is the success path.
func (a *app) say(msg string) {
	a.status = msg
	a.statusAt = a.clock()
}

// transientStatus is the message a recent action left for the folio, or ""
// once it has had its time. The folio then goes back to the page number instead
// of keeping an answer to a question the reader has moved on from.
func (a *app) transientStatus() string {
	if a.failed || a.status == "" || a.statusAt.IsZero() {
		return ""
	}
	if a.clock().Sub(a.statusAt) > 2500*time.Millisecond {
		return ""
	}
	return a.status
}

// toastNotice is a message waiting for the next build, which is the only pass
// that can add it: a toast belongs to a window's frame.
type toastNotice struct {
	text, kind string
}

// notify queues the outcome of what the reader just did — a book removed from
// the shelf, a download that finished — to show as the toolkit's own toast at
// the foot of the window. It replaces the lines the home page used to keep for
// good: an answer to an action is not part of the library.
func (a *app) notify(text string) { a.notifyKind(text, "success") }

// notifyError queues a message that something failed: it shows in the danger
// colour and for a little longer than an answer.
func (a *app) notifyError(text string) { a.notifyKind(text, "error") }

func (a *app) notifyKind(text, kind string) {
	if text == "" {
		return
	}
	a.notices = append(a.notices, toastNotice{text: text, kind: kind})
	a.services.Invalidate()
}

// drainNotices hands the queued messages to the window's toasts. It runs at the
// top of a build, before the view: the toasts' viewport is built after it, so a
// message queued by the last frame's click shows in this one.
func (a *app) drainNotices(c *ui.Context) {
	if len(a.notices) == 0 {
		return
	}
	pending := a.notices
	a.notices = nil
	for _, n := range pending {
		timeout := 4 * time.Second
		if n.kind == "error" {
			timeout = 6 * time.Second
		}
		c.AddToast(ui.Toast{Title: n.text, Type: n.kind, Timeout: timeout})
	}
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
		s.Prefs.Scroll = o.Scroll
		s.Prefs.Turn = string(o.Turn)
		s.Prefs.Theme = string(theme)
	})
	a.st.SaveSoon(0)
}

// setTheme turns the page to another paper. Only the page's colours change: the
// layout keeps its line boxes and pages, so the next frame repaints the pages on
// screen and the chrome with it, whatever the book's size.
func (a *app) setTheme(t reader.Theme) {
	if t == a.theme && !a.themeSys {
		return
	}
	if a.desk != nil {
		// Every window turns the page together: the palette the pages are laid
		// out with is the process's, so two papers would repaint in each other's
		// colours.
		a.desk.setTheme(t, a)
		return
	}
	// A paper change is a repaint of the same pages, so the log can say that it
	// cost no layout at all.
	perf.event("paper %q -> %q (a repaint, no layout)", string(a.theme), string(t))
	a.theme = t
	a.themeSys = false
	a.savePrefs()
}

// optionChanges names what moved between two reading settings, for the
// performance log: "font size 18.0 -> 20.0, margins 48 -> 40".
func optionChanges(from, to reader.Options) string {
	var out []string
	num := func(name string, a, b float32) {
		if a != b {
			out = append(out, fmt.Sprintf("%s %.1f -> %.1f", name, a, b))
		}
	}
	num("font size", from.FontSize, to.FontSize)
	num("line height", from.LineHeight, to.LineHeight)
	num("margin", from.Margin, to.Margin)
	if from.Scroll != to.Scroll {
		out = append(out, perfMode(from.Scroll)+" -> "+perfMode(to.Scroll))
	}
	if from.Spread != to.Spread {
		out = append(out, fmt.Sprintf("spread %v -> %v", from.Spread, to.Spread))
	}
	if from.Justify != to.Justify {
		out = append(out, fmt.Sprintf("justify %v -> %v", from.Justify, to.Justify))
	}
	if from.Font != to.Font {
		out = append(out, fmt.Sprintf("face %q -> %q", from.Font, to.Font))
	}
	if from.Turn != to.Turn {
		out = append(out, fmt.Sprintf("turn %q -> %q", from.Turn, to.Turn))
	}
	if len(out) == 0 {
		return "no visible change"
	}
	return strings.Join(out, ", ")
}

// layoutSheet is the sheet a layout starts at, and whether the page area that
// decides it has been measured. The area's width is what says whether the sheet
// holds one page or two, so a layout started before it is known lays the whole
// book out at a width the reader never sees, and the frame that measures the
// area lays it out again. A book opens on that frame, so this is the one case
// where the layout waits instead of guessing.
func (a *app) layoutSheet(w, h float32) (sw, sh float32, measured bool) {
	sw, sh = a.sheetSize(w, h, a.opts)
	return sw, sh, a.areaW > 0
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
	sw, sh, measured := a.layoutSheet(w, h)
	palette := a.palette()
	paper := paperOf(palette)
	gate := chooseLayoutGate(layoutGateInput{
		width: w, height: h, sheetW: sw, sheetH: sh, measured: measured,
		bookReady: a.book != nil, fontsReady: a.fonts != nil,
		opts: opts, layOpts: a.layOpts, paper: paper, layPaper: a.layPaper,
		docAt: a.docAt, hasLayout: a.layout != nil, loading: a.loading,
		layW: a.layW, layH: a.layH,
		failed: a.failed, failW: a.failW, failH: a.failH, failFontSize: a.failSize,
	})
	switch gate {
	case layoutGateTooNarrow:
		// The page keeps its margins whatever the window does: below three of
		// them there is no line to lay out, so say so instead of failing the job.
		a.cancelLayout()
		a.dropPreview()
		a.loading = false
		a.failed, a.failW, a.failH, a.failSize = true, w, h, opts.FontSize
		a.status = locales.LayoutTooNarrow()
		return
	case layoutGateWaitMeasure:
		// The page area's width is what decides the sheet, and the frame that
		// shows the reading view for the first time is the one that carries it.
		// Waiting for it is what keeps the whole book from being laid out at a
		// guessed width and then laid out again at the real one.
		return
	case layoutGateNotReady, layoutGateStableFailure, layoutGateLoading:
		return
	case layoutGateCurrent:
		// The published layout is this very sheet, and its pages are on screen:
		// a preview built for a size the window has since come back to is stale.
		a.dropPreview()
		a.sheetSettled()
		return
	}

	// The sheet moved. While the window is being dragged the chapters on screen
	// are laid out for it (buildPreview) and drawn at once, and the whole book
	// waits for the sheet to hold still: a width change re-breaks every chapter
	// of the book, so a job per pixel of a drag would put the layout the reader
	// is waiting for behind dozens of stale ones — and the garbage of all of
	// them is what the frame loop was paying for.
	//
	// A tester owns no window and no job (a.win is nil), so it takes no resize
	// either: the preview and the waiting are driven from here, below.
	if a.win != nil {
		switch a.resizeStep(sw, sh) {
		case resizePreview:
			if !a.sheetPreviewed {
				a.sheetPreviewed = true
				a.beginResize(sw, sh, opts)
			} else if a.preview == nil || sw != a.previewW || sh != a.previewH {
				a.buildPreview(sw, sh, opts)
			}
			return
		}
	}

	if a.repaper(sw, sh, opts) {
		return
	}
	if a.win == nil {
		return
	}

	// Anything still running names a sheet or a document this job supersedes.
	a.cancelLayout()
	a.job++
	job := a.job
	a.loading = true
	a.layW, a.layH = sw, sh
	a.layOpts = opts
	a.layPaper = paper
	a.failed = false
	// The log gets the job as a pair of lines: the sheet it started with, and
	// what it cost when it landed.
	jobAt := time.Now()
	perf.layoutStart(sw, sh, opts.Scroll)

	bookPtr, fonts, win := a.book, a.fonts, a.win
	prev := a.layout
	doc, docAt := a.doc, a.docAt
	size := opts.FontSize
	// The job is cancellable: a newer size, setting or book makes its result
	// pointless, and stopping it hands the machine back to the frame loop and to
	// the job that matters.
	ctx, cancel := context.WithCancel(context.Background())
	a.layCancel = cancel
	go func() {
		var images map[render.ImageID][]byte
		rebuilt := false
		// What each part of the job cost, for the log: a rebuild of the book
		// and the layout proper. The search index is built later, on demand.
		var docSky, layoutSky time.Duration
		if doc == nil || docAt != size {
			// The reader paints links itself (ThemeLinks), so the paper's link
			// colour stays out of the document and a theme change never rebuilds
			// it.
			buildAt := time.Now()
			d, imgs, err := bookPtr.BuildWith(size, book.Options{ThemeLinks: true})
			docSky = time.Since(buildAt)
			if err != nil {
				win.Update(func() {
					defer a.refresh(win)
					perf.event("laying out failed after %s: %v", perfSpan(time.Since(jobAt)), err)
					if job != a.job {
						return
					}
					a.layCancel = nil
					a.loading = false
					a.failed, a.failW, a.failH, a.failSize = true, w, h, size
					a.dropPreview()
					a.status = locales.LayoutFailed(err.Error())
				})
				return
			}
			doc, docAt = d, size
			images = imgs
			rebuilt = true
		}
		// The same document can be laid out again by reusing what the previous
		// layout already shaped: a theme change only repaints, and a settings
		// change reuses the line boxes it can.
		var layout *render.Layout
		var err error
		layoutAt := time.Now()
		if doc != nil && prev != nil && prev.Document() == doc {
			layout, err = reader.RelayoutWithPalette(ctx, prev, fonts, palette, sw, sh, opts)
		} else {
			layout, err = reader.BuildLayoutWithPalette(ctx, doc, fonts, palette, sw, sh, opts)
		}
		layoutSky = time.Since(layoutAt)
		win.Update(func() {
			defer a.refresh(win)
			if job != a.job {
				perf.event("a layout was dropped: a newer one began while it ran (%s)",
					perfSpan(time.Since(jobAt)))
				return
			}
			a.layCancel = nil
			a.loading = false
			if err != nil {
				a.failed, a.failW, a.failH, a.failSize = true, w, h, size
				a.status = locales.LayoutFailed(err.Error())
				a.dropPreview()
				perf.event("laying out failed after %s: %v", perfSpan(time.Since(jobAt)), err)
				return
			}
			// Decoding belongs on the UI thread and happens once per build.
			if rebuilt && a.paint != nil {
				for id, data := range images {
					a.paint.SetImage(id, data)
				}
			}
			// The whole book is here: the chapters the preview laid out are part
			// of it now, at the size every page is numbered for.
			a.dropPreview()
			a.doc, a.docAt = doc, docAt
			a.layout = layout
			a.dropTiles()
			a.layGen++
			a.layScroll = opts.Scroll
			if a.jumpAt {
				a.jumpAt = false
				if a.layScroll {
					if y, ok := layout.OffsetOf(a.jumpLoc); ok {
						a.scrollY = y
					}
				} else if page, ok := layout.PageOf(a.jumpLoc); ok {
					a.page = page
				}
			}
			// Node ids belong to one layout: a selection from the old one would
			// point at nothing, and a slide in flight at a page of it.
			a.clearSelection()
			a.turning = nil
			a.status = ""
			a.tileH = 0
			if a.scrolling() {
				// The scroll flow is sliced into tiles no taller than a reader's
				// bitmap should be (see reader.ScrollTileHeight); the layout says
				// how tall the ones it made are.
				a.tileH = sh
				if board := layout.Tile(0); board != nil {
					a.tileH = board.Height()
				}
				a.page = 0
				a.scrollY = clampf(a.scrollY, 0, a.maxScroll())
				if a.pendingResume && a.pan != nil {
					a.pendingResume = false
					if loc, how := a.pan.ResumeAt(layout); how != "" {
						if y, ok := layout.OffsetOf(loc); ok {
							a.scrollY = y
							a.resumeNote = resumeScrollNote(layout, loc, how)
							a.say(a.resumeNote)
							a.beginCoverIntro()
						}
					}
				}
				a.scrollY = clampf(a.scrollY, 0, a.maxScroll())
				a.scrollChapter = -1
			} else {
				a.page = a.snapPage(min(a.page, layout.PageCount()-1))
				a.page = max(a.page, 0)
				if a.pendingResume && a.pan != nil {
					a.pendingResume = false
					if page, how := a.pan.Resume(layout); how != "" {
						a.page = a.snapPage(page)
						a.resumeNote = locales.ResumePage(page+1, resumeHow(how))
						a.say(a.resumeNote)
						a.beginCoverIntro()
					}
				}
			}
			// A new layout starts the column still: wherever the reader was is
			// the place it stands at now.
			a.scrollGoal, a.scrollAt = a.scrollY, time.Time{}
			if a.pan != nil {
				if rebuilt {
					// The index walks the document the layout was just built
					// from. Make it on the first query instead of paying for it
					// on every document rebuild.
					a.pan.SetIndexBuilder(func() (*search.Index, error) {
						return search.NewIndex(bookPtr, doc)
					})
				}
				if a.scrolling() {
					if loc, ok := a.currentLoc(); ok {
						a.scrollChapter = loc.Chapter
						a.pan.RefreshAt(a.book, layout, loc)
						a.recordScroll(layout, loc)
					} else {
						a.pan.Refresh(a.book, layout, 0)
					}
				} else {
					a.pan.Refresh(a.book, layout, a.page)
					a.pan.RecordPage(layout)
				}
			}
			a.scrollAhead()
			// Last, so the marks follow the new geometry and the new index: a
			// rebuild has just cleared the hits.
			a.refreshMarks()
			pages, scroll := layout.PageCount(), a.layScroll
			if scroll {
				pages = layout.TileCount()
			}
			perf.laidOut(time.Since(jobAt), docSky, 0, layoutSky, pages, scroll)
		})
	}()
}

// repaper prints the book on another paper when nothing but the paper changed,
// on the calling thread. The layout keeps every line and every page, so this
// costs a copy of the layout rather than a pass over the book, and the pages
// drawn later in this frame already wear the new colours: the page and the chrome
// turn over together, whatever the book's size. Nothing derived from the pages
// moves — the reader's place, its selection and the panels stand.
//
// It reports whether it repainted; anything else, a paper change included when
// the geometry moved with it, goes through the background job.
func (a *app) repaper(sw, sh float32, opts reader.Options) bool {
	if a.loading || a.layout == nil || a.doc == nil || a.layout.Document() != a.doc ||
		sw != a.layW || sh != a.layH || !opts.Same(a.layOpts) {
		return false
	}
	a.layout = reader.RecolorWithPalette(a.layout, a.palette())
	a.layGen++
	a.layPaper = paperOf(a.palette())
	return true
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
			Title:   locales.OpenDialogTitle(),
			Filters: []mygo.FileFilter{{Name: locales.BookFiles(), Extensions: []string{"epub", "txt"}}},
		})
		if err != nil {
			win.Update(func() {
				defer a.refresh(win)
				a.status = locales.OpenFailed(err.Error())
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

// chooseDir shows the directory dialog, as chooseFile does for a book. The
// folder is imported: its books join the shelf, and the ones already there stay.
func (a *app) chooseDir() {
	win := a.showWindow()
	if win == nil || a.pan == nil {
		return
	}
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:    win,
			Title:     locales.ChooseFolderTitle(),
			Directory: true,
		})
		if err != nil {
			win.Update(func() {
				defer a.refresh(win)
				a.pan.Shelf.Status = locales.ChooseFolderFailed(err.Error())
			})
			return
		}
		if len(paths) == 0 {
			return
		}
		dir := paths[0]
		win.Update(func() {
			defer a.refresh(win)
			a.pan.ImportDir(dir)
			if a.desk != nil {
				// A folder imported in one window joins every shelf.
				a.desk.refreshShelves(a)
			}
		})
	}()
}

// loadFonts opens the faces a book is set in, for the face setting: the
// system's sans CJK face always, its serif one only when the book is set in it.
// It runs off the UI thread, so the launch of a book never waits for the parse,
// and its result is published through Window.Update.
func (a *app) loadFonts(face string) (*reader.Fonts, error) {
	if a.fontErr != nil {
		return nil, a.fontErr
	}
	if a.desk != nil {
		// The faces are the app's: the first window parses them and every
		// other window reads in them without a second parse.
		return a.desk.loadFonts(face, a.fontSans, a.fontSerif)
	}
	serif := a.fontSerif
	if face != reader.FontSerif {
		serif = ""
	}
	fonts, err := reader.OpenFonts(a.fontSans, serif)
	if err != nil {
		return nil, err
	}
	log.Printf("quire: font %s, serif %q", fonts.Path, fonts.SerifPath)
	// Parsing the faces leaves the tables the layout keeps and, beside them, the
	// copies and scratch the parse was made with. Hand that garbage back to the
	// system now, while no page waits for it, rather than at the next collection:
	// it is most of what a freshly opened book adds to the process.
	debug.FreeOSMemory()
	return fonts, nil
}

// reloadFonts opens the faces the current setting needs when the session has
// none yet — the book was opened before they landed, or the reader switched to
// the serif face after reading sans. It runs on the UI thread and does the
// reading on a goroutine, like a book's own open.
func (a *app) reloadFonts() {
	if a.fontsLoading || a.fontErr != nil || a.book == nil {
		return
	}
	face := a.opts.Font
	if a.fonts != nil && (face != reader.FontSerif || a.fonts.HasSerif()) {
		return
	}
	win := a.win
	if win == nil {
		return
	}
	a.fontsLoading = true
	go func() {
		fonts, err := a.loadFonts(face)
		win.Update(func() {
			defer a.refresh(win)
			a.fontsLoading = false
			if err != nil {
				log.Println(err)
				a.status = err.Error()
				return
			}
			a.fonts = fonts
			// The pages on screen were set in the face before this one, and the
			// painter holds their images: the book is built and laid out again.
			a.paint = reader.NewUIPainterWithPalette(nil, fonts, a.palette())
			a.cancelLayout()
			a.dropPreview()
			a.doc = nil
			a.layW, a.layH = 0, 0
			// A setting that moved while the faces loaded asks for them again.
			if a.opts.Font == reader.FontSerif && a.fontSerif != "" && !a.fonts.HasSerif() {
				a.reloadFonts()
			}
		})
	}()
}

// openAsync parses a book off the UI thread and publishes it through
// Window.Update. fonts is the set the session has (nil before the first book),
// and load asks for the faces the book is set in to be opened first.
func (a *app) openAsync(win *mygo.Window, path string, fonts *reader.Fonts, face string, load bool) {
	if load {
		loaded, err := a.loadFonts(face)
		if err != nil {
			log.Println(err)
			win.Update(func() {
				defer a.refresh(win)
				a.fontsLoading = false
				if a.opening == path {
					a.opening = ""
				}
				a.status = err.Error()
			})
			return
		}
		fonts = loaded
	}
	parseAt := time.Now()
	perf.event("opening %s", path)
	b, err := book.Open(path)
	parseSky := time.Since(parseAt)
	if err != nil {
		win.Update(func() {
			defer a.refresh(win)
			a.fontsLoading = false
			perf.event("could not open %s after %s: %v", path, perfSpan(parseSky), err)
			if a.opening == path {
				a.opening = ""
			}
			a.status = locales.ReadBookFailed(err.Error())
		})
		return
	}
	perf.event("opened %q (%d chapters) in %s", b.Title, len(b.Chapters), perfSpan(parseSky))
	win.Update(func() {
		defer a.refresh(win)
		a.fontsLoading = false
		if a.opening == path {
			a.opening = ""
		}
		if fonts != nil && (fonts != a.fonts || a.paint == nil) {
			// The faces this session read with, opened with the book that needed
			// them: from here on a page can be laid out and drawn. A window made
			// after the parse already has them but not their painter.
			a.fonts = fonts
			a.paint = reader.NewUIPainterWithPalette(nil, fonts, a.palette())
			a.doc = nil
		}
		a.cancelLayout() // drop any layout still in flight
		a.failed = false
		a.book = b
		a.cover = b.Cover
		if a.pan != nil {
			// The panels follow the book as it opens, before anything is drawn.
			a.pan.Opened(b, path)
			a.pan.Refresh(b, nil, 0)
			if a.desk != nil {
				// The book just opened joins the shelves of the other,
				// still-library windows.
				a.desk.refreshShelves(a)
			}
		}
		// The book opens on its cover. A book with a place saved in the store
		// dissolves from it into that place once the layout lands: the resume
		// below starts the dissolve (beginCoverIntro).
		a.showCover = b.HasCover
		a.coverIntro = nil
		if a.paint != nil {
			a.paint.ClearImages()
		}
		a.dropTiles()
		a.doc = nil
		a.docAt = 0
		a.layout = nil
		a.preview = nil
		a.page = 0
		a.layW, a.layH = 0, 0
		a.sheetSettled()
		// The sheet, the box and the page area are all of this book's reading
		// view: none of them is measured yet, and the layout of the new book
		// waits for the first frame to measure them rather than reusing a
		// window's worth of geometry from the book before it.
		a.boxW, a.boxH, a.areaW = 0, 0, 0
		a.turning = nil
		a.clearSelection()
		a.hitQuads = nil
		a.noteQuads = nil
		a.file = path
		a.status = locales.OpenedBook(b.Title)
		a.pendingResume = true
		a.resumeNote = ""
		// The trail of jumps belongs to the book that was open.
		a.nav.reset()
		a.layScroll = false
		a.scrollY, a.tileH, a.scrollChapter = 0, 0, -1
		a.scrollGoal, a.scrollAt = 0, time.Time{}
		// The place a pending mode switch meant to restore names a node of the
		// book before this one; this book resumes from the store instead.
		a.jumpAt = false
		a.hoverLink = false
		title := appName
		if a.book != nil {
			title = locales.WindowTitle(a.book.Title)
		}
		win.SetTitle(title)
		// The proxy icon in the title bar, the Window menu and the Dock's
		// recent list all follow the file being read.
		win.SetRepresentedFile(path)
		_ = mygo.App.AddRecentDocument(path)
	})
}

// appName is the reader's name: the window's title while no book is open, and
// what the About box and the menus call it. Its messages are in the catalogs.
const appName = "Quire"

// windowOptions are the reader's window. Every platform gets the Books-style
// shell: no system title bar, the view runs to the top edge, and its own bars
// drag the window and leave the system's buttons their room. Only where those
// buttons go is the platform's own business — traffic lights at the left on
// macOS, caption buttons at the right on Windows, whichever side the desktop
// configures on Linux — and c.TitleBar() reports it.
func windowOptions(view func(*ui.Context), stateKey string) mygo.WindowOptions {
	o := mygo.WindowOptions{
		Title:         appName,
		Width:         787,
		Height:        590,
		MinWidth:      640,
		MinHeight:     420,
		StateKey:      stateKey,
		TitleBarStyle: mygo.TitleBarHidden,
		// The window controls fill the reader's own bar, so that their
		// glyphs centre on the toolbar's: Windows centres them in this
		// height, and Linux's GTK buttons do too.
		TitleBarHeight: headerHeight,
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

// menuAction runs a menu command against the window the reader is looking at
// and asks that window for a frame. The same bar serves every window, and the
// app keeps running with all of them closed on macOS, so the command makes a
// window when none is left.
func (a *app) menuAction(fn func(*app)) {
	target := a
	if a.desk != nil {
		target = a.desk.menuApp()
	}
	if target == nil {
		return
	}
	fn(target)
	target.refresh(target.win)
}

// menuBar is the macOS menu bar. The other platforms keep the window's own
// shortcuts: a native UI window has no menu bar there. The labels follow the
// language the app shows, so the bar is made again when it changes.
//
// The bar is the app's, and every command runs against the window the reader is
// looking at: the closures below ask the desk for that window rather than
// holding the one that happened to install the menu.
func (a *app) menuBar() *mygo.Menu {
	// onActive is the command for a window the reader is looking at now, or a
	// new one when the last window was closed.
	onActive := func(fn func(*app)) func(*mygo.MenuItem, *mygo.Window) {
		return func(*mygo.MenuItem, *mygo.Window) { a.menuAction(fn) }
	}
	copySelection := &mygo.MenuItem{
		ID:    "copySelection",
		Label: locales.MenuCopySelection(),
		// The first frame keeps this in step with the selection.
		Disabled: len(a.selQuads) == 0,
		// No accelerator: ⌘C must reach the view for the page selection and
		// the focused search field. An enabled menu item would take the key
		// first and copy neither of them.
		Click: onActive(func(t *app) { t.copyPageText() }),
	}
	a.menuCopy = copySelection
	a.menuCopyOn = len(a.selQuads) > 0
	addNote := &mygo.MenuItem{
		ID:       "addNote",
		Label:    locales.AddNote(),
		Disabled: len(a.selQuads) == 0,
		Click:    onActive(func(t *app) { t.addNoteFromSelection() }),
	}
	a.menuAddNote = addNote
	scroll := &mygo.MenuItem{
		Label:   locales.MenuScrollReading(),
		Type:    mygo.MenuItemCheckbox,
		Checked: a.opts.Scroll,
		Click: onActive(func(t *app) {
			o := t.opts
			o.Scroll = !o.Scroll
			t.setOptions(o)
		}),
	}
	a.menuScroll = scroll
	show := func(sidebar string) func(*mygo.MenuItem, *mygo.Window) {
		return onActive(func(t *app) {
			if t.pan != nil {
				t.pan.Show(sidebar)
			}
		})
	}
	menu := mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu},
		{Label: locales.MenuFile(), Submenu: []*mygo.MenuItem{
			{Label: locales.MenuOpen(), Accelerator: "CmdOrCtrl+O", Click: onActive((*app).chooseFile)},
			{Role: mygo.RoleOpenRecent},
			mygo.Separator(),
			{Role: mygo.RoleClose},
		}},
		{Label: locales.MenuEdit(), Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleUndo},
			{Role: mygo.RoleRedo},
			mygo.Separator(),
			{Role: mygo.RoleCut},
			{Role: mygo.RolePaste},
			{Role: mygo.RoleSelectAll},
			mygo.Separator(),
			copySelection,
			addNote,
		}},
		{Label: locales.MenuView(), Submenu: []*mygo.MenuItem{
			{Label: locales.MenuSidebar(), Accelerator: "CmdOrCtrl+B", Click: onActive((*app).toggleSidebar)},
			{Label: locales.MenuSortByTitle(), Type: mygo.MenuItemCheckbox, Checked: a.sortByTitle, Click: onActive(func(t *app) {
				t.sortByTitle = !t.sortByTitle
			})},
			scroll,
			{Label: locales.MenuContents(), Accelerator: "CmdOrCtrl+T", Click: show(appstate.SidebarToc)},
			{Label: locales.MenuSearch(), Accelerator: "CmdOrCtrl+F", Click: show(appstate.SidebarSearch)},
			{Label: locales.PanelBookmarks(), Accelerator: "CmdOrCtrl+Shift+B", Click: show(appstate.SidebarBookmarks)},
			{Label: locales.PanelNotes(), Accelerator: "CmdOrCtrl+Shift+N", Click: show(appstate.SidebarNotes)},
			{Label: locales.MenuDisplaySettings(), Accelerator: "CmdOrCtrl+,", Click: onActive(func(t *app) { t.aaOpen = true })},
			mygo.Separator(),
			{Role: mygo.RoleToggleFullScreen},
		}},
		{Role: mygo.RoleWindowMenu},
		{Role: mygo.RoleHelp},
	})
	if a.desk != nil {
		// One bar, shared by every window: the items live on the desk so the
		// focused window can keep them in step whichever window installed it.
		a.desk.menuOwner = a
		a.desk.menuCopy, a.desk.menuAddNote, a.desk.menuScroll = copySelection, addNote, scroll
		a.desk.menuCopyOn = len(a.selQuads) > 0
	}
	return menu
}

func main() {
	fontFlag := flag.String("font", "", "path to a .ttf/.otf/.ttc font (default: $LAYX_FONT or a system CJK font)")
	shelfFlag := flag.String("shelf", "", "directory of EPUB/TXT files to import into the shelf")
	serifFlag := flag.String("serif", "", "path to the serif face (default: $LAYX_SERIF or a system serif)")
	stateFlag := flag.String("state", "", "state file (default: the config directory)")
	perfFlag := flag.Bool("perf", false, "write a human-readable performance log (also $LAYX_PERF)")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: quire [-font path] [-shelf dir] [-perf] [book.epub]\n")
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
	recovered := ""
	if statePath != "" {
		s, err := store.Open(statePath)
		if err != nil {
			log.Println(err)
		} else {
			st = s
			recovered = s.Recovered()
		}
	}

	// The desk owns what every window shares: the store, the reading faces, the
	// paper, the goals and the menu bar.
	d := &desk{
		st:        st,
		opts:      reader.DefaultOptions(),
		theme:     reader.ThemeQuiet,
		goalBooks: defaultGoalBooks,
		goalDays:  defaultGoalDays,
	}

	// Book sources are configured and registered before any window exists.
	srcConfPath := ""
	if statePath != "" {
		srcConfPath = filepath.Join(filepath.Dir(statePath), "sources.json")
	} else if cfgDir, err := os.UserConfigDir(); err == nil {
		srcConfPath = filepath.Join(cfgDir, "quire", "sources.json")
	}
	if srcConfPath != "" {
		d.initSources(srcConfPath)
	}
	if st != nil {
		prefs := st.State().Prefs
		if prefs.GoalBooks > 0 {
			d.goalBooks = prefs.GoalBooks
		}
		if prefs.GoalDays > 0 {
			d.goalDays = prefs.GoalDays
		}
		d.opts = reader.Options{
			FontSize:   prefs.FontSize,
			LineHeight: prefs.LineHeight,
			Font:       prefs.Font,
			Justify:    prefs.Justify,
			Margin:     prefs.Margin,
			Spread:     prefs.Spread,
			Scroll:     prefs.Scroll,
			Turn:       reader.Turn(prefs.Turn),
		}.Normalized()
		if prefs.Theme != "" {
			d.theme = reader.ParseTheme(prefs.Theme)
			d.themeSys = false
		}
	}
	// The reading faces are named here, not read: a parsed system CJK face costs
	// tens of megabytes, and the shelf alone is set in the window's own Inter.
	// The first book opens them, in the faces its setting reads in (see
	// (*desk).loadFonts).
	fontPath, fontErr := reader.FindFont(*fontFlag)
	d.fontSans, d.fontErr = fontPath, fontErr
	d.fontSerif = reader.FindSerif(*serifFlag)

	// The first window carries the launch: the book named on the command line,
	// the recovered state's notice, and the app's own status.
	first := d.newApp()
	d.add(first)
	d.library = first
	first.startPath = flag.Arg(0)
	if recovered != "" {
		first.notify(locales.StateRecovered(recovered))
		log.Printf("store: damaged state backed up to %s", recovered)
	}
	if fontErr != nil {
		first.status = fontErr.Error()
		log.Println(fontErr)
	}
	if st != nil {
		prefs := st.State().Prefs
		if prefs.FontSize == 0 && prefs.Theme == "" {
			// Record the defaults once, so the state file shows the shape.
			first.savePrefs()
		}
	}
	// The reader floats its panels over the page, so a panel remembered from
	// the previous session waits for the book that would show it; the library
	// has its own source list and never floats one.
	if first.startPath == "" && first.pan.Showing() {
		first.pan.Show(appstate.SidebarNone)
	}

	// The performance log, if the reader asked for one: it names the machine and
	// the settings first, then what every frame and every layout cost. It takes
	// the standard logger with it, so MyGo's own frame lines land in the file.
	perfStart(perfOptions{
		flag:      *perfFlag,
		env:       os.Getenv("LAYX_PERF"),
		file:      os.Getenv("LAYX_PERF_FILE"),
		statePath: statePath,
		reading:   perfReading(d.opts, d.theme),
		font:      fontPath,
	})
	// A file opened with the app — from the Finder, the Dock, Open Recent or a
	// second instance — may arrive before the window exists; the desk holds it
	// until then and chooses the window it belongs in.
	mygo.App.OnOpenFile(d.openExternal)
	// A second instance only brings the reader forward; MyGo passes its files
	// to OnOpenFile above.
	mygo.App.OnSecondInstance(func([]string, string) {
		a := d.focused()
		if a == nil {
			a = d.focusOrOpen()
		}
		if a == nil {
			return
		}
		a.focusWindow()
	})
	// The app speaks the languages of its catalogs, and MyGo's own strings,
	// including the menu roles', follow: importing the catalogs registers them
	// with package i18n, which is what makes those the app's languages.
	//
	// The menu bar and the window titles are set once, so they are set again
	// when the language the app shows changes: switching it in System Settings
	// relabels them without a restart.
	mygo.App.OnLocaleChanged(func(string) {
		if runtime.GOOS == "darwin" {
			// Rebuild the bar with the app that installed it, whether or not
			// its window is still open: the bar is visible with no window at
			// all, and it must change language with the app.
			a := d.menuOwner
			if a == nil {
				a = d.focusOrOpen()
			}
			if a != nil {
				mygo.App.SetMenu(a.menuBar())
			}
		}
		for _, a := range d.wins {
			if !a.live() {
				continue
			}
			if a.book != nil {
				a.win.SetTitle(locales.WindowTitle(a.book.Title))
			} else {
				a.win.SetTitle(appName)
			}
			a.win.Invalidate()
		}
	})
	if runtime.GOOS == "darwin" {
		// Closing the last window leaves the app in the Dock, as every Mac app;
		// clicking the icon brings back a window with the reader as it was.
		mygo.App.OnWindowAllClosed(func() {})
		mygo.App.OnActivate(func(hasVisibleWindows bool) {
			if hasVisibleWindows {
				return
			}
			if a := d.focusOrOpen(); a != nil && a.live() {
				a.win.Show()
				a.focusWindow()
			}
		})
		mygo.App.SetMenu(first.menuBar())
	}

	mygo.App.WhenReady(func() {
		d.ready = true
		first.newWindow()
		// The shelf is the books the store remembers: it is listed from there on
		// every launch, so a book opened once is still there. The flag imports a
		// folder into that list first, and the listing shows the whole of it.
		if *shelfFlag != "" {
			first.pan.ImportDir(*shelfFlag)
		} else {
			first.pan.ReloadShelf()
		}
		if d.pendingOpen == "" && first.startPath != "" {
			if abs, err := filepath.Abs(first.startPath); err == nil {
				first.startPath = abs
			}
			d.pendingOpen = first.startPath
		}
		if d.pendingOpen != "" {
			path := d.pendingOpen
			d.pendingOpen = ""
			d.openExternal(path)
		}
	})
	runErr := mygo.App.Run()
	// The session's totals go last, while the log can still be written.
	perf.close()
	if runErr != nil {
		if st != nil {
			_ = st.Close()
		}
		log.Fatal(runErr)
	}
	if st != nil {
		if err := st.Close(); err != nil {
			log.Println(err)
		}
	}
}
