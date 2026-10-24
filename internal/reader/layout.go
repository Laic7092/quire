package reader

import (
	"github.com/laix/layx/render"
)

const (
	// Margin is the page's side margin, in CSS pixels (DIPs), measured off
	// Books' own reading view at its 787 DIP window: the text column is 318
	// wide inside a 372 DIP page.
	Margin = 27
	// PageTop and PageBottom are the page's vertical insets: a book sits its
	// text closer to the head than to the folio, and the extra room at the
	// foot is where Books keeps the page's shadow.
	PageTop    = 8
	PageBottom = 38
	// DefaultFontSize is the reading size in CSS pixels.
	DefaultFontSize = 17
)

// ChapterOfPage maps a global page number to the chapter that starts it. The
// book converter anchors chapter i with AnchorID(i+1), so Locate/PageOf find
// each chapter's first page exactly.
func ChapterOfPage(l *render.Layout, chapters, page int) int {
	if l == nil || chapters <= 0 {
		return 0
	}
	for i := chapters - 1; i >= 0; i-- {
		loc, ok := l.Locate(render.AnchorID(i + 1))
		if !ok {
			continue
		}
		first, ok := l.PageOf(loc)
		if ok && first <= page {
			return i
		}
	}
	return 0
}
