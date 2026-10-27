package widgets

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/icons"
)

// The row fill is the one thing the shared pieces decide with numbers: a chosen
// row is tinted with the accent, a hovered one with the theme's hover colour, and
// a chosen row keeps its tint while the pointer is over it.
func TestRowBackgroundPicksTheStrongestState(t *testing.T) {
	var (
		hover, accent      ui.Color
		plain, over        ui.Color
		chosen, chosenOver ui.Color
	)
	tt := ui.NewTester(func(c *ui.Context) {
		theme := c.Theme()
		hover, accent = theme.SurfaceHover, theme.Accent
		plain = RowBackground(c, false, false)
		over = RowBackground(c, true, false)
		chosen = RowBackground(c, false, true)
		chosenOver = RowBackground(c, true, true)
	}, 200, 100)
	tt.Frame()

	if plain != ui.Transparent {
		t.Errorf("a plain row is filled with %v, want transparent", plain)
	}
	if over != hover {
		t.Errorf("a hovered row is filled with %v, want the theme's hover %v", over, hover)
	}
	if want := accent.Alpha(0.16); chosen != want {
		t.Errorf("a chosen row is filled with %v, want the accent tint %v", chosen, want)
	}
	if chosenOver != chosen {
		t.Errorf("a chosen row under the pointer is filled with %v, want the chosen tint %v", chosenOver, chosen)
	}
}

// The panel pieces build headlessly: an empty list says what it is waiting for,
// a heading carries its label, and a toolbar glyph is named for assistive
// technology and for tests.
func TestPanelPiecesBuild(t *testing.T) {
	built := func(c *ui.Context) {
		SectionTitle(c, "Chapters")
		Empty(c, "Nothing here", "Open a book first")
		GlyphButton(c, icons.Search, "Find", "Find in the book")
		TextGlyph(c, "Aa", "Display settings", "Themes & Settings")
		IconButton(c, icons.Bookmark, "Bookmark", "Bookmark this page")
		SmallButton(c, "Reset", "Reset the settings")
	}
	tt := ui.NewTester(built, 400, 400)
	tt.Frame()
	for _, want := range []string{"Chapters", "Nothing here", "Open a book first", "Aa", "Reset"} {
		if !tt.HasText(want) {
			t.Errorf("%q is not built: %q", want, tt.Texts())
		}
	}
	for _, label := range []string{"Find", "Display settings", "Bookmark"} {
		if _, ok := tt.Find(label); !ok {
			t.Errorf("the control named %q is missing: %q", label, tt.Texts())
		}
	}
}
