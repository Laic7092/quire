package main

import (
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/appstate"
	"github.com/laix/layx-reader/internal/icons"
	"github.com/laix/layx-reader/internal/library"
	"github.com/laix/layx-reader/internal/reader"
	"github.com/laix/layx-reader/internal/store"
	"github.com/laix/layx-reader/internal/widgets"
	"github.com/laix/layx-reader/locales"
	"github.com/laix/layx/render"
)

// Window metrics, in DIPs, as the two Books windows have them: a shallow bar
// with the book's name in it, a folio of small type under the text, and a
// library whose sidebar is a source list.
const (
	headerHeight = 46
	footerHeight = 44
	pageMaxWidth = 720
	pagePadding  = 21
	edgeWidth    = 34

	// The title bar's clusters, measured off Books' own toolbar: a 26 DIP
	// capsule around three 27 DIP glyph cells, and the separate round
	// bookmark control after it.
	toolbarHeight = 26
	toolbarCell   = 27

	// The reading panels float over the page in a card at the left, as Books
	// floats its table of contents: they take their width from the page's
	// margin, never from the text, so opening one does not reflow the book.
	panelWidth  = 300
	panelMargin = 12
	panelTop    = 8

	// The library's rows are a Mac source list's, so they keep their size
	// whatever the window: a 26 DIP pitch, a 24 DIP pill and a 13 DIP label.
	// Everything that follows the window's width is derived in homeMetricsFor.
	libraryRowHeight = 24
	libraryRowPitch  = 26
	// librarySideTop is the strip at the head of the source list: the room the
	// traffic lights take on macOS, and the same room where the desktop draws
	// no title bar, so the rows sit where a Mac's would.
	librarySideTop = 49
	// homeTopHeight is the strip above the title, holding the week's ring.
	homeTopHeight = 44
	// coverAspect is a cover's height over its width, about a trade
	// paperback's.
	coverAspect = 1.5625
)

// homeMetrics are the library page's measures for one window, in DIPs. They are
// ratios of the width clamped to sane ends rather than the numbers of any one
// screenshot: an 818 DIP window (Books' own home, where the prototype was
// measured) lands on the same frame, and a wider window gets a wider source
// list, roomier margins and larger covers — so many more books, not stretched
// ones. The type does not scale: a Mac's source list and headings keep their
// point sizes at every window size.
type homeMetrics struct {
	// side is the source list's width; padding is the page's inset at the left
	// and right.
	side, padding float32
	// coverW, coverH and coverGap size one cover and the space beside it; the
	// row scrolls sideways, so a window shows as many as fit.
	coverW, coverH, coverGap float32
	// firstGap is the space under the title before the first band, bandGap the
	// space between a band's covers and the next band, and goalsGap the space
	// before the reading goals.
	firstGap, bandGap, goalsGap float32
	// ring is the diameter of the year's ring in the goals card.
	ring float32
}

// homeMetricsFor measures the library for a window w DIPs wide, with or without
// the source list. The ratios come from Books' home; the clamps keep a small
// window usable and a large one from turning the covers into posters.
func homeMetricsFor(w float32, sidebar bool) homeMetrics {
	m := homeMetrics{side: clampf(w*0.196, 160, 224)}
	content := w
	if sidebar {
		content -= m.side
	} else {
		m.side = 0
	}
	m.padding = clampf(content*0.0486, 20, 40)
	usable := content - m.padding
	m.coverW = clampf(usable*0.128, 76, 140)
	m.coverH = m.coverW * coverAspect
	m.coverGap = m.coverW * 0.275
	m.firstGap = m.coverH * 0.19
	m.bandGap = m.coverH * 0.40
	m.goalsGap = m.coverH * 0.26
	m.ring = clampf(m.coverH*0.96, 104, 160)
	return m
}

// clampf keeps v between lo and hi.
func clampf(v, lo, hi float32) float32 { return min(max(v, lo), hi) }

// serifHead is the family the library's own headings are set in: Books prints
// "Home" and the shops' titles in a serif, unlike everything around them. The
// reader registers the face under this name when it opens its fonts, and a
// system without it falls back to the sans.
const serifHead = "book-serif, serif"

// view builds the window: the library, or the book being read with its tools at
// the top and its folio at the foot.
func (a *app) view(c *ui.Context) {
	// The performance log measures the build pass from here to the end of the
	// function: MyGo's own layout and commit come after it, and its draw
	// callback after that.
	perf.frameBegin()
	defer perf.buildDone()
	// Menu commands run outside the build and still need the clipboard and a
	// way to ask for a frame.
	a.services = c.Services()
	// The draw callback of this frame is the one the box built here keeps: it
	// must not step the column a second time (see drawSheet).
	a.frameBuilt = true
	a.syncMenu()
	a.keys(c)
	t := a.applyTheme(c)
	a.settle(c)
	if a.turning != nil {
		// The slide is on its way, or it has landed and a key still held asked
		// for the next pages while it was under way. A turn's place is a function
		// of the clock, so the frames that show it ask from the draw callback and
		// need no view built (drawSheet); this pass is where a landed turn is
		// settled and the next one starts, from the spread the last one arrived
		// at, so the run of turns is one page after another and never a restart.
		if _, moving := a.turnProgress(); !moving {
			a.turning = nil
			perf.turnDone()
			if delta := a.turnNext; delta != 0 {
				a.startTurn(delta, true)
			}
		}
	}
	if a.gliding() {
		// A wheel's notches glide: cover what this frame's share of the distance
		// is, so that the box the sheet draws in is placed from a column that has
		// already moved. The frames after this one paint without building (see
		// drawSheet), which is where a glide spends most of its time.
		a.stepScroll()
	}
	perf.frameScene(a.frameScene())

	ui.Column(c).Fill().Background(t.Background).Children(func() {
		if a.book == nil {
			a.library(c, t)
			return
		}
		a.header(c, t)
		// The page and the floating panels share one box: the panels are
		// absolute, so they lie over the reading area instead of taking room
		// from it, and the book keeps its measure while one is open.
		ui.Box(c).Grow(1).MinWidth(0).FillWidth().Children(func() {
			a.reader(c, t)
			a.panels(c)
		})
	})
}

// keys installs the window's shortcuts. They are declared before the widgets
// that could take the same keys, so a focused text field still wins where the
// framework routes keys to it first.
func (a *app) keys(c *ui.Context) {
	if c.Shortcut(ui.Cmd, ui.KeyB) {
		a.toggleSidebar()
	}
	if a.pan != nil {
		if c.Shortcut(ui.Cmd, ui.KeyF) {
			a.pan.Show(appstate.SidebarSearch)
		}
		if c.Shortcut(ui.Cmd, ui.KeyT) {
			a.pan.Show(appstate.SidebarToc)
		}
	}
	if c.Shortcut(0, ui.KeyEscape) {
		switch {
		case a.aaOpen:
			a.aaOpen = false
		case a.showCover:
			a.showCover = false
		case a.pan != nil && a.pan.Showing():
			a.pan.Show(appstate.SidebarNone)
		case len(a.selQuads) > 0:
			a.clearSelection()
		case a.libSearch:
			a.libSearch = false
			a.libQuery = ""
		default:
			// Nothing left to close: the way back to the library, as Books has
			// it.
			a.closeBook()
		}
	}
	if c.Shortcut(ui.Cmd, ui.KeyO) {
		a.chooseFile()
	}
	if a.book == nil {
		return
	}
	if c.Shortcut(ui.Cmd, ui.KeyD) {
		a.toggleBookmark()
	}
	if c.Shortcut(ui.Cmd, ui.KeyC) {
		// The selection stays after a copy, as in every other reader.
		a.copySelection(c)
	}
	if c.Shortcut(0, ui.KeyLeft) || c.Shortcut(0, ui.KeyPageUp) {
		a.turn(-1)
	}
	if c.Shortcut(0, ui.KeyRight) || c.Shortcut(0, ui.KeyPageDown) || c.Shortcut(0, ui.KeySpace) {
		a.turn(1)
	}
	if a.scrolling() {
		// A column moves by lines with the arrows, by a screen with the page
		// keys, and to its ends with Home and End; a turn is a screenful.
		if c.Shortcut(0, ui.KeyDown) {
			a.scrollLines(3)
		}
		if c.Shortcut(0, ui.KeyUp) {
			a.scrollLines(-3)
		}
		if c.Shortcut(0, ui.KeyHome) {
			a.scrollTo(0)
		}
		if c.Shortcut(0, ui.KeyEnd) {
			a.scrollTo(a.maxScroll())
		}
	} else {
		if c.Shortcut(0, ui.KeyHome) {
			a.gotoPage(0)
		}
		if c.Shortcut(0, ui.KeyEnd) && a.layout != nil {
			a.gotoPage(a.layout.PageCount() - 1)
		}
	}
	if c.Shortcut(ui.Cmd, ui.KeyMinus) || c.Shortcut(0, ui.KeyMinus) {
		a.changeFont(-1)
	}
	if c.Shortcut(ui.Cmd, ui.KeyEqual) || c.Shortcut(0, ui.KeyEqual) {
		a.changeFont(1)
	}
}

// syncMenu keeps the menu bar's state in step with the view. It costs nothing
// when no menu was installed, as on Linux and Windows.
func (a *app) syncMenu() {
	if a.menuCopy == nil {
		return
	}
	if on := len(a.selQuads) > 0; on != a.menuCopyOn {
		a.menuCopyOn = on
		a.menuCopy.SetEnabled(on)
		if a.menuAddNote != nil {
			a.menuAddNote.SetEnabled(on)
		}
	}
	if a.menuScroll != nil && a.menuScroll.IsChecked() != a.opts.Scroll {
		a.menuScroll.SetChecked(a.opts.Scroll)
	}
}

// header is the reader's bar: the panels and the way back at the left, the
// book's name in the middle, and the display settings at the right, as Books
// lays it out. It has no fill of its own — the paper runs under it.
//
// In a window with a hidden title bar this bar is the title bar: it leaves the
// system's window buttons their room — traffic lights at the left on macOS,
// caption buttons at the right on Windows, either side on Linux — and drags the
// window, as Books' toolbar does.
func (a *app) header(c *ui.Context, t *ui.Theme) {
	bar := c.TitleBar()
	pal := a.chrome
	ui.Row(c).FillWidth().Height(max(headerHeight, bar.Height)).
		Padding(0, 8+bar.Right, 0, 8+bar.Left).AlignItems(ui.Center).DragWindow().
		Children(func() {
			a.toolbarCluster(c, func() {
				a.panelButton(c, appstate.SidebarToc, icons.ListBullet, locales.PanelContents(), locales.PanelContentsTip())
				a.panelButton(c, appstate.SidebarBookmarks, icons.BookMarkSquare, locales.PanelBookmarks(), locales.PanelBookmarksTip())
				a.panelButton(c, appstate.SidebarNotes, icons.NoteCard, locales.PanelNotes(), locales.PanelNotesTip())
			})

			// The book's name is centred in the window itself, not in the room
			// left between the clusters, as Books sets it.
			ui.Box(c).Absolute().Left(0).Right(0).Top(0).Bottom(0).Center().PassThrough().Children(func() {
				ui.Text(c, a.book.Title).FontSize(12).FontWeight(600).
					TextColor(t.TextMuted).SingleLine().MaxWidth(320)
			})
			ui.Spacer(c)

			if a.failed && a.status != "" {
				ui.Text(c, a.status).FontSize(11).TextColor(t.Danger).SingleLine().MaxWidth(180)
			}
			a.toolbarCluster(c, func() {
				a.toolbarButton(c, icons.Share, locales.Share(), locales.Share(), nil)
				var aa ui.Element
				aa = a.toolbarTextButton(c, "Aa", "Aa", locales.ThemesSettings())
				if aa.Clicked() {
					a.aaOpen = !a.aaOpen
					a.syncSettingsFields()
				}
				a.settingsPanel(c, aa)
				a.toolbarButton(c, icons.Search, locales.Search(), locales.SearchTip(), func() {
					a.togglePanel(appstate.SidebarSearch)
				})
			})
			ui.Box(c).Width(13)
			// The ribbon: bookmarks the page, and fills with the accent when the
			// page is bookmarked already, so one control turns the mark on and off
			// as Books' own does.
			on := a.bookmarkedAt()
			label, tip := locales.AddBookmark(), locales.AddBookmarkTip()
			fill, ink := pal.Toolbar, pal.OnToolbar
			if on {
				label, tip = locales.RemoveBookmark(), locales.RemoveBookmarkTip()
				fill, ink = pal.Accent, pal.OnAccent
			}
			b := ui.Box(c).Width(27).Height(27).Radius(13.5).Center().
				Background(reader.UIColor(fill)).Cursor(ui.CursorPointer).
				Label(label).Tooltip(tip).OnClick(a.toggleBookmark)
			b.Children(func() {
				ui.Icon(c, icons.Bookmark).Size(15, 15).TextColor(reader.UIColor(ink))
			})
		})
}

// panels floats the open reading panel over the page at the left, in a card
// inset from the page's edges, as Books floats its table of contents. It is
// absolute in the page's box, so the page keeps every DIP of its measure while
// the panel is open: opening one never reflows the book.
func (a *app) panels(c *ui.Context) {
	if a.pan == nil || !a.pan.Showing() {
		return
	}
	pal := a.chrome
	card := ui.Box(c).Absolute().Left(panelMargin).Top(panelTop).Bottom(panelMargin).
		Width(panelWidth).Radius(14).Background(reader.UIColor(pal.Popover)).
		Border(1, reader.UIColor(pal.Line)).Clip().
		Shadow(0, 12, 32, 0, ui.RGBA(0, 0, 0, 0.24)).Label(a.pan.Title())
	card.Children(func() {
		a.pan.View(c)
	})
}

// toolbarCluster is the capsule that groups the title bar's glyphs, as Books
// draws it: a soft pill one step below the paper, with no divisions inside.
func (a *app) toolbarCluster(c *ui.Context, build func()) {
	ui.Row(c).Height(toolbarHeight).Radius(toolbarHeight/2).Padding(0, 1).
		Background(reader.UIColor(a.chrome.Toolbar)).AlignItems(ui.Center).
		Children(build)
}

// toolbarButton is one glyph cell inside a cluster: 16 DIPs of ink with a soft
// wash under the pointer.
func (a *app) toolbarButton(c *ui.Context, icon *ui.SVG, label, tip string, onClick func()) ui.Element {
	pal := a.chrome
	b := ui.Box(c).Width(toolbarCell).Height(toolbarHeight - 2).Radius(7).Center().
		Cursor(ui.CursorPointer).Label(label).KeepFocus().Tooltip(tip)
	if b.Hovered() {
		b.Background(reader.UIColor(pal.Hover))
	}
	if onClick != nil {
		b.OnClick(onClick)
	}
	b.Children(func() {
		ui.Icon(c, icon).Size(16, 16).TextColor(reader.UIColor(pal.OnToolbar))
	})
	return b
}

// panelButton is one of the three panel controls at the head of the reading
// bar: a glyph that floats its panel over the page, and closes it again when
// the panel is already the open one. The chosen cell keeps a wash, so the panel
// on screen is the one that reads as pressed.
func (a *app) panelButton(c *ui.Context, side string, icon *ui.SVG, label, tip string) {
	b := a.toolbarButton(c, icon, label, tip, nil)
	b.OnClick(func() { a.togglePanel(side) })
	if a.pan != nil && a.pan.Sidebar == side {
		b.Background(reader.UIColor(a.chrome.Pressed))
	}
}

// toolbarTextButton is toolbarButton for a control that is its own label, as
// Books writes the display settings.
func (a *app) toolbarTextButton(c *ui.Context, text, label, tip string) ui.Element {
	pal := a.chrome
	b := ui.Box(c).Width(toolbarCell).Height(toolbarHeight - 2).Radius(7).Center().
		Cursor(ui.CursorPointer).Label(label).KeepFocus().Tooltip(tip)
	if b.Hovered() {
		b.Background(reader.UIColor(pal.Hover))
	}
	b.Children(func() {
		ui.Text(c, text).FontSize(12).FontWeight(600).TextColor(reader.UIColor(pal.OnToolbar))
	})
	return b
}

// library is the window with nothing open: the source list at the left and the
// home page at the right, as Books' own library has it.
func (a *app) library(c *ui.Context, t *ui.Theme) {
	w, _ := c.Size()
	m := homeMetricsFor(w, !a.libSideOff)
	ui.Row(c).Fill().Background(t.Background).Children(func() {
		if !a.libSideOff {
			a.sourceList(c, t, m.side)
		}
		a.home(c, t, m)
	})
}

// sourceList is the library's sidebar: the search and the way in, then the
// library's own filters under a heading, then the collections, with the reader
// at the foot. It is a Mac source list: rows on a 26 DIP pitch, the chosen one
// filled with ink, and headings in the small muted type Books uses.
func (a *app) sourceList(c *ui.Context, t *ui.Theme, width float32) {
	pal := a.chrome
	bar := c.TitleBar()
	ui.Column(c).Width(width).FillHeight().Padding(0, 0, 16, 0).
		Background(reader.UIColor(pal.Side)).Children(func() {
		top := float32(librarySideTop)
		if bar.Height > top {
			top = bar.Height
		}
		// The head of the source list is the window's: the traffic lights sit
		// over it, and the strip drags the window.
		ui.Box(c).FillWidth().Height(top).DragWindow()
		if a.libSearch {
			ui.Row(c).Padding(0, 8, 2, 8).Children(func() {
				field := ui.SearchField(c, &a.libQuery).Label(locales.LibrarySearchLabel()).
					Placeholder(locales.LibrarySearchPlaceholder()).FillWidth()
				if field.Submitted() {
					field.Focus()
				}
			})
		}
		a.sourceRow(c, t, width, icons.Search, locales.Search(), false, func() {
			a.libSearch = !a.libSearch
			if !a.libSearch {
				a.libQuery = ""
			}
		})
		a.sourceRow(c, t, width, icons.House, locales.SidebarHome(), a.libFilter == 0, func() {
			a.libFilter, a.libSearch = 0, false
		})
		a.sourceRow(c, t, width, icons.Folder, locales.SidebarShelfFolder(), false, func() { a.chooseDir() })
		a.sourceRow(c, t, width, icons.FilePlus, locales.MenuOpen(), false, func() { a.chooseFile() })

		a.sourceHeading(c, t, locales.SidebarLibraryHeading())
		a.sourceRow(c, t, width, icons.Grid, locales.SidebarAll(), a.libFilter == 3, func() { a.libFilter = 3 })
		a.sourceRow(c, t, width, icons.Half, locales.SidebarReading(), a.libFilter == 1, func() { a.libFilter = 1 })
		a.sourceRow(c, t, width, icons.Check, locales.SidebarFinished(), a.libFilter == 2, func() { a.libFilter = 2 })

		a.sourceHeading(c, t, locales.SidebarCollections())
		a.sourceRow(c, t, width, icons.Book, locales.SidebarMyBooks(), false, func() {
			a.libFilter, a.libSearch = 0, false
		})
		a.sourceRow(c, t, width, icons.Clock, locales.SidebarRecent(), false, func() {
			a.libFilter, a.sortByTitle = 0, false
		})
		a.sourceRow(c, t, width, icons.Plus, locales.SidebarNewCollection(), false, func() {
			a.libFilter, a.libSearch = 0, false
		})

		ui.Spacer(c)
		a.accountRow(c, t)
	})
}

// sourceRow is one row of the source list: an icon and a name, filled with the
// ink colour while it is the chosen one, as Books marks the shelf being shown.
// The row is 24 DIPs tall inside a 26 DIP pitch, so the pill reads as a Mac
// sidebar's rather than as a full-width band.
func (a *app) sourceRow(c *ui.Context, t *ui.Theme, width float32, icon *ui.SVG, label string, chosen bool, onClick func()) {
	pal := a.chrome
	row := ui.Row(c).Width(width-16).Height(libraryRowHeight).Margin(1, 8).
		Radius(6).Center().Cursor(ui.CursorPointer).Label(label)
	// A chosen row is filled with ink on light paper and with a lighter grey
	// at night, as a Mac source list does it either way.
	ink := t.Text
	if chosen {
		fill := pal.Text
		ink = reader.UIColor(pal.OnAccent)
		if pal.Theme.Dark() {
			fill, ink = pal.Pressed, reader.UIColor(pal.Text)
		}
		row.Background(reader.UIColor(fill))
	} else if row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	if onClick != nil {
		row.OnClick(onClick)
	}
	row.Children(func() {
		// The icon is centred in a 30 DIP column, so every label starts at the
		// same place and the glyphs line up under one another.
		ui.Box(c).Width(30).FillHeight().Center().Children(func() {
			ui.Icon(c, icon).Size(15, 15).TextColor(ink)
		})
		ui.Text(c, label).FontSize(13).TextColor(ink).SingleLine().Grow(1)
	})
}

// sourceHeading is a heading of the source list: small, muted, and set apart
// from the rows above it.
func (a *app) sourceHeading(c *ui.Context, t *ui.Theme, text string) {
	ui.Row(c).Padding(12, 14, 6, 14).Children(func() {
		ui.Text(c, text).FontSize(11).FontWeight(600).TextColor(t.TextMuted).SingleLine()
	})
}

// accountRow is the foot of the sidebar: who is reading, as Books shows the
// account.
func (a *app) accountRow(c *ui.Context, t *ui.Theme) {
	pal := a.chrome
	ui.Row(c).FillWidth().Height(34).Padding(0, 16, 0, 16).Gap(4).Center().Children(func() {
		avatar := ui.Box(c).Width(22).Height(22).Radius(11).Center().
			Background(reader.UIColor(pal.Muted))
		avatar.Children(func() {
			ui.Icon(c, icons.Person).Size(13, 13).TextColor(reader.UIColor(pal.Side))
		})
		ui.Text(c, locales.LocalReader()).FontSize(12).FontWeight(600).TextColor(t.Text).SingleLine().Grow(1)
	})
}

// home is the shelf: the reading goals ring at the top right, the serif title,
// then the bands of covers — what is being read and the whole library — and the
// reading goals card under them, as Books' own home page has it.
func (a *app) home(c *ui.Context, t *ui.Theme, m homeMetrics) {
	entries := a.shelf()
	ui.Scroll(c).Grow(1).MinWidth(0).FillHeight().Background(t.Background).Children(func() {
		ui.Column(c).FillWidth().Padding(0, 0, 40, 0).Children(func() {
			a.homeBar(c, t)
			ui.Column(c).FillWidth().Padding(6, m.padding, 0, m.padding).Children(func() {
				ui.Text(c, a.homeTitle()).Font(serifHead).FontSize(26).FontWeight(700).
					TextColor(t.Text)
				if a.book == nil && a.status != "" && a.failed {
					ui.Text(c, a.status).FontSize(12).TextColor(t.Danger).MaxWidth(600).
						Padding(6, 0, 0, 0)
				}
			})
			switch {
			case a.pan == nil || a.pan.Library == nil || len(entries) == 0:
				a.homeEmpty(c, t, m)
				return
			}
			top := m.firstGap
			if a.libFilter == 0 {
				if recent := a.byLastRead(a.inProgress(entries)); len(recent) > 0 {
					a.shelfBand(c, t, m, 0, top, locales.ContinueReading(),
						locales.BooksInProgress(len(recent)), recent)
					top = m.bandGap
				}
			}
			a.shelfBand(c, t, m, 1, top, locales.AllBooks(), a.libraryLine(), entries)
			if a.libFilter == 0 {
				a.goalsSection(c, t, m)
			}
		})
	})
}

// homeBar is the strip above the title: the week's reading ring at the right,
// where Books floats it over the content. With the source list hidden this is
// the library's only title bar, so the empty stretch beside the ring drags the
// window, and the ring keeps clear of the window buttons when the desktop puts
// them at the right.
func (a *app) homeBar(c *ui.Context, t *ui.Theme) {
	bar := c.TitleBar()
	ui.Row(c).FillWidth().Height(homeTopHeight).AlignItems(ui.Center).
		Padding(8, 12+bar.Right, 0, 0).DragWindow().Children(func() {
		ui.Spacer(c)
		a.weekRing(c, t)
	})
}

// weekRing is the small ring at the top right: the days read this week over the
// week's goal, as Books counts a reading streak.
func (a *app) weekRing(c *ui.Context, t *ui.Theme) {
	st := a.readingStats(a.clock())
	track := t.Accent.Alpha(0.22)
	ring := ui.Box(c).Width(28).Height(28).Label(locales.WeekRing()).
		Tooltip(locales.WeekRingTip(st.DaysThisWeek, st.WeekGoal)).
		Draw(func(p *ui.Painter, r ui.Rect) {
			drawRing(p, r, st.weekProgress(), t.Accent, track, 3)
		})
	ring.Children(func() {
		ui.Column(c).Fill().Center().Children(func() {
			ui.Textf(c, "%d", st.DaysThisWeek).FontSize(10).FontWeight(600).TextColor(t.Accent)
			ui.Textf(c, "%d", st.WeekGoal).FontSize(8).TextColor(t.TextMuted)
		})
	})
}

// homeTitle is the heading of the page: the filter being shown, as Books names
// each of its views.
func (a *app) homeTitle() string {
	switch a.libFilter {
	case 1:
		return locales.SidebarReading()
	case 2:
		return locales.SidebarFinished()
	case 3:
		return locales.SidebarAll()
	}
	return locales.SidebarHome()
}

// homeEmpty is the shelf with nothing on it: what to do, and how.
func (a *app) homeEmpty(c *ui.Context, t *ui.Theme, m homeMetrics) {
	ui.Column(c).FillWidth().Padding(60, m.padding, 30, m.padding).Gap(10).
		AlignItems(ui.Center).Children(func() {
		ui.Icon(c, icons.BookOpen).Size(40, 40).TextColor(t.TextMuted)
		ui.Text(c, locales.HomeEmptyTitle()).FontSize(15).FontWeight(600)
		ui.Text(c, locales.HomeEmptyHint()).
			FontSize(12).TextColor(t.TextMuted).TextAlign(ui.Center)
		ui.Row(c).Gap(8).Padding(6, 0, 0, 0).Children(func() {
			if ui.Button(c, locales.MenuOpen()).Clicked() {
				a.chooseFile()
			}
			if ui.Button(c, locales.ChooseFolder()).Clicked() {
				a.chooseDir()
			}
		})
	})
}

// shelfBand is one band of the library: its name and a chevron, a caption, and
// a row of covers that scrolls sideways. Books writes the count into the caption
// and leaves the covers to speak.
func (a *app) shelfBand(c *ui.Context, t *ui.Theme, m homeMetrics, row int, top float32, title, caption string, entries []library.Entry) {
	if len(entries) == 0 {
		return
	}
	ui.Column(c).FillWidth().Padding(top, 0, 0, 0).Children(func() {
		ui.Column(c).FillWidth().Padding(0, m.padding, 0, m.padding).Children(func() {
			head := ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(4)
			head.Children(func() {
				group := ui.Row(c).Gap(4).Center().Cursor(ui.CursorPointer).Label(title)
				group.OnClick(func() { a.libFilter = bandFilter(row) })
				group.Children(func() {
					ui.Text(c, title).FontSize(15).FontWeight(600).TextColor(t.Text)
					ui.Icon(c, icons.ChevronRight).Size(12, 12).TextColor(t.TextMuted)
				})
				ui.Spacer(c)
				// The order is a small control that appears with the pointer,
				// so the band keeps Books' clean line otherwise.
				if head.Hovered() {
					if widgets.GlyphButton(c, icons.Sort, locales.Sort(), locales.SortTip()).Clicked() {
						a.sortByTitle = !a.sortByTitle
					}
				}
			})
			if caption != "" {
				ui.Text(c, caption).FontSize(12).TextColor(t.TextMuted).SingleLine()
			}
		})
		ui.ScrollHorizontal(c).FillWidth().TrackScroll(&a.shelfScroll[row]).
			Padding(m.coverH*0.088, 0, 0, m.padding).Gap(m.coverGap).Children(func() {
			for _, e := range entries {
				a.bookCover(c, t, m, e)
			}
		})
	})
}

// bandFilter is the filter a band's chevron goes to: the books being read for
// the first band, the whole library for all-books.
func bandFilter(row int) int {
	if row == 0 {
		return 1
	}
	return 3
}

// bookCover is one book on the shelf: the cover alone, as Books shows it, with
// the progress of a part-read book as a thin line on its edge.
func (a *app) bookCover(c *ui.Context, t *ui.Theme, m homeMetrics, e library.Entry) {
	board := ui.Box(c).Width(m.coverW).Height(m.coverH).Radius(3).Background(t.Surface).
		Clip().Shadow(0, 2, 7, 0, ui.RGBA(0, 0, 0, 0.16)).
		Cursor(ui.CursorPointer).Label(e.Title)
	board.OnClick(func() { a.openPath(e.Path) })
	if art := a.coverBitmap(e); art != nil {
		board.Children(func() {
			ui.Image(c, art).Fill().Fit(ui.Cover)
		})
	} else {
		board.Border(1, t.Border).Center().Children(func() {
			ui.Icon(c, icons.Book).Size(22, 22).TextColor(t.TextMuted)
		})
	}
	if read := a.readProgress(e); read > 0 && read < 1 {
		board.Children(func() {
			ui.Box(c).Absolute().Left(0).Right(0).Bottom(0).Height(2).
				Background(t.Accent).Label(locales.ProgressLabel(e.Title))
		})
	}
}

// goalsSection is the foot of the home page: how the year is going, in the large
// ring Books draws, with the week and the streak beside it.
func (a *app) goalsSection(c *ui.Context, t *ui.Theme, m homeMetrics) {
	st := a.readingStats(a.clock())
	ui.Column(c).FillWidth().Padding(m.goalsGap, m.padding, 0, m.padding).Children(func() {
		ui.Text(c, locales.GoalsTitle()).FontSize(15).FontWeight(600).TextColor(t.Text)
		ui.Text(c, locales.GoalsHint()).FontSize(12).TextColor(t.TextMuted).
			SingleLine()
		a.goalCard(c, t, m, st)
	})
}

// goalCard is the white card under the heading: the year's ring in the middle,
// the record beside it, and the two controls Books floats at its top right.
func (a *app) goalCard(c *ui.Context, t *ui.Theme, m homeMetrics, st readingStats) {
	ui.Column(c).FillWidth().Padding(20, 0, 0, 0).Children(func() {
		card := ui.Box(c).FillWidth().Radius(10).Background(t.Surface).
			Border(1, t.Border.Alpha(0.45)).
			Shadow(0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.08)).
			Padding(28, 24, 24, 24)
		card.Children(func() {
			ui.Row(c).Absolute().Top(10).Right(12).Gap(2).Children(func() {
				gear := widgets.GlyphButton(c, icons.Sliders, locales.GoalsSettings(), locales.GoalsSettingsTip())
				if gear.Clicked() {
					a.goalsOpen = !a.goalsOpen
				}
				a.goalsPanel(c, gear)
				if widgets.GlyphButton(c, icons.Share, locales.ShareRecord(), locales.ShareRecordTip()).Clicked() {
					a.shareStats(c, st)
				}
			})
			ui.Column(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ring := ui.Box(c).Width(m.ring).Height(m.ring).Label(locales.YearGoalLabel()).
					Draw(func(p *ui.Painter, r ui.Rect) {
						drawRing(p, r, st.yearProgress(), t.Accent, t.SurfaceHover, 9)
					})
				ring.Children(func() {
					ui.Column(c).Fill().Center().Children(func() {
						ui.Textf(c, "%d", st.BooksThisYear).FontSize(28).FontWeight(600).
							TextColor(t.Text)
						ui.Text(c, locales.GoalOfBooks(st.BooksGoal)).FontSize(11).TextColor(t.TextMuted)
						ui.Text(c, locales.FinishedThisYear()).FontSize(11).TextColor(t.TextMuted).Padding(4, 0, 0, 0)
					})
				})
				ui.Row(c).Gap(44).Padding(18, 0, 0, 0).Center().Children(func() {
					a.goalStat(c, t, locales.StatDays(), locales.UnitDays(st.Days))
					a.goalStat(c, t, locales.StatStreak(), locales.UnitDays(st.Streak))
					a.goalStat(c, t, locales.SidebarFinished(), locales.UnitBooks(st.Finished))
				})
			})
			if !a.goalCopied.IsZero() && a.clock().Sub(a.goalCopied) < 3*time.Second {
				ui.Text(c, locales.RecordCopied()).FontSize(11).TextColor(t.TextMuted).
					FillWidth().TextAlign(ui.Center).Padding(10, 0, 0, 0)
				c.After(3*time.Second - a.clock().Sub(a.goalCopied))
			}
		})
	})
}

// goalStat is one of the three figures under the ring: a number and what it
// counts, as the message of the figure writes them.
func (a *app) goalStat(c *ui.Context, t *ui.Theme, label, value string) {
	ui.Column(c).AlignItems(ui.Center).Children(func() {
		ui.Text(c, value).FontSize(15).FontWeight(600).TextColor(t.Text)
		ui.Text(c, label).FontSize(11).TextColor(t.TextMuted).Padding(2, 0, 0, 0)
	})
}

// goalsPanel is the small card behind the card's controls: how many books a
// year and how many days a week the reader means to read. Every choice is
// stored as it is made.
func (a *app) goalsPanel(c *ui.Context, anchor ui.Element) {
	if !a.goalsOpen {
		return
	}
	pal := a.chrome
	ui.PopoverBase(c, anchor, &a.goalsOpen, func(panel ui.Element) {
		panel.Width(212).Padding(10).Radius(12).Gap(8).
			Background(reader.UIColor(pal.Popover)).
			Shadow(0, 10, 28, 0, ui.RGBA(0, 0, 0, 0.24)).
			Children(func() {
				ui.Text(c, locales.GoalsTitle()).FontSize(11).TextColor(mutedOn(pal)).FillWidth().
					TextAlign(ui.Center)
				a.goalChoices(c, locales.GoalBooksLabel(), locales.GoalBooksCell, []int{6, 12, 24, 52}, a.goalBooks,
					func(n int) { a.setGoals(n, a.goalDays) })
				a.goalChoices(c, locales.GoalDaysLabel(), locales.GoalDaysCell, []int{3, 5, 7}, a.goalDays,
					func(n int) { a.setGoals(a.goalBooks, n) })
			})
	})
}

// goalChoices is one goal as a segmented row: the numbers to pick from, with the
// one in force filled. sayCell names one choice for assistive technology, as
// "24 books a year".
func (a *app) goalChoices(c *ui.Context, label string, sayCell func(int) string, choices []int, chosen int, onPick func(int)) {
	pal := a.chrome
	ui.Column(c).Children(func() {
		ui.Text(c, label).FontSize(11).TextColor(mutedOn(pal))
		ui.Row(c).FillWidth().Height(24).Radius(6).Padding(2).Gap(2).
			Background(reader.UIColor(pal.Pressed)).Children(func() {
			for _, n := range choices {
				cell := ui.Box(c).Grow(1).FillHeight().Radius(5).Center().
					Cursor(ui.CursorPointer).Label(sayCell(n))
				if n == chosen {
					cell.Background(reader.UIColor(pal.Popover))
				} else if cell.Hovered() {
					cell.Background(reader.UIColor(pal.Hover))
				}
				cell.OnClick(func() { onPick(n) })
				weight := 400
				if n == chosen {
					weight = 600
				}
				cell.Children(func() {
					ui.Textf(c, "%d", n).FontSize(11).FontWeight(weight).
						TextColor(reader.UIColor(pal.Text))
				})
			}
		})
	})
}

// tMuted is the caption colour on a panel, which sits on the popover colour
// rather than on the page.
func mutedOn(pal reader.Palette) ui.Color {
	return reader.UIColor(pal.Muted)
}

// shareStats puts the reading record on the clipboard and notes it on the card
// for a moment.
func (a *app) shareStats(c *ui.Context, st readingStats) {
	c.WriteClipboard(locales.ShareRecordText(
		st.BooksThisYear, st.BooksGoal, st.DaysThisWeek, st.WeekGoal, st.Days, st.Streak))
	a.goalCopied = a.clock()
}

// libraryLine is the caption under the shelf's title: how many books it holds.
// The shelf is the books the reader opened, so there is no folder to name.
func (a *app) libraryLine() string {
	if a.pan == nil || a.pan.Library == nil || len(a.pan.Library.Entries) == 0 {
		return locales.LibraryEmptyLine()
	}
	return locales.LibraryCount(len(a.pan.Library.Entries))
}

// shelf is the whole library as the current filter and query leave it.
func (a *app) shelf() []library.Entry {
	if a.pan == nil || a.pan.Library == nil {
		return nil
	}
	entries := a.pan.Library.Filter(strings.TrimSpace(a.libQuery))
	out := entries[:0:0]
	for _, e := range entries {
		read := a.readProgress(e)
		switch a.libFilter {
		case 1:
			if read <= 0 || read >= 1 {
				continue
			}
		case 2:
			if read < 1 {
				continue
			}
		}
		out = append(out, e)
	}
	if a.sortByTitle {
		sort.SliceStable(out, func(i, j int) bool { return out[i].Title < out[j].Title })
		return out
	}
	return a.byLastRead(out)
}

// inProgress keeps the books that have been started and not finished.
func (a *app) inProgress(entries []library.Entry) []library.Entry {
	out := make([]library.Entry, 0, len(entries))
	for _, e := range entries {
		if read := a.readProgress(e); read > 0 && read < 1 {
			out = append(out, e)
		}
	}
	return out
}

// readingCount is how many books are part read; the sidebar shows it as the
// count of what is waiting.
func (a *app) readingCount() int {
	if a.pan == nil || a.pan.Library == nil {
		return 0
	}
	return len(a.inProgress(a.pan.Library.Entries))
}

// byLastRead orders the shelf by when each book was last opened, the newest
// first, and the rest by title: the same order the sidebar's shelf uses.
func (a *app) byLastRead(entries []library.Entry) []library.Entry {
	out := append([]library.Entry(nil), entries...)
	when := func(e library.Entry) time.Time {
		if a.st == nil {
			return e.Modified
		}
		if p, ok := a.st.Position(store.Key(e.Path, e.Size, e.Modified)); ok {
			return p.Updated
		}
		return e.Modified
	}
	sort.SliceStable(out, func(i, j int) bool {
		ti, tj := when(out[i]), when(out[j])
		if ti.Equal(tj) {
			return out[i].Title < out[j].Title
		}
		return ti.After(tj)
	})
	return out
}

// readProgress is how far into a book the reader got, from 0 to 1.
func (a *app) readProgress(e library.Entry) float64 {
	if a.st == nil {
		return 0
	}
	p, ok := a.st.Position(store.Key(e.Path, e.Size, e.Modified))
	if !ok || p.Progress <= 0 {
		return 0
	}
	return float64(p.Progress)
}

// reader is the reading area: the paper the theme prints on, the page or the
// two pages of the open book set on it without a frame, and the folio under
// them.
func (a *app) reader(c *ui.Context, t *ui.Theme) {
	ui.Column(c).Grow(1).MinWidth(0).FillHeight().Background(paper()).Children(func() {
		area := ui.Column(c).Grow(1).MinWidth(0).FillWidth().AlignItems(ui.Center).
			Padding(0, pagePadding).Bind(&a.area).Children(func() {
			a.backEdge(c, t)
			wide := pageMaxWidth * float32(a.pagesOnSheet())
			box := ui.Box(c).FillWidth().MaxWidth(wide).FillHeight().
				Bind(&a.body).Label(locales.PageLabel()).Focusable().
				Cursor(a.linkCursor()).
				HandleInput(a.handlePageInput).
				Draw(a.drawSheet)
			// Only a page has a selection to act on; the cover has none.
			if a.layout != nil && !a.showCover {
				box.ContextMenu(func(m *ui.Menu) { a.pageMenu(c, m) })
			}
			box.Children(func() {
				if a.layout != nil {
					return
				}
				message := a.status
				switch {
				case a.loading:
					message = locales.LayingOut()
				case message == "":
					message = locales.ReaderEmptyHint()
				}
				ui.Column(c).Fill().Center().Padding(32).Gap(10).Children(func() {
					if a.loading {
						ui.Spinner(c).Size(18, 18)
					}
					colour := t.TextMuted
					if a.failed {
						colour = t.Danger
					}
					ui.Text(c, message).TextColor(colour).TextAlign(ui.Center).MaxWidth(320)
				})
			})
		})
		a.folio(c, t)
		// The page area's width is what says whether the sheet holds one page or
		// an open book, so it is read before the layout that depends on it is
		// asked for. The box is the committed frame's, which is one frame older
		// than the area, and the very first frame of a book has neither: the
		// layout waits for the area and asks for the frame that measures it,
		// rather than laying the whole book out at a guessed width and then
		// laying it out again at the real one.
		if ab := area.Bounds(); ab.W > 0 {
			a.areaW = ab.W
		}
		if b := a.body.Bounds(c); b.W > 0 && b.H > 0 {
			a.boxW, a.boxH = b.W, b.H
			a.ensureLayout(b.W, b.H)
		}
		// The first frame of a book has no box and no page area behind it yet, and
		// the layout cannot be asked for without them: ask for the frame that has
		// them, rather than laying the book out at a guessed width or leaving the
		// reader on the spinner until something else happens to draw. A window
		// with no room in it measures nothing, so it is not asked again: a resize
		// builds a frame of its own.
		if w, h := c.Size(); w > 0 && h > 0 && a.layout == nil && !a.loading && (a.boxW <= 0 || a.areaW <= 0) {
			c.Invalidate()
		}
		// A window that grew meanwhile leaves less flow below the viewport: keep
		// the column inside the book rather than past its end.
		if a.scrolling() {
			a.scrollY = clampf(a.scrollY, 0, a.maxScroll())
			a.scrollGoal = clampf(a.scrollGoal, 0, a.maxScroll())
		}
	})
}

// folio is the bar under the text, as Books draws it: the chapter at the left,
// the page in the middle, what is left of the chapter at the right, and the
// small scrubber pill under them at the book's own progress.
func (a *app) folio(c *ui.Context, t *ui.Theme) {
	muted := t.TextMuted
	ink := reader.UIColor(a.chrome.Muted)
	w, _ := c.Size()
	ui.Column(c).FillWidth().Height(footerHeight).Children(func() {
		ui.Row(c).FillWidth().Height(30).Padding(0, 48).Gap(6).
			AlignItems(ui.Center).Children(func() {
			if a.layout == nil {
				return
			}
			// The left of the folio is the chapter, marked with the little
			// page badge Books prints beside it.
			badge := ui.Box(c).Width(13).Height(13).Radius(6.5).Center().
				Background(reader.UIColor(a.chrome.OnToolbar))
			badge.Children(func() {
				ui.Icon(c, icons.Bookmark).Size(8, 8).TextColor(paper())
			})
			ui.Textf(c, "%d", a.chapterNumber()).FontSize(10).TextColor(muted)
			ui.Spacer(c)
			if left := a.chapterPagesLeft(); left != "" {
				ui.Text(c, left).FontSize(10).TextColor(muted)
			}
		})
		// The page number is centred in the window, not between the two
		// other ends.
		ui.Box(c).Absolute().Left(0).Right(0).Top(0).Height(30).Center().PassThrough().Children(func() {
			if a.layout == nil {
				return
			}
			if hint := a.selectionHint(); hint != "" && !a.showCover {
				ui.Text(c, hint).FontSize(10).TextColor(muted)
				return
			}
			ui.Text(c, a.pageLabel()).FontSize(10).TextColor(muted)
		})
		ui.Spacer(c)
		// The scrubber: the whole window's width, the pill at the book's
		// progress, as Books shows the place in the book at the foot.
		ui.Box(c).FillWidth().Height(12).Children(func() {
			knob := ui.Box(c).Width(19).Height(6).Radius(3).Background(ink)
			p := float32(a.progress())
			knob.Margin(3, 0, 0, 8+p*(w-16-19))
		})
	})
}

// chapterNumber is the chapter the open spread starts in, numbered from one.
func (a *app) chapterNumber() int {
	if a.book == nil || a.layout == nil {
		return 0
	}
	if a.scrolling() {
		return a.currentChapter() + 1
	}
	return reader.ChapterOfPage(a.layout, len(a.book.Chapters), a.page) + 1
}

// selectionHint is the small note the folio carries while text is selected or
// just copied: "" when there is nothing to say.
func (a *app) selectionHint() string {
	if len(a.selQuads) == 0 {
		return ""
	}
	if a.selCopied > 0 {
		return locales.SelectionCopied(a.selCopied)
	}
	return locales.SelectionSelected()
}

// chapterPagesLeft is how much of the chapter is still to read, as Books counts
// it at the foot of the page: "本章还有 12 页". A scrolling column counts the
// screens it still has to go.
func (a *app) chapterPagesLeft() string {
	if a.book == nil || a.layout == nil {
		return ""
	}
	if a.scrolling() {
		return a.chapterScreensLeft()
	}
	i := reader.ChapterOfPage(a.layout, len(a.book.Chapters), a.page)
	first, ok := a.layout.Locate(render.AnchorID(i + 1))
	if !ok {
		return ""
	}
	if _, ok := a.layout.PageOf(first); !ok {
		return ""
	}
	last := a.layout.PageCount()
	if i+1 < len(a.book.Chapters) {
		if next, ok := a.layout.Locate(render.AnchorID(i + 2)); ok {
			if p, ok := a.layout.PageOf(next); ok {
				last = p
			}
		}
	}
	left := last - (a.page + a.pageStep())
	if left <= 0 {
		return locales.ChapterLastPage()
	}
	if left == 1 {
		return locales.ChapterOnePageLeft()
	}
	return locales.ChapterPagesLeft(left)
}

// backEdge is the way back to the library at the page's left edge: a chevron
// that appears while the pointer rests over the margin, as Books shows its page
// arrows, and floats over the page so the book keeps its measure. It is the only
// edge control: turning on is the keyboard's and the folio's job.
func (a *app) backEdge(c *ui.Context, t *ui.Theme) {
	strip := ui.Box(c).Absolute().Left(0).Top(0).Bottom(0).Width(edgeWidth).Center().
		Cursor(ui.CursorPointer).Label(locales.BackToLibrary()).Tooltip(locales.BackToLibraryTip())
	strip.OnClick(a.closeBook)
	strip.Children(func() {
		if !strip.Hovered() {
			return
		}
		button := ui.Box(c).Width(26).Height(26).Radius(13).Center().
			Background(t.SurfaceHover)
		button.Children(func() {
			ui.Icon(c, icons.ChevronLeft).Size(15, 15).TextColor(t.TextMuted)
		})
	})
}

// closeBook puts the library back in front of the reader. The reading position
// is already in the store, so the book opens where it was left.
func (a *app) closeBook() {
	a.job++ // drop any layout still in flight
	a.loading = false
	a.failed = false
	a.turning = nil
	a.aaOpen = false
	a.book = nil
	a.doc = nil
	a.docAt = 0
	a.layout = nil
	a.page = 0
	a.showCover = false
	a.file = ""
	a.status = ""
	a.resumeNote = ""
	a.layW, a.layH = 0, 0
	a.boxW, a.boxH, a.areaW = 0, 0, 0
	a.layScroll = false
	a.scrollY, a.tileH, a.scrollChapter = 0, 0, -1
	a.scrollGoal, a.scrollAt = 0, time.Time{}
	a.clearSelection()
	a.hitQuads = nil
	a.noteQuads = nil
	if a.paint != nil {
		a.paint.ClearImages()
	}
	if a.win != nil {
		a.win.SetTitle(appName)
		a.win.SetRepresentedFile("")
	}
	if a.pan != nil {
		a.pan.Refresh(nil, nil, 0)
		a.pan.Show(appstate.SidebarNone)
	}
}

// toggleSidebar hides or shows the library's source list. The reader floats its
// panels over the page instead of narrowing it, so it has no sidebar to toggle.
func (a *app) toggleSidebar() {
	if a.book == nil {
		a.libSideOff = !a.libSideOff
	}
}

// togglePanel opens side over the page, or closes it when it is already the
// open panel, so a second click on the same toolbar button puts the panel away.
func (a *app) togglePanel(side string) {
	if a.pan == nil {
		return
	}
	if a.pan.Sidebar == side {
		a.pan.Show(appstate.SidebarNone)
		return
	}
	a.pan.Show(side)
}
