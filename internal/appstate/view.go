package appstate

import (
	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/store"
	"github.com/laix/layx-reader/internal/widgets"
	"github.com/laix/layx-reader/locales"
)

// Showing reports whether a panel is open; the host floats it over the page.
func (a *App) Showing() bool { return a.Sidebar != SidebarNone }

// Title is the name of the open panel, for a window title or a status line.
func (a *App) Title() string {
	switch a.Sidebar {
	case SidebarToc:
		return locales.PanelContents()
	case SidebarBookmarks:
		return locales.PanelBookmarks()
	case SidebarNotes:
		return locales.PanelNotes()
	case SidebarSearch:
		return locales.Search()
	}
	return ""
}

// Show opens side, replacing whatever panel was there. An empty side closes the
// panel and remembers what was open.
func (a *App) Show(side string) {
	switch side {
	case SidebarToc, SidebarBookmarks, SidebarNotes, SidebarSearch:
		a.Sidebar = side
	default:
		a.Sidebar = SidebarNone
	}
	a.persistSidebar()
	switch a.Sidebar {
	case SidebarToc:
		a.Toc.ScrollToCurrent()
	case SidebarBookmarks:
		a.Bookmarks.ScrollToCurrent()
	case SidebarNotes:
		a.Notes.ScrollToCurrent()
	}
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

// View draws the open panel. The host floats it over the page in a card of its
// own, so the panel draws no background and takes no part in the page's layout.
// It is rebuilt from the App's fields every pass and keeps nothing but the
// widgets' own state, so it is safe to call any number of times per frame.
func (a *App) View(c *ui.Context) ui.Element {
	col := ui.Column(c).Fill()
	col.Children(func() {
		switch a.Sidebar {
		case SidebarToc:
			a.Toc.View(c)
		case SidebarSearch:
			a.Search.View(c)
		case SidebarBookmarks:
			a.Bookmarks.View(c)
		case SidebarNotes:
			a.Notes.View(c)
		default:
			widgets.Empty(c, locales.EmptyPanel(), "")
		}
	})
	return col
}
