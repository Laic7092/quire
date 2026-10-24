package reader

import (
	"sync/atomic"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx/render/paint"
)

// Theme names the paper a book is read on. Books offers six: the page, its
// ink, and the chrome around it, which the display settings print as cards.
type Theme string

const (
	ThemeOriginal Theme = "original"
	ThemeQuiet    Theme = "quiet"
	ThemePaper    Theme = "paper"
	ThemeBold     Theme = "bold"
	ThemeCalm     Theme = "calm"
	ThemeFocus    Theme = "focus"

	// The names the four papers had before the six; state files written then
	// still name a paper the reader knows.
	ThemeWhite = ThemeOriginal
	ThemeGray  = ThemePaper
	ThemeSepia = ThemeCalm
	ThemeNight = ThemeQuiet
)

// ThemeOrder lists the themes in the order the display settings show them:
// two rows of three, as Books prints them.
var ThemeOrder = []Theme{
	ThemeOriginal, ThemeQuiet, ThemePaper,
	ThemeBold, ThemeCalm, ThemeFocus,
}

// Dark reports whether reading on the theme means reading at night, which the
// window's own look follows.
func (t Theme) Dark() bool { return t == ThemeQuiet || t == ThemeBold }

// Palette is every colour a page and the chrome around it are made of: the
// sheet, the ink on it, the veils drawn over that ink, and the desk and bars
// the sheet lies on.
//
// Paper, Ink and Link are baked into a layout's page display lists, so a host
// that changes them asks for a paint-only relayout (Relayout): the document, its
// line boxes and its page maps all stand. The rest are painted by the host, so
// they cost nothing.
type Palette struct {
	Theme Theme

	Paper paint.Color
	Ink   paint.Color
	// Select is drawn over text the reader has dragged over, Match over search
	// hits, and MatchOn over the hit the search panel points at.
	Select  paint.Color
	Match   paint.Color
	MatchOn paint.Color
	// Note is drawn over the passages the reader marked with a note. It is
	// quieter than Match, which the reader asks for and then dismisses: a
	// marked passage sits on the page until it is removed.
	Note paint.Color
	// Link paints link text and the rule under it. It travels with the paper
	// rather than the ink, so a link stays recognisable in every theme; the
	// reader paints it (the document underlines links but names no colour), so a
	// theme change repaints it with the rest.
	Link paint.Color

	// Desk is the space around the page, Chrome the bars, Side the library's own
	// panel, Popover the menus and the display settings. Line separates them,
	// Text writes on them and Muted does so for the captions. Accent is the one
	// strong colour, as Books' is its red.
	Desk     paint.Color
	Side     paint.Color
	Chrome   paint.Color
	Popover  paint.Color
	Hover    paint.Color
	Pressed  paint.Color
	Line     paint.Color
	Text     paint.Color
	Muted    paint.Color
	Accent   paint.Color
	OnAccent paint.Color

	// Raised is the fill of the controls inside a popover: the size row, the
	// theme cards and the row at their foot. Toolbar is the fill of the
	// capsules in the title bar, and OnToolbar the ink and hairlines on them.
	Raised    paint.Color
	Toolbar   paint.Color
	OnToolbar paint.Color
}

var (
	// Original is a sheet of white paper under near-black ink.
	Original = Palette{
		Theme:     ThemeOriginal,
		Paper:     paint.Color{R: 0xff, G: 0xff, B: 0xff, A: 0xff},
		Ink:       paint.Color{R: 0x1c, G: 0x1c, B: 0x1e, A: 0xff},
		Link:      paint.Color{R: 0x1a, G: 0x5f, B: 0xc4, A: 0xff},
		Select:    paint.Color{R: 0x2f, G: 0x6a, B: 0xc8, A: 0x38},
		Match:     paint.Color{R: 0xf2, G: 0xc1, B: 0x4e, A: 0x59},
		MatchOn:   paint.Color{R: 0xf0, G: 0x92, B: 0x2e, A: 0x8a},
		Note:      paint.Color{R: 0xe0, G: 0xa0, B: 0x38, A: 0x33},
		Desk:      paint.Color{R: 0xff, G: 0xff, B: 0xff, A: 0xff},
		Side:      paint.Color{R: 0xeb, G: 0xea, B: 0xea, A: 0xff},
		Chrome:    paint.Color{R: 0xff, G: 0xff, B: 0xff, A: 0xff},
		Popover:   paint.Color{R: 0xff, G: 0xff, B: 0xff, A: 0xff},
		Hover:     paint.Color{R: 0xe4, G: 0xe3, B: 0xe3, A: 0xff},
		Pressed:   paint.Color{R: 0xd6, G: 0xd5, B: 0xd5, A: 0xff},
		Line:      paint.Color{R: 0xd9, G: 0xd8, B: 0xd8, A: 0xff},
		Text:      paint.Color{R: 0x1c, G: 0x1c, B: 0x1e, A: 0xff},
		Muted:     paint.Color{R: 0x8a, G: 0x8a, B: 0x8e, A: 0xff},
		Accent:    paint.Color{R: 0xc0, G: 0x50, B: 0x39, A: 0xff},
		OnAccent:  paint.Color{R: 0xff, G: 0xfa, B: 0xf6, A: 0xff},
		Raised:    paint.Color{R: 0xef, G: 0xef, B: 0xf0, A: 0xff},
		Toolbar:   paint.Color{R: 0xef, G: 0xef, B: 0xf0, A: 0xff},
		OnToolbar: paint.Color{R: 0x2c, G: 0x2c, B: 0x2e, A: 0xff},
	}
	// Quiet is the graphite page: Books' first dark paper, and the one the
	// reader's own screenshot shows. Its chrome is the same graphite, one step
	// below the sheet.
	Quiet = Palette{
		Theme:     ThemeQuiet,
		Paper:     paint.Color{R: 0x4a, G: 0x4a, B: 0x4d, A: 0xff},
		Ink:       paint.Color{R: 0xe8, G: 0xe8, B: 0xee, A: 0xff},
		Link:      paint.Color{R: 0x9e, G: 0xc4, B: 0xff, A: 0xff},
		Select:    paint.Color{R: 0xf2, G: 0xc1, B: 0x4e, A: 0x59},
		Match:     paint.Color{R: 0xd8, G: 0xac, B: 0x45, A: 0x60},
		MatchOn:   paint.Color{R: 0xe8, G: 0xb4, B: 0x3c, A: 0x8c},
		Note:      paint.Color{R: 0xe0, G: 0xa0, B: 0x38, A: 0x30},
		Desk:      paint.Color{R: 0x4a, G: 0x4a, B: 0x4d, A: 0xff},
		Side:      paint.Color{R: 0x2e, G: 0x2e, B: 0x30, A: 0xff},
		Chrome:    paint.Color{R: 0x4a, G: 0x4a, B: 0x4d, A: 0xff},
		Popover:   paint.Color{R: 0x30, G: 0x30, B: 0x33, A: 0xff},
		Hover:     paint.Color{R: 0x55, G: 0x55, B: 0x59, A: 0xff},
		Pressed:   paint.Color{R: 0x48, G: 0x48, B: 0x4d, A: 0xff},
		Line:      paint.Color{R: 0x41, G: 0x41, B: 0x46, A: 0xff},
		Text:      paint.Color{R: 0xe8, G: 0xe8, B: 0xee, A: 0xff},
		Muted:     paint.Color{R: 0xae, G: 0xae, B: 0xb2, A: 0xff},
		Accent:    paint.Color{R: 0xd9, G: 0x7a, B: 0x5e, A: 0xff},
		OnAccent:  paint.Color{R: 0x1b, G: 0x14, B: 0x10, A: 0xff},
		Raised:    paint.Color{R: 0x4c, G: 0x4c, B: 0x51, A: 0xff},
		Toolbar:   paint.Color{R: 0x31, G: 0x31, B: 0x33, A: 0xff},
		OnToolbar: paint.Color{R: 0xe0, G: 0xe0, B: 0xe6, A: 0xff},
	}
	// Paper is the same white page with the glare taken off.
	Paper = Palette{
		Theme:     ThemePaper,
		Paper:     paint.Color{R: 0xee, G: 0xed, B: 0xed, A: 0xff},
		Ink:       paint.Color{R: 0x1c, G: 0x1c, B: 0x1e, A: 0xff},
		Link:      paint.Color{R: 0x1a, G: 0x5f, B: 0xc4, A: 0xff},
		Select:    paint.Color{R: 0x2f, G: 0x6a, B: 0xc8, A: 0x40},
		Match:     paint.Color{R: 0xf2, G: 0xc1, B: 0x4e, A: 0x60},
		MatchOn:   paint.Color{R: 0xf0, G: 0x92, B: 0x2e, A: 0x90},
		Note:      paint.Color{R: 0xe0, G: 0xa0, B: 0x38, A: 0x33},
		Desk:      paint.Color{R: 0xee, G: 0xed, B: 0xed, A: 0xff},
		Side:      paint.Color{R: 0xeb, G: 0xea, B: 0xea, A: 0xff},
		Chrome:    paint.Color{R: 0xee, G: 0xed, B: 0xed, A: 0xff},
		Popover:   paint.Color{R: 0xf5, G: 0xf5, B: 0xf6, A: 0xff},
		Hover:     paint.Color{R: 0xe4, G: 0xe3, B: 0xe3, A: 0xff},
		Pressed:   paint.Color{R: 0xd6, G: 0xd5, B: 0xd5, A: 0xff},
		Line:      paint.Color{R: 0xd9, G: 0xd8, B: 0xd8, A: 0xff},
		Text:      paint.Color{R: 0x1c, G: 0x1c, B: 0x1e, A: 0xff},
		Muted:     paint.Color{R: 0x8a, G: 0x8a, B: 0x8e, A: 0xff},
		Accent:    paint.Color{R: 0xc0, G: 0x50, B: 0x39, A: 0xff},
		OnAccent:  paint.Color{R: 0xff, G: 0xfa, B: 0xf6, A: 0xff},
		Raised:    paint.Color{R: 0xef, G: 0xef, B: 0xf0, A: 0xff},
		Toolbar:   paint.Color{R: 0xef, G: 0xef, B: 0xf0, A: 0xff},
		OnToolbar: paint.Color{R: 0x2c, G: 0x2c, B: 0x2e, A: 0xff},
	}
	// Bold is the page after dark: the black paper Books offers.
	Bold = Palette{
		Theme:     ThemeBold,
		Paper:     paint.Color{R: 0x29, G: 0x29, B: 0x2b, A: 0xff},
		Ink:       paint.Color{R: 0xf2, G: 0xf2, B: 0xf7, A: 0xff},
		Link:      paint.Color{R: 0x9e, G: 0xc4, B: 0xff, A: 0xff},
		Select:    paint.Color{R: 0xf2, G: 0xc1, B: 0x4e, A: 0x55},
		Match:     paint.Color{R: 0xd8, G: 0xac, B: 0x45, A: 0x60},
		MatchOn:   paint.Color{R: 0xe8, G: 0xb4, B: 0x3c, A: 0x8c},
		Note:      paint.Color{R: 0xe0, G: 0xa0, B: 0x38, A: 0x30},
		Desk:      paint.Color{R: 0x29, G: 0x29, B: 0x2b, A: 0xff},
		Side:      paint.Color{R: 0x1c, G: 0x1c, B: 0x1e, A: 0xff},
		Chrome:    paint.Color{R: 0x29, G: 0x29, B: 0x2b, A: 0xff},
		Popover:   paint.Color{R: 0x21, G: 0x21, B: 0x23, A: 0xff},
		Hover:     paint.Color{R: 0x3b, G: 0x3b, B: 0x3f, A: 0xff},
		Pressed:   paint.Color{R: 0x35, G: 0x35, B: 0x39, A: 0xff},
		Line:      paint.Color{R: 0x2f, G: 0x2f, B: 0x32, A: 0xff},
		Text:      paint.Color{R: 0xf2, G: 0xf2, B: 0xf7, A: 0xff},
		Muted:     paint.Color{R: 0xa8, G: 0xa8, B: 0xae, A: 0xff},
		Accent:    paint.Color{R: 0xd9, G: 0x7a, B: 0x5e, A: 0xff},
		OnAccent:  paint.Color{R: 0x1b, G: 0x14, B: 0x10, A: 0xff},
		Raised:    paint.Color{R: 0x3a, G: 0x3a, B: 0x3e, A: 0xff},
		Toolbar:   paint.Color{R: 0x2f, G: 0x2f, B: 0x32, A: 0xff},
		OnToolbar: paint.Color{R: 0xe8, G: 0xe8, B: 0xee, A: 0xff},
	}
	// Calm is the warm paper Books made its name on.
	Calm = Palette{
		Theme:     ThemeCalm,
		Paper:     paint.Color{R: 0xf0, G: 0xe2, B: 0xc9, A: 0xff},
		Ink:       paint.Color{R: 0x2a, G: 0x24, B: 0x1c, A: 0xff},
		Link:      paint.Color{R: 0x1c, G: 0x55, B: 0xa8, A: 0xff},
		Select:    paint.Color{R: 0x9a, G: 0x6d, B: 0x2f, A: 0x55},
		Match:     paint.Color{R: 0xf2, G: 0xc1, B: 0x4e, A: 0x59},
		MatchOn:   paint.Color{R: 0xf0, G: 0x92, B: 0x2e, A: 0x8a},
		Note:      paint.Color{R: 0xc0, G: 0x8a, B: 0x30, A: 0x38},
		Desk:      paint.Color{R: 0xf0, G: 0xe2, B: 0xc9, A: 0xff},
		Side:      paint.Color{R: 0xeb, G: 0xea, B: 0xea, A: 0xff},
		Chrome:    paint.Color{R: 0xf0, G: 0xe2, B: 0xc9, A: 0xff},
		Popover:   paint.Color{R: 0xff, G: 0xfc, B: 0xf6, A: 0xff},
		Hover:     paint.Color{R: 0xe4, G: 0xe3, B: 0xe3, A: 0xff},
		Pressed:   paint.Color{R: 0xd6, G: 0xd5, B: 0xd5, A: 0xff},
		Line:      paint.Color{R: 0xd9, G: 0xd8, B: 0xd8, A: 0xff},
		Text:      paint.Color{R: 0x2a, G: 0x24, B: 0x1c, A: 0xff},
		Muted:     paint.Color{R: 0x8a, G: 0x7d, B: 0x6a, A: 0xff},
		Accent:    paint.Color{R: 0x9a, G: 0x56, B: 0x36, A: 0xff},
		OnAccent:  paint.Color{R: 0xff, G: 0xfa, B: 0xf3, A: 0xff},
		Raised:    paint.Color{R: 0xef, G: 0xef, B: 0xf0, A: 0xff},
		Toolbar:   paint.Color{R: 0xef, G: 0xef, B: 0xf0, A: 0xff},
		OnToolbar: paint.Color{R: 0x2c, G: 0x2c, B: 0x2e, A: 0xff},
	}
	// Focus is the cream page: the warmest of the light papers.
	Focus = Palette{
		Theme:     ThemeFocus,
		Paper:     paint.Color{R: 0xff, G: 0xfc, B: 0xf4, A: 0xff},
		Ink:       paint.Color{R: 0x2b, G: 0x2a, B: 0x26, A: 0xff},
		Link:      paint.Color{R: 0x1c, G: 0x55, B: 0xa8, A: 0xff},
		Select:    paint.Color{R: 0x2f, G: 0x6a, B: 0xc8, A: 0x38},
		Match:     paint.Color{R: 0xf2, G: 0xc1, B: 0x4e, A: 0x59},
		MatchOn:   paint.Color{R: 0xf0, G: 0x92, B: 0x2e, A: 0x8a},
		Note:      paint.Color{R: 0xe0, G: 0xa0, B: 0x38, A: 0x33},
		Desk:      paint.Color{R: 0xff, G: 0xfc, B: 0xf4, A: 0xff},
		Side:      paint.Color{R: 0xeb, G: 0xea, B: 0xea, A: 0xff},
		Chrome:    paint.Color{R: 0xff, G: 0xfc, B: 0xf4, A: 0xff},
		Popover:   paint.Color{R: 0xff, G: 0xfd, B: 0xf8, A: 0xff},
		Hover:     paint.Color{R: 0xe4, G: 0xe3, B: 0xe3, A: 0xff},
		Pressed:   paint.Color{R: 0xd6, G: 0xd5, B: 0xd5, A: 0xff},
		Line:      paint.Color{R: 0xd9, G: 0xd8, B: 0xd8, A: 0xff},
		Text:      paint.Color{R: 0x2b, G: 0x2a, B: 0x26, A: 0xff},
		Muted:     paint.Color{R: 0x8a, G: 0x87, B: 0x7e, A: 0xff},
		Accent:    paint.Color{R: 0xc0, G: 0x50, B: 0x39, A: 0xff},
		OnAccent:  paint.Color{R: 0xff, G: 0xfa, B: 0xf6, A: 0xff},
		Raised:    paint.Color{R: 0xef, G: 0xef, B: 0xf0, A: 0xff},
		Toolbar:   paint.Color{R: 0xef, G: 0xef, B: 0xf0, A: 0xff},
		OnToolbar: paint.Color{R: 0x2c, G: 0x2c, B: 0x2e, A: 0xff},
	}

	// The old names, so code written for four papers keeps its palette.
	White = Original
	Gray  = Paper
	Sepia = Calm
	Night = Quiet

	current atomic.Pointer[Palette]
)

func init() { SetPalette(Quiet) }

// ParseTheme turns a theme name into a paper, accepting the four names the
// earlier palettes were saved under too; "" for a name it does not know.
func ParseTheme(name string) Theme {
	switch Theme(name) {
	case ThemeOriginal, ThemeQuiet, ThemePaper, ThemeBold, ThemeCalm, ThemeFocus:
		return Theme(name)
	}
	switch name {
	case "white":
		return ThemeOriginal
	case "sepia":
		return ThemeCalm
	case "gray":
		return ThemePaper
	case "night":
		return ThemeQuiet
	}
	return ""
}

// ForTheme returns the palette of t, or Quiet for a name it does not know.
func ForTheme(t Theme) Palette {
	switch t {
	case ThemeOriginal:
		return Original
	case ThemePaper:
		return Paper
	case ThemeBold:
		return Bold
	case ThemeCalm:
		return Calm
	case ThemeFocus:
		return Focus
	}
	return Quiet
}

// System picks the theme a window opens on: Quiet when the system is dark, the
// white paper otherwise.
func System(dark bool) Theme {
	if dark {
		return ThemeQuiet
	}
	return ThemeOriginal
}

// SetPalette makes p the colours the next layout and the next cover use. It is
// safe to call while a layout runs: that layout keeps the palette it started
// with.
func SetPalette(p Palette) { current.Store(&p) }

// Current returns the palette in use.
func Current() Palette {
	if p := current.Load(); p != nil {
		return *p
	}
	return Quiet
}

// UIColor converts a colour from the renderer's paint to the UI's, so the
// chrome and the page come out of one palette.
func UIColor(c paint.Color) ui.Color {
	if c.A == 0xff {
		return ui.RGB(c.R, c.G, c.B)
	}
	return ui.RGBA(c.R, c.G, c.B, float32(c.A)/255)
}
