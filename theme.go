package main

import (
	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/reader"
	"github.com/laix/layx/render/paint"
)

// pagePaper is what a layout's pages are painted with: the sheet, the ink and
// the colour of links. They are baked into the page display lists, so a change
// repaints the pages — a paint-only relayout — instead of rebuilding the book.
type pagePaper struct {
	paper, ink, link paint.Color
}

// paperOf is the colours the pages of a palette are printed in.
func paperOf(p reader.Palette) pagePaper {
	return pagePaper{paper: p.Paper, ink: p.Ink, link: p.Link}
}

// skinKey is what a built skin depends on: the paper, and the system bits the
// theme starts from (its appearance, accent and text size). Keeping the last one
// means the window's look is built once per change, not once per frame.
type skinKey struct {
	paper  reader.Theme
	dark   bool
	accent ui.Color
	font   float32
	// library tells that the window had no book open when the skin was built:
	// the library's own palette is not the paper's, so switching between the
	// two has to rebuild the skin even when the paper did not change.
	library bool
}

// paperTheme is the paper being read on: the one the reader picked, or the one
// the system's appearance suggests until they pick one.
func (a *app) paperTheme(dark bool) reader.Theme {
	if a.themeSys || a.theme == "" {
		return reader.System(dark)
	}
	return a.theme
}

// applyTheme installs the window's own look for this frame: the reading theme's
// colours over MyGo's, so the chrome, the panels and the page all come out of one
// palette, and remembers the colours the page is drawn on.
//
// The library is the one exception, as it is in Books: with nothing open the
// window is chrome, and it keeps the system's own light or dark appearance
// whatever paper the last book was printed on. Opening a book turns the window
// into the page.
func (a *app) applyTheme(c *ui.Context) *ui.Theme {
	base := *c.Theme()
	paper := a.paperTheme(base.Dark)
	pal := reader.ForTheme(paper)
	if a.book == nil {
		pal = systemChrome(base.Dark)
	}
	a.chrome = pal
	key := skinKey{paper: paper, dark: base.Dark, accent: base.Accent, font: base.FontSize, library: a.book == nil}
	if !a.skinSet || key != a.skinKey {
		a.skin = readerTheme(base, a.chrome)
		a.skinSet = true
		a.skinKey = key
	}
	c.SetTheme(&a.skin)
	// The page's ink and paper are baked into the page display lists, so a
	// change of paper costs a paint-only relayout: ensureLayout sees the new
	// palette on this frame and the pages on screen are drawn again.
	if reader.Current().Theme != paper {
		reader.SetPalette(reader.ForTheme(paper))
	}
	return &a.skin
}

// systemChrome is the window with nothing open: the desktop's own light or
// dark appearance rather than one of the four papers. Apple Books' home is
// white (graphite after dark) whatever paper the last book was printed on,
// and its rings are the system blue, so the library gets a palette of its own
// instead of borrowing the reader's.
func systemChrome(dark bool) reader.Palette {
	if dark {
		return reader.Palette{
			Theme:    reader.ThemeNight,
			Paper:    paint.Color{R: 0x1c, G: 0x1c, B: 0x1e, A: 0xff},
			Ink:      paint.Color{R: 0xf2, G: 0xf2, B: 0xf7, A: 0xff},
			Desk:     paint.Color{R: 0x1c, G: 0x1c, B: 0x1e, A: 0xff},
			Side:     paint.Color{R: 0x2c, G: 0x2c, B: 0x2e, A: 0xff},
			Chrome:   paint.Color{R: 0x1c, G: 0x1c, B: 0x1e, A: 0xff},
			Popover:  paint.Color{R: 0x2c, G: 0x2c, B: 0x2e, A: 0xff},
			Hover:    paint.Color{R: 0x3a, G: 0x3a, B: 0x3c, A: 0xff},
			Pressed:  paint.Color{R: 0x48, G: 0x48, B: 0x4a, A: 0xff},
			Line:     paint.Color{R: 0x3a, G: 0x3a, B: 0x3c, A: 0xff},
			Text:     paint.Color{R: 0xf2, G: 0xf2, B: 0xf7, A: 0xff},
			Muted:    paint.Color{R: 0x98, G: 0x98, B: 0x9d, A: 0xff},
			Accent:   paint.Color{R: 0x0a, G: 0x84, B: 0xff, A: 0xff},
			OnAccent: paint.Color{R: 0xff, G: 0xff, B: 0xff, A: 0xff},
		}
	}
	return reader.Palette{
		Theme:    reader.ThemeWhite,
		Paper:    paint.Color{R: 0xff, G: 0xff, B: 0xff, A: 0xff},
		Ink:      paint.Color{R: 0x1c, G: 0x1c, B: 0x1e, A: 0xff},
		Desk:     paint.Color{R: 0xff, G: 0xff, B: 0xff, A: 0xff},
		Side:     paint.Color{R: 0xeb, G: 0xea, B: 0xea, A: 0xff},
		Chrome:   paint.Color{R: 0xff, G: 0xff, B: 0xff, A: 0xff},
		Popover:  paint.Color{R: 0xff, G: 0xff, B: 0xff, A: 0xff},
		Hover:    paint.Color{R: 0xe4, G: 0xe3, B: 0xe3, A: 0xff},
		Pressed:  paint.Color{R: 0xd6, G: 0xd5, B: 0xd5, A: 0xff},
		Line:     paint.Color{R: 0xd9, G: 0xd8, B: 0xd8, A: 0xff},
		Text:     paint.Color{R: 0x1c, G: 0x1c, B: 0x1e, A: 0xff},
		Muted:    paint.Color{R: 0x8a, G: 0x8a, B: 0x8e, A: 0xff},
		Accent:   paint.Color{R: 0x0a, G: 0x84, B: 0xff, A: 0xff},
		OnAccent: paint.Color{R: 0xff, G: 0xff, B: 0xff, A: 0xff},
	}
}

// readerTheme is the app's own look over MyGo's base theme. The palette carries
// every colour the window needs, so the four papers differ in more than the page:
// the desk, the bars and the panels follow, as they do in Books.
func readerTheme(base ui.Theme, pal reader.Palette) ui.Theme {
	t := base
	t.Dark = pal.Theme.Dark()
	t.Background = reader.UIColor(pal.Desk)
	t.Surface = reader.UIColor(pal.Popover)
	t.SurfaceHover = reader.UIColor(pal.Hover)
	t.SurfacePressed = reader.UIColor(pal.Pressed)
	t.Border = reader.UIColor(pal.Line)
	t.Text = reader.UIColor(pal.Text)
	t.TextMuted = reader.UIColor(pal.Muted)
	t.Accent = reader.UIColor(pal.Accent)
	t.AccentHover = t.Accent.Mix(t.Text, 0.18)
	t.AccentPressed = t.Accent.Mix(t.Text, 0.32)
	t.AccentText = reader.UIColor(pal.OnAccent)
	t.Selection = t.Accent.Alpha(0.24)
	t.Focus = t.Accent
	t.Radius = 7
	t.Spacing = 4
	if f := reader.UIFontFamily(); f != "" {
		t.Font = f
	}
	return t
}

// paper is the colour of the page itself: the sheet the book is printed on,
// which the reading theme sets.
func paper() ui.Color {
	return reader.UIColor(reader.Current().Paper)
}
