package icons

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// Every icon parses at package init; touching them here proves none of the SVGs
// is malformed, which MustParseSVG would otherwise only reveal in the window.
func TestIconsParse(t *testing.T) {
	all := map[string]*ui.SVG{
		"Book":         Book,
		"BookOpen":     BookOpen,
		"ListTree":     ListTree,
		"Search":       Search,
		"PanelLeft":    PanelLeft,
		"Folder":       Folder,
		"FilePlus":     FilePlus,
		"Refresh":      Refresh,
		"ChevronLeft":  ChevronLeft,
		"ChevronRight": ChevronRight,
		"ChevronsLeft": ChevronsLeft,
		"Minus":        Minus,
		"Plus":         Plus,
		"Close":        Close,
		"Type":         Type,
		"NoteCard":     NoteCard,
	}
	for name, icon := range all {
		if icon == nil {
			t.Errorf("%s is nil", name)
		}
	}
}
