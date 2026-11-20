package main

// The phone's own shapes of the two windows: a library with a large title, a
// bar of destinations at the foot and covers in a grid rather than a Mac source
// list; and a reading page whose bars are a finger's size, whose middle is the
// way to show the chrome, and whose scrolling column a finger drags.
//
// None of it is a second app. The same view builds the same reader; only where
// the pieces go, and how large their targets are, follows the screen.

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	uimobile "github.com/egoist/mygo/ui/mobile"
	"github.com/laix/quire/internal/appstate"
	"github.com/laix/quire/internal/icons"
	"github.com/laix/quire/internal/library"
	"github.com/laix/quire/internal/reader"
	"github.com/laix/quire/locales"
)

// onPhone reports that the app runs on a phone's system. The layout asks the
// window's width as well, so an iPad keeps the desk-like library and a desktop
// build with a narrow window does not turn into a phone.
//
// QUIRE_MOBILE=1 makes a desktop build take the phone's shape too, so the
// phone layout can be looked at without a simulator or a device: the window
// comes up at 390×844 and the bars, gestures and covers follow the screen.
var onPhone = func() bool {
	if os.Getenv("QUIRE_MOBILE") != "" {
		return true
	}
	return runtime.GOOS == "ios" || runtime.GOOS == "android"
}

const (
	// A phone is a window that is narrow or short: an iPhone in portrait, or
	// the same phone on its side, where the height gives it away.
	phoneMaxWidth  = 700
	phoneMaxHeight = 500

	// The reading bars take a finger's sizes: the bar at the head, the tools
	// at the foot, and one target in either.
	mobileHeaderHeight = 44
	// mobileFooterHeight is the tools' band and the progress pill under them.
	// The folio's page label floats over the page above it, so the band does
	// not keep a row for it.
	mobileFooterHeight = 48
	mobileCell         = 44
	mobileToolHeight   = 40
	mobileLabelHeight  = 18
	mobileScrubHeight  = 8
	mobileTabHeight    = 50
	// mobileSheetTitleHeight is the sheet's own bar: the title in the middle
	// and the close control at the right, under the grabber.
	mobileSheetTitleHeight = 52

	// A bottom inset taller than this is the keyboard rather than the system's
	// bar, and the tab bar goes under it as the system's does.
	mobileKeyboard = 120

	// The library's grid and margins on a phone.
	mobilePadding  = 16
	mobileCoverGap = 14
)

// compact reports that this window is a phone's screen, where the library and
// the reading bars take the phone's shape.
func (a *app) compact(c *ui.Context) bool {
	if !a.phone {
		return false
	}
	w, h := c.Size()
	return w > 0 && h > 0 && (w < phoneMaxWidth || h < phoneMaxHeight)
}

// barRoom is the reading bar's height: a phone's 44 DIPs, or the desktop bar
// that leaves the system's window buttons their room.
func (a *app) barRoom(c *ui.Context) float32 {
	if a.compact(c) {
		return mobileHeaderHeight
	}
	return barHeight(c)
}

// footerRoom is the tools' height at the foot of the page.
func (a *app) footerRoom(c *ui.Context) float32 {
	if a.compact(c) {
		return mobileFooterHeight
	}
	return footerHeight
}

// syncPhoneChrome tells the native window what it cannot know from the view:
// the colour that fills the safe-area edges around the content, and whether the
// system's bar icons must be light or dark over it. Both are set only when they
// change, as each costs the system a call and a repaint.
func (a *app) syncPhoneChrome(t *ui.Theme) {
	if !a.phone {
		return
	}
	style := mygo.BarStyleDark
	if t.Dark {
		style = mygo.BarStyleLight
	}
	if a.sysBar != style {
		a.sysBar = style
		mygo.SystemBars.SetStyle(style)
	}
	if a.win == nil || a.win.IsDestroyed() {
		return
	}
	hex := fmt.Sprintf("#%02x%02x%02x", t.Background.R, t.Background.G, t.Background.B)
	if hex == a.winBG {
		return
	}
	a.winBG = hex
	_ = a.win.SetBackgroundColor(hex)
}

// mobileLibrary is the library on a phone: the title at the head, the shelf in
// the middle, and the destinations at the foot, as every app on the system
// looks. The Mac source list is gone; what it held that a phone needs is under
// Sources, and its filters are the bar's other destinations.
func (a *app) mobileLibrary(c *ui.Context, t *ui.Theme) {
	m := phoneMetricsFor(widthOf(c))
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		a.mobileTopBar(c, t)
		switch a.libTab {
		case mobileTabSources:
			a.mobileSources(c, t)
		case mobileTabSearch:
			a.mobileSearchScreen(c, t, m)
		default:
			a.mobileShelf(c, t, m)
		}
		a.mobileTabs(c, t)
	})
}

// widthOf is the window's width, or 0 before it is measured.
func widthOf(c *ui.Context) float32 {
	w, _ := c.Size()
	return w
}

// mobileTabTitle is the name of a destination, as the large title says it. The
// library is the whole shelf, and home is what is being read and the year's
// goals, so the two do not carry the same title.
func (a *app) mobileTabTitle(tab int) string {
	switch tab {
	case mobileTabLibrary:
		return locales.SidebarLibraryHeading()
	case mobileTabSources:
		return locales.SidebarSources()
	case mobileTabSearch:
		return locales.Search()
	}
	return locales.SidebarHome()
}

// mobileTopBar is the large title of a phone's library. It is the system's
// shape drawn by the app, so a desktop build with the phone layout sees the
// same thing. Search is a destination of its own in the tab bar, so the bar
// has no trailing button.
func (a *app) mobileTopBar(c *ui.Context, t *ui.Theme) {
	uimobile.TopBar(c, uimobile.TopBarOptions{Title: a.mobileTabTitle(a.libTab), Large: true})
}

// mobileSearchScreen is the search destination: the shelf's own field at the
// head and the books it matches under it. Only the shelf is searched: the
// shops are another destination and need the network.
func (a *app) mobileSearchScreen(c *ui.Context, t *ui.Theme, m homeMetrics) {
	ui.Row(c).FillWidth().Padding(0, mobilePadding, 10, mobilePadding).Children(func() {
		field := ui.SearchField(c, &a.libQuery).Label(locales.LibrarySearchLabel()).
			Placeholder(locales.LibrarySearchPlaceholder()).FillWidth()
		if a.searchFocus {
			// The tab was just chosen: the field takes the keys and the keyboard
			// comes up, once, so a tap on the field later still puts it away.
			a.searchFocus = false
			field.Focus()
		}
	})
	query := strings.TrimSpace(a.libQuery)
	if query == "" {
		ui.Column(c).Grow(1).Center().Padding(32).Children(func() {
			ui.Text(c, locales.LibrarySearchHint()).FontSize(13).
				TextColor(t.TextMuted).TextAlign(ui.Center).MaxWidth(280)
		})
		return
	}
	entries := a.filteredShelf(3, a.libQuery)
	ui.Scroll(c).Grow(1).MinWidth(0).FillHeight().Children(func() {
		ui.Column(c).FillWidth().Padding(0, 0, 28, 0).Children(func() {
			a.mobileShelfGrid(c, t, m, "", locales.LibraryCount(len(entries)), entries, 0)
		})
	})
}

// mobileShelf is the middle of the phone: home, which is what is being read
// and the year's goals, or the library, which is the whole shelf with its
// filters. A filter is not a destination of its own: it narrows the library.
func (a *app) mobileShelf(c *ui.Context, t *ui.Theme, m homeMetrics) {
	home := a.libTab == mobileTabHome
	filter := a.libFilter
	if home {
		filter = 0
	}
	entries := a.filteredShelf(filter, "")
	ui.Scroll(c).Grow(1).MinWidth(0).FillHeight().Background(t.Background).Children(func() {
		ui.Column(c).FillWidth().Padding(0, 0, 28, 0).Children(func() {
			if a.status != "" && a.failed {
				ui.Column(c).FillWidth().Padding(4, m.padding, 0, m.padding).Children(func() {
					ui.Text(c, a.status).FontSize(12).TextColor(t.Danger)
				})
			}
			if a.pan == nil || a.pan.Library == nil || len(a.pan.Library.Entries) == 0 {
				a.homeEmpty(c, t, m)
				return
			}
			if home {
				if recent := a.byLastRead(a.inProgress(entries)); len(recent) > 0 {
					a.shelfBand(c, t, m, 0, m.firstGap, locales.ContinueReading(),
						locales.BooksInProgress(len(recent)), recent)
				} else {
					ui.Column(c).FillWidth().Padding(m.firstGap, m.padding, 0, m.padding).Children(func() {
						ui.Text(c, locales.HomeNothingInProgress()).FontSize(13).
							TextColor(t.TextMuted)
					})
				}
				a.goalsSection(c, t, m)
				return
			}
			a.mobileFilterRow(c, t, m)
			a.mobileShelfGrid(c, t, m, "", locales.LibraryCount(len(entries)), entries, 0)
		})
	})
}

// mobileFilterRow is the library's own row: which books to show, and the order
// to show them in. Both are always on screen, as a finger has no hover to bring
// them out with.
func (a *app) mobileFilterRow(c *ui.Context, t *ui.Theme, m homeMetrics) {
	pal := a.chrome
	ui.Row(c).FillWidth().Padding(6, m.padding, 0, m.padding).Gap(8).
		AlignItems(ui.Center).Children(func() {
		for _, f := range []struct {
			filter int
			label  string
		}{{3, locales.SidebarAll()}, {1, locales.SidebarReading()}, {2, locales.SidebarFinished()}} {
			f := f
			chosen := a.libFilter == f.filter
			chip := ui.Box(c).Height(32).Padding(0, 14).Radius(16).Center().
				Cursor(ui.CursorPointer).Label(f.label)
			ink := t.TextMuted
			if chosen {
				chip.Background(reader.UIColor(pal.Accent))
				ink = reader.UIColor(pal.OnAccent)
			} else {
				chip.Background(t.SurfaceHover)
			}
			chip.OnClick(func() { a.libFilter = f.filter })
			chip.Children(func() {
				ui.Text(c, f.label).FontSize(13).FontWeight(600).TextColor(ink)
			})
		}
		ui.Spacer(c)
		sort := ui.Box(c).Size(36, 36).Radius(10).Center().
			Cursor(ui.CursorPointer).Label(locales.Sort()).Tooltip(locales.SortTip())
		if sort.Hovered() {
			sort.Background(t.SurfaceHover)
		}
		sort.OnClick(func() { a.sortByTitle = !a.sortByTitle })
		sort.Children(func() {
			ui.Icon(c, icons.Sort).Size(18, 18).TextColor(t.TextMuted)
		})
	})
}

// mobileShelfGrid is one band of the shelf as a grid of covers, as the system's
// library shows a shelf: a heading, the count under it, and the books row by
// row.
func (a *app) mobileShelfGrid(c *ui.Context, t *ui.Theme, m homeMetrics, title, caption string, entries []library.Entry, top float32) {
	ui.Column(c).FillWidth().Padding(top, m.padding, 0, m.padding).Children(func() {
		if title != "" {
			ui.Text(c, title).FontSize(20).FontWeight(700).TextColor(t.Text)
		}
		if caption != "" {
			ui.Text(c, caption).FontSize(12).TextColor(t.TextMuted).SingleLine().
				Padding(2, 0, 0, 0)
		}
		ui.Grid(c).FillWidth().Columns(m.cols).Gap(m.coverGap).Padding(14, 0, 0, 0).
			Children(func() {
				for _, e := range entries {
					a.bookCover(c, t, m, e)
				}
			})
	})
}

// mobileSources is what the Mac's source list holds, as the rows of a phone's
// settings-like screen: the way in, and the shops.
func (a *app) mobileSources(c *ui.Context, t *ui.Theme) {
	ui.Scroll(c).Grow(1).MinWidth(0).FillHeight().Background(t.Background).Children(func() {
		ui.Column(c).FillWidth().Padding(8, 0, 28, 0).Gap(22).Children(func() {
			uimobile.Section(c, locales.SidebarLibraryHeading(), func() {
				a.mobileRow(c, t, icons.FilePlus, locales.MenuOpen(), a.chooseFile)
				// The system's picker on a phone hands the app files, not a
				// folder to scan: the desk keeps the folder, and the shop rows
				// below take its place here.
				a.mobileRow(c, t, icons.Refresh, locales.SidebarRecent(), func() {
					if a.pan != nil {
						a.pan.ReloadShelf()
					}
				})
			})
			uimobile.Section(c, locales.SidebarSources(), func() {
				for _, id := range knownSourceIDs {
					id := id
					a.mobileRow(c, t, sourceSheetIcon(id), sourceName(a, id), func() {
						if a.find == nil {
							a.find = &sourceSheet{}
						}
						a.find.openSheet(id)
					})
				}
			})
		})
	})
}

// mobileRow is one row of the sources screen, with the press a finger expects.
func (a *app) mobileRow(c *ui.Context, t *ui.Theme, icon *ui.SVG, label string, onClick func()) {
	row := uimobile.ListRow(c, uimobile.RowOptions{
		Title: label,
		Leading: func() {
			ui.Icon(c, icon).Size(20, 20).TextColor(t.TextMuted)
		},
		Disclosure: true,
	})
	if row.Clicked() {
		onClick()
	}
}

// The library's destinations on a phone: what to read next, the whole shelf,
// the shops, and the shelf's search. The library's filters narrow the shelf
// instead of taking a destination each.
const (
	mobileTabHome = iota
	mobileTabLibrary
	mobileTabSources
	mobileTabSearch
)

// mobileTab is the name and glyph of one destination.
type mobileTab struct {
	label string
	icon  *ui.SVG
}

func mobileTabItems() []mobileTab {
	return []mobileTab{
		{locales.SidebarHome(), icons.House},
		{locales.SidebarLibraryHeading(), icons.Grid},
		{locales.SidebarSources(), icons.Globe},
		{locales.Search(), icons.Search},
	}
}

// mobileTabs is the bar of destinations across the foot of the library, drawn
// down to the screen's edge under the system's own bar.
func (a *app) mobileTabs(c *ui.Context, t *ui.Theme) {
	_, _, bottom, _ := c.SafeArea()
	if bottom > mobileKeyboard {
		// The keyboard covers the bar, as it covers the system's own.
		return
	}
	bar := ui.Column(c).FillWidth().Shrink(0).Background(t.Surface).
		Padding(0, 0, bottom, 0).Margin(0, 0, -bottom, 0)
	bar.Children(func() {
		ui.Box(c).FillWidth().Height(0.5).Background(t.Border)
		ui.Row(c).FillWidth().Height(mobileTabHeight).Children(func() {
			for i, tab := range mobileTabItems() {
				a.mobileTab(c, t, i, tab)
			}
		})
	})
}

// mobileTab is one destination: its glyph and name in the accent while it is
// where the library is, and in the muted ink when it is not.
func (a *app) mobileTab(c *ui.Context, t *ui.Theme, i int, tab mobileTab) {
	on := a.libTab == i
	ink := t.TextMuted
	if on {
		ink = t.Accent
	}
	item := ui.Column(c).Key(i).Grow(1).Basis(0).FillHeight().Center().Gap(2).
		Cursor(ui.CursorPointer).Label(tab.label).Role(ui.RoleTab).Checked(on)
	item.OnClick(func() {
		if a.libTab == i {
			return
		}
		a.libTab = i
		switch i {
		case mobileTabHome:
			a.libFilter = 0
		case mobileTabLibrary:
			if a.libFilter == 0 {
				a.libFilter = 3
			}
		case mobileTabSearch:
			// The tab is a search screen: the field takes the keyboard as it opens.
			a.searchFocus = true
		}
		a.goalsOpen = false
	})
	item.Children(func() {
		ui.Icon(c, tab.icon).Size(23, 23).TextColor(ink)
		ui.Text(c, tab.label).FontSize(10).TextColor(ink).NoWrap()
	})
}

// mobileHeader is the reading bar on a phone: back at the start, the book's
// name in the middle, and the display settings and the ribbon at the end. The
// panels and search moved to the foot, where a thumb reaches them.
func (a *app) mobileHeader(c *ui.Context, t *ui.Theme) {
	// The paper is under it: the column slides beneath this bar, so it is a
	// layer of its own and not a band the text keeps clear of.
	row := ui.Row(c).Absolute().Left(0).Right(0).Top(0).Height(mobileHeaderHeight).
		Padding(0, 4).AlignItems(ui.Center).Background(a.paperColor())
	row.AnimatePose(chromePose(a.chromeTarget()), ui.Animation{Duration: chromeFade})
	row.Children(func() {
		a.mobilePaperButton(c, icons.ChevronLeft, locales.BackToLibrary(), a.leaveBook)
		ui.Spacer(c)
		name := ui.Box(c).Absolute().Left(mobileCell).Right(mobileCell + 4).Top(0).Bottom(0).
			Center().PassThrough()
		name.Children(func() {
			if a.book == nil {
				return
			}
			// The name and what is left of the chapter: the second line is the
			// figure the desk keeps at the foot, and the bar is where a phone has
			// room for it without covering the page.
			ui.Column(c).Gap(0).Center().Children(func() {
				ui.Text(c, a.book.Title).FontSize(15).FontWeight(600).
					TextColor(t.Text).SingleLine().Ellipsis("…")
				if left := a.chapterPagesLeft(); left != "" {
					ui.Text(c, left).FontSize(10).TextColor(t.TextMuted).SingleLine()
				}
			})
		})
		var aa ui.Element
		aa = a.mobilePaperTextButton(c, "Aa", locales.ThemesSettings(), func() {
			a.aaOpen = !a.aaOpen
			a.syncSettingsFields()
		})
		a.settingsPanel(c, aa)
	})
}

// mobilePaperButton is one target on the paper's bar: 44 DIPs, with the wash a
// pressed control takes.
func (a *app) mobilePaperButton(c *ui.Context, icon *ui.SVG, label string, onClick func()) ui.Element {
	pal := a.chrome
	b := ui.Box(c).Size(mobileCell, mobileCell).Radius(mobileCell / 2).Center().
		Cursor(ui.CursorPointer).Label(label).KeepFocus().OnClick(onClick)
	if b.Hovered() {
		b.Background(reader.UIColor(pal.Hover))
	}
	b.Children(func() {
		ui.Icon(c, icon).Size(22, 22).TextColor(reader.UIColor(pal.OnToolbar))
	})
	return b
}

// mobilePaperTextButton is the Aa target, which is its own label.
func (a *app) mobilePaperTextButton(c *ui.Context, text, label string, onClick func()) ui.Element {
	pal := a.chrome
	b := ui.Box(c).Size(mobileCell, mobileCell).Radius(mobileCell / 2).Center().
		Cursor(ui.CursorPointer).Label(label).KeepFocus().Tooltip(label).OnClick(onClick)
	if b.Hovered() {
		b.Background(reader.UIColor(pal.Hover))
	}
	b.Children(func() {
		ui.Text(c, text).FontSize(18).FontWeight(600).TextColor(reader.UIColor(pal.OnToolbar))
	})
	return b
}

// mobileFolio is the foot of a phone's page: the tools a thumb reaches —
// contents, search, the ribbon and the notes — and the book's progress under
// them. The page and what is left of the chapter are not a band of it: they
// float over the paper above the tools (see mobilePageLabels).
func (a *app) mobileFolio(c *ui.Context, t *ui.Theme) {
	pal := a.chrome
	w, _ := c.Size()
	bar := ui.Column(c).Absolute().Left(0).Right(0).Bottom(0).Height(mobileFooterHeight).
		Background(a.paperColor())
	bar.AnimatePose(chromePose(a.chromeTarget()), ui.Animation{Duration: chromeFade})
	bar.Children(func() {
		ui.Row(c).FillWidth().Height(mobileToolHeight).Children(func() {
			a.mobileTool(c, icons.ListBullet, locales.PanelContents(), func() {
				a.togglePanel(appstate.SidebarToc)
			}, a.pan != nil && a.pan.Sidebar == appstate.SidebarToc)
			a.mobileTool(c, icons.Search, locales.Search(), func() {
				a.togglePanel(appstate.SidebarSearch)
			}, a.pan != nil && a.pan.Sidebar == appstate.SidebarSearch)
			on := a.bookmarkedAt()
			label := locales.AddBookmark()
			if on {
				label = locales.RemoveBookmark()
			}
			a.mobileTool(c, icons.Bookmark, label, a.toggleBookmark, on)
			a.mobileTool(c, icons.NoteCard, locales.PanelNotes(), func() {
				a.togglePanel(appstate.SidebarNotes)
			}, a.pan != nil && a.pan.Sidebar == appstate.SidebarNotes)
		})
		ui.Box(c).FillWidth().Height(mobileScrubHeight).Children(func() {
			knob := ui.Box(c).Width(19).Height(6).Radius(3).Background(reader.UIColor(pal.Muted))
			p := float32(a.progress())
			knob.Margin(1, 0, 0, 8+p*(w-16-19))
		})
	})
}

// mobileTool is one tool at the foot of the page, a quarter of its width and a
// finger's height.
func (a *app) mobileTool(c *ui.Context, icon *ui.SVG, label string, onClick func(), on bool) {
	pal := a.chrome
	ink := reader.UIColor(pal.Muted)
	if on {
		ink = reader.UIColor(pal.Accent)
	}
	b := ui.Box(c).Grow(1).Basis(0).FillHeight().Center().
		Cursor(ui.CursorPointer).Label(label).OnClick(onClick)
	if b.Hovered() {
		b.Background(reader.UIColor(pal.Hover))
	}
	b.Children(func() {
		ui.Icon(c, icon).Size(22, 22).TextColor(ink)
	})
}

// mobilePageLabels floats the folio's page over the paper, above the tools at
// the foot: it takes no band of the sheet, so the text keeps its measure. What
// is left of the chapter is the bar's second line under the book's name (see
// mobileHeader), which is where a phone has room for it without covering the
// page.
func (a *app) mobilePageLabels(c *ui.Context, t *ui.Theme) {
	if a.book == nil || a.layout == nil || a.showCover {
		return
	}
	muted := reader.UIColor(a.chrome.Muted)
	label := a.pageLabel()
	if hint := a.selectionHint(); hint != "" {
		label = hint
	} else if msg := a.transientStatus(); msg != "" {
		label = msg
	}
	bottom := ui.Box(c).Absolute().Left(0).Right(0).Bottom(mobileFooterHeight + 2).
		Height(mobileLabelHeight).Center().PassThrough()
	bottom.AnimatePose(chromePose(a.chromeTarget()), ui.Animation{Duration: chromeFade})
	bottom.Children(func() {
		ui.Text(c, label).FontSize(11).TextColor(muted).SingleLine()
	})
	if a.nav.canBack() || a.nav.canForward() {
		// The jumps stay at the foot's start, where a thumb that scrolls the page
		// is not: they are a way back through the places the reader visited.
		nav := ui.Row(c).Absolute().Left(0).Bottom(mobileFooterHeight+2).
			Height(mobileLabelHeight).Padding(0, 8).AlignItems(ui.Center)
		nav.AnimatePose(chromePose(a.chromeTarget()), ui.Animation{Duration: chromeFade})
		nav.Children(func() { a.folioNav(c, t) })
	}
}

// mobileSheet is the phone's shape of a panel: a sheet that comes up from the
// bottom edge, as wide as the screen, with the grabber its system draws, the
// panel's name in the middle and a close control at the right.
//
// detents are the heights it rests at, as shares of the screen. Dragging it
// down, a tap outside it, or back closes it, which sets *open to false: the
// caller keeps its own state in step with that.
func (a *app) mobileSheet(c *ui.Context, open *bool, title string, detents []float32, content func()) {
	t := c.Theme()
	bg := t.Background
	uimobile.Sheet(c, open, uimobile.SheetOptions{
		Detents:    detents,
		Background: &bg,
		Label:      title,
	}, func() {
		a.mobileSheetTitle(c, t, title, open)
		content()
	})
}

// mobileSheetTitle is the bar at the head of a sheet: the name in the middle,
// the close control at the right, and a hairline under them.
func (a *app) mobileSheetTitle(c *ui.Context, t *ui.Theme, title string, open *bool) {
	ui.Row(c).FillWidth().Height(mobileSheetTitleHeight).Padding(0, 8).
		AlignItems(ui.Center).Children(func() {
		ui.Box(c).Width(mobileCell)
		ui.Text(c, title).FontSize(15).FontWeight(600).TextColor(t.Text).
			Grow(1).Basis(0).TextAlign(ui.Center).SingleLine()
		close := ui.Box(c).Size(mobileCell, mobileCell).Radius(mobileCell / 2).Center().
			Cursor(ui.CursorPointer).Label(locales.Close())
		if close.Hovered() {
			close.Background(t.SurfaceHover)
		}
		close.OnClick(func() { *open = false })
		close.Children(func() {
			ui.Icon(c, icons.Close).Size(18, 18).TextColor(t.TextMuted)
		})
	})
	ui.Box(c).FillWidth().Height(1).Background(t.Border)
}

// phoneToasts builds the window's notifications where a phone can see them:
// above the bar at the foot of the screen — the tab bar, or the tools under a
// page — and above an open sheet, which covers that place. The desk keeps the
// theme's own viewport, which already sits at the foot.
func (a *app) phoneToasts(c *ui.Context) {
	if !a.compact(c) {
		return
	}
	t := c.Theme()
	fill, ink := t.Inverse, t.InverseText
	if fill.A == 0 {
		fill, ink = t.Text, t.Background
	}
	_, top, _, bottom := c.SafeArea()
	ui.ToastViewportBase(c, func(viewport ui.Element, toasts []ui.Toast) {
		viewport.Padding(0, mobilePadding).Gap(8).AlignItems(ui.Center)
		switch {
		case a.sheetShowing():
			// A sheet covers the foot of the screen, so its messages stand above
			// it, clear of the status bar.
			viewport.Justify(ui.Start).Padding(top+8, mobilePadding, 0, mobilePadding)
		case a.book != nil:
			// Above the page's own figure, so the two answers do not sit on top of
			// one another.
			viewport.Bottom(a.footerRoom(c) + mobileLabelHeight + 6)
		default:
			viewport.Bottom(mobileTabHeight + bottom + 10)
		}
		for _, ts := range toasts {
			toast := ui.ToastBase(c, ts)
			box := toast.Root.Row().AlignItems(ui.Center).Gap(8).
				Padding(10, 16).Radius(10).MaxWidth(max(widthOf(c)-2*mobilePadding, 200)).
				Background(fill).TextColor(ink)
			box.Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.25))
			box.Children(func() {
				if ts.Description == "" {
					ui.Text(c, ts.Title).FontSize(13).TextColor(ink)
					return
				}
				ui.Column(c).Shrink(1).Gap(2).Children(func() {
					ui.Text(c, ts.Title).FontSize(13).FontWeight(600).TextColor(ink)
					ui.Text(c, ts.Description).FontSize(12).TextColor(ink.Alpha(0.75))
				})
			})
			if left := toast.Left(); left > 0 {
				// The next frame closes it: the engine only takes an expired toast
				// out while building, so ask for the build that does.
				c.After(left)
			}
		}
	})
}

// sheetShowing reports whether a sheet or a dialog is over the window, which is
// where a phone's messages would be hidden.
func (a *app) sheetShowing() bool {
	if a.panSheet || a.aaOpen || a.goalsOpen || a.removeOpen {
		return true
	}
	return a.find != nil && (a.find.shown || a.find.settingsOpen)
}

// phoneMetricsFor measures the library for a W-wide phone: a margin of 16, and
// as many covers side by side as fit at a size a finger can hit, with the
// heading and goals a step smaller than the desk's.
func phoneMetricsFor(w float32) homeMetrics {
	m := homeMetrics{
		padding:  mobilePadding,
		coverGap: mobileCoverGap,
		firstGap: 16,
		bandGap:  24,
		goalsGap: 22,
	}
	usable := w - 2*m.padding
	cols := 3
	if usable < 330 {
		cols = 2
	}
	m.cols = cols
	m.coverW = (usable - float32(cols-1)*m.coverGap) / float32(cols)
	m.coverH = m.coverW * coverAspect
	m.ring = clampf(w*0.30, 104, 150)
	return m
}

// phoneStartColor is the colour the native window fills before the first frame
// paints: the paper of a book window, or the library's own desk.
func (a *app) phoneStartColor() string {
	dark := mygo.Theme.IsDark()
	pal := systemChrome(dark)
	if a.book != nil || a.bookWindow {
		pal = reader.ForTheme(a.paperTheme(dark))
	}
	return fmt.Sprintf("#%02x%02x%02x", pal.Desk.R, pal.Desk.G, pal.Desk.B)
}

// takesFinger gives the page the finger's own moves on a phone, rather than
// letting the toolkit pan the window: a page paints its own text, so dragging
// the scrolling column and dragging out a selection are its gestures, not the
// window's. Without it the toolkit pans, and its fling swallows the touch that
// comes to stop it — a second drag right after the first would do nothing, and
// a long press would pan instead of mark text.
func (a *app) takesFinger(box ui.Element) {
	if !a.phone {
		return
	}
	// Asking where the pointer is is what marks the element as one that follows
	// it (ui's flagTrackPointer); the position itself is of no use here.
	box.PointerPosition()
}
