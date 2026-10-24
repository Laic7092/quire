package appstate

import (
	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/store"
	"github.com/laix/layx-reader/internal/widgets"
)

// Showing reports whether a panel is open; the host floats it over the page.
func (a *App) Showing() bool { return a.Sidebar != SidebarNone }

// Title is the name of the open panel, for a window title or a status line.
func (a *App) Title() string {
	switch a.Sidebar {
	case SidebarToc:
		return "目录"
	case SidebarBookmarks:
		return "书签"
	case SidebarNotes:
		return "笔记"
	case SidebarSearch:
		return "搜索"
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
	if a.Sidebar == SidebarToc {
		a.Toc.ScrollToCurrent()
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
			widgets.Empty(c, "还没有书签", "这本书的书签会列在这里")
		case SidebarNotes:
			widgets.Empty(c, "还没有笔记", "这本书的笔记和划线的文字会列在这里")
		default:
			widgets.Empty(c, "没有打开的页面", "")
		}
	})
	return col
}
