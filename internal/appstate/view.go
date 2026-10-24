package appstate

import (
	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/icons"
	"github.com/laix/layx-reader/internal/store"
)

// Panel indices in the sidebar's segmented control, in the order it shows them.
const (
	panelToc = iota
	panelSearch
	panelShelf
)

var panelNames = [3]string{SidebarToc, SidebarSearch, SidebarShelf}

// Showing reports whether a panel is open; the host narrows the reader when it
// is.
func (a *App) Showing() bool { return a.Sidebar != SidebarNone }

// Title is the name of the open panel, for a window title or a status line.
func (a *App) Title() string {
	switch a.Sidebar {
	case SidebarShelf:
		return "书架"
	case SidebarToc:
		return "目录"
	case SidebarSearch:
		return "搜索"
	}
	return ""
}

// Show opens side, replacing whatever panel was there. An empty side closes the
// sidebar and remembers what was open.
func (a *App) Show(side string) {
	switch side {
	case SidebarShelf, SidebarToc, SidebarSearch:
		a.Sidebar = side
	default:
		a.Sidebar = SidebarNone
	}
	a.persistSidebar()
	if a.Sidebar == SidebarShelf && a.Library != nil && len(a.Library.Entries) == 0 {
		a.ReloadShelf()
	}
	if a.Sidebar == SidebarToc {
		a.Toc.ScrollToCurrent()
	}
}

// panelIndex is where the segmented control's thumb sits: the open panel, or
// the last one when the sidebar is closed.
func (a *App) panelIndex() int {
	side := a.Sidebar
	if side == SidebarNone && a.Store != nil {
		side = a.Store.State().Prefs.Sidebar
	}
	for i, name := range panelNames {
		if name == side {
			return i
		}
	}
	return panelToc
}

func (a *App) persistSidebar() {
	if a.Store == nil {
		return
	}
	open := a.Sidebar != SidebarNone
	last := a.Sidebar
	a.Store.Update(func(s *store.State) {
		s.Prefs.SidebarOpen = open
		if open {
			s.Prefs.Sidebar = last
		}
	})
	a.Store.SaveSoon(0)
}

// View draws the sidebar: the segmented control that switches panels, then the
// open panel. It is rebuilt from the App's fields every pass and keeps nothing
// but the widgets' own state, so it is safe to call any number of times per
// frame.
func (a *App) View(c *ui.Context) ui.Element {
	t := c.Theme()
	col := ui.Column(c).Fill().Background(t.Surface)
	col.Children(func() {
		ui.Column(c).Padding(8, 10).Children(func() {
			a.switcher(c)
		})
		ui.Divider(c)
		switch a.Sidebar {
		case SidebarShelf:
			a.Shelf.View(c)
		case SidebarToc:
			a.Toc.View(c)
		case SidebarSearch:
			a.Search.View(c)
		}
	})
	return col
}

// switcher is the three-way control that picks the panel. Choosing the segment
// that is already chosen leaves the panel open; closing it is the toolbar's
// job, so a click never hides the sidebar by surprise.
func (a *App) switcher(c *ui.Context) {
	t := c.Theme()
	before := a.panelIndex()
	index := before
	seg := ui.SegmentedBase(c, &index, len(panelNames))
	seg.Track.FillWidth().Padding(1).Gap(2).Children(func() {
		titles := [3]string{"目录", "搜索", "书架"}
		glyphs := [3]*ui.SVG{icons.ListTree, icons.Search, icons.Book}
		for i := range titles {
			s := seg.Segment(i).Grow(1).Padding(5, 4).Radius(6).Label(titles[i])
			text := t.TextMuted
			if i == index {
				s.Background(t.SurfaceHover)
				text = t.Text
			}
			s.Children(func() {
				ui.Row(c).Gap(4).Center().Children(func() {
					ui.Icon(c, glyphs[i]).Size(15, 15).TextColor(text)
					ui.Text(c, titles[i]).FontSize(12).TextColor(text).SingleLine()
				})
			})
		}
	})
	// Compare the choice rather than asking Changed: a control reports input
	// from its first frame in some builds, and a stray write would open the
	// sidebar and overwrite what the user had closed.
	if index != before {
		a.Show(panelNames[index])
	}
}
