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
	mobileFooterHeight = 66
	mobileCell         = 44
	mobileToolHeight   = 40
	mobileLabelHeight  = 18
	mobileScrubHeight  = 8
	mobileTabHeight    = 50

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
		if a.libTab != mobileTabSources {
			a.mobileSearch(c, t)
		}
		if a.libTab == mobileTabSources {
			a.mobileSources(c, t)
		} else {
			a.mobileHome(c, t, m)
		}
		a.mobileTabs(c, t)
	})
}

// widthOf is the window's width, or 0 before it is measured.
func widthOf(c *ui.Context) float32 {
	w, _ := c.Size()
	return w
}

// mobileTopBar is the large title of a phone's library, with the search at its
// trailing end. It is the system's shape drawn by the app, so a desktop build
// with the phone layout sees the same thing.
func (a *app) mobileTopBar(c *ui.Context, t *ui.Theme) {
	title := a.homeTitle()
	if a.libTab == mobileTabSources {
		title = locales.SidebarSources()
	}
	o := uimobile.TopBarOptions{Title: title, Large: true}
	if a.libTab != mobileTabSources {
		o.Trailing = func() {
			a.mobileTopButton(c, icons.Search, locales.Search(), func() {
				a.libSearch = !a.libSearch
				if !a.libSearch {
					a.libQuery = ""
				}
			})
		}
	}
	uimobile.TopBar(c, o)
}

// mobileSearch is the library's search field under the title, while the
// magnifier in the bar is on.
func (a *app) mobileSearch(c *ui.Context, t *ui.Theme) {
	if !a.libSearch {
		return
	}
	ui.Row(c).FillWidth().Padding(0, mobilePadding, 10, mobilePadding).Children(func() {
		field := ui.SearchField(c, &a.libQuery).Label(locales.LibrarySearchLabel()).
			Placeholder(locales.LibrarySearchPlaceholder()).FillWidth()
		if field.Submitted() {
			field.Focus()
		}
	})
}

// mobileHome is the shelf in the middle of the phone: what is being read, then
// the books in a grid, then how the reading year is going.
func (a *app) mobileHome(c *ui.Context, t *ui.Theme, m homeMetrics) {
	entries := a.shelf()
	ui.Scroll(c).Grow(1).MinWidth(0).FillHeight().Background(t.Background).Children(func() {
		ui.Column(c).FillWidth().Padding(0, 0, 28, 0).Children(func() {
			if a.notice != "" || (a.status != "" && a.failed) {
				ui.Column(c).FillWidth().Padding(4, m.padding, 0, m.padding).Children(func() {
					if a.notice != "" {
						ui.Text(c, a.notice).FontSize(12).TextColor(t.TextMuted)
					}
					if a.status != "" && a.failed {
						ui.Text(c, a.status).FontSize(12).TextColor(t.Danger)
					}
				})
			}
			if a.pan == nil || a.pan.Library == nil || len(entries) == 0 {
				a.homeEmpty(c, t, m)
				return
			}
			if a.libFilter == 0 {
				top := m.firstGap
				if recent := a.byLastRead(a.inProgress(entries)); len(recent) > 0 {
					a.shelfBand(c, t, m, 0, m.firstGap, locales.ContinueReading(),
						locales.BooksInProgress(len(recent)), recent)
					top = m.bandGap
				}
				a.mobileShelfGrid(c, t, m, locales.AllBooks(), a.libraryLine(), entries, top)
				a.goalsSection(c, t, m)
				return
			}
			a.mobileShelfGrid(c, t, m, a.homeTitle(), a.libraryLine(), entries, m.firstGap)
		})
	})
}

// mobileShelfGrid is one band of the shelf as a grid of covers, as the system's
// library shows a shelf: a heading, the count under it, and the books row by
// row.
func (a *app) mobileShelfGrid(c *ui.Context, t *ui.Theme, m homeMetrics, title, caption string, entries []library.Entry, top float32) {
	ui.Column(c).FillWidth().Padding(top, m.padding, 0, m.padding).Children(func() {
		ui.Text(c, title).FontSize(20).FontWeight(700).TextColor(t.Text)
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

// mobileTopButton is one target in the library's title bar: 44 DIPs of a
// finger's room around a 22 DIP glyph.
func (a *app) mobileTopButton(c *ui.Context, icon *ui.SVG, label string, onClick func()) ui.Element {
	t := c.Theme()
	b := ui.Box(c).Size(mobileCell, mobileCell).Radius(mobileCell / 2).Center().
		Cursor(ui.CursorPointer).Label(label).KeepFocus().OnClick(onClick)
	if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	b.Children(func() {
		ui.Icon(c, icon).Size(22, 22).TextColor(t.Text)
	})
	return b
}

// The library's destinations on a phone. They are the source list's filters and
// its way into the shops, as the system's own library has a bar at its foot.
const (
	mobileTabHome = iota
	mobileTabAll
	mobileTabReading
	mobileTabSources
)

// mobileTab is the name and glyph of one destination.
type mobileTab struct {
	label string
	icon  *ui.SVG
}

func mobileTabItems() []mobileTab {
	return []mobileTab{
		{locales.SidebarHome(), icons.House},
		{locales.SidebarAll(), icons.Grid},
		{locales.SidebarReading(), icons.Half},
		{locales.SidebarSources(), icons.Globe},
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
		case mobileTabAll:
			a.libFilter = 3
		case mobileTabReading:
			a.libFilter = 1
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
		name := ui.Box(c).Absolute().Left(mobileCell).Right(2*mobileCell + 4).Top(0).Bottom(0).
			Center().PassThrough()
		name.Children(func() {
			if a.book != nil {
				ui.Text(c, a.book.Title).FontSize(15).FontWeight(600).
					TextColor(t.Text).SingleLine().Ellipsis("…")
			}
		})
		var aa ui.Element
		aa = a.mobilePaperTextButton(c, "Aa", locales.ThemesSettings(), func() {
			a.aaOpen = !a.aaOpen
			a.syncSettingsFields()
		})
		a.settingsPanel(c, aa)
		on := a.bookmarkedAt()
		label, tip := locales.AddBookmark(), locales.AddBookmarkTip()
		if on {
			label, tip = locales.RemoveBookmark(), locales.RemoveBookmarkTip()
		}
		a.mobilePaperButtonColored(c, icons.Bookmark, label, tip, func() { a.toggleBookmark() }, on)
	})
}

// mobilePaperButton is one target on the paper's bar: 44 DIPs, with the wash a
// pressed control takes.
func (a *app) mobilePaperButton(c *ui.Context, icon *ui.SVG, label string, onClick func()) ui.Element {
	return a.mobilePaperButtonColored(c, icon, label, "", onClick, false)
}

// mobilePaperButtonColored is mobilePaperButton, its glyph in the accent when
// the control is on, as the ribbon is while the page is bookmarked.
func (a *app) mobilePaperButtonColored(c *ui.Context, icon *ui.SVG, label, tip string, onClick func(), on bool) ui.Element {
	pal := a.chrome
	b := ui.Box(c).Size(mobileCell, mobileCell).Radius(mobileCell / 2).Center().
		Cursor(ui.CursorPointer).Label(label).KeepFocus().OnClick(onClick)
	if tip != "" {
		b.Tooltip(tip)
	}
	if b.Hovered() {
		b.Background(reader.UIColor(pal.Hover))
	}
	ink := reader.UIColor(pal.OnToolbar)
	if on {
		ink = reader.UIColor(pal.Accent)
	}
	b.Children(func() {
		ui.Icon(c, icon).Size(22, 22).TextColor(ink)
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

// mobileFolio is the foot of a phone's page: the place and the chapter in small
// type, then the tools a thumb reaches — contents, search, the ribbon and the
// notes — and the book's progress under them.
func (a *app) mobileFolio(c *ui.Context, t *ui.Theme) {
	pal := a.chrome
	w, _ := c.Size()
	bar := ui.Column(c).Absolute().Left(0).Right(0).Bottom(0).Height(mobileFooterHeight).
		Background(a.paperColor())
	bar.AnimatePose(chromePose(a.chromeTarget()), ui.Animation{Duration: chromeFade})
	bar.Children(func() {
		ui.Row(c).FillWidth().Height(mobileLabelHeight).Padding(0, mobilePadding).
			AlignItems(ui.Center).Children(func() {
			a.folioNav(c, t)
			ui.Spacer(c)
			label := a.pageLabel()
			if hint := a.selectionHint(); hint != "" && !a.showCover {
				label = hint
			} else if msg := a.transientStatus(); msg != "" {
				label = msg
			}
			ui.Text(c, label).FontSize(11).TextColor(reader.UIColor(pal.Muted)).SingleLine()
			ui.Spacer(c)
			if left := a.chapterPagesLeft(); left != "" && a.layout != nil {
				ui.Text(c, left).FontSize(11).TextColor(reader.UIColor(pal.Muted)).SingleLine()
			}
		})
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
