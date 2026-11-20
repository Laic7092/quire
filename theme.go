package main

import (
	"runtime"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/laic7092/layx/render/paint"
	"github.com/laix/quire/internal/reader"
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
//
// The window's own controls — the system's caption buttons over the bar, GTK's
// title buttons — follow the same appearance, which MyGo is told once per
// change: see syncWindowAppearance.
func (a *app) applyTheme(c *ui.Context) *ui.Theme {
	base := *c.Theme()
	// A window is either the library or a book. The library is chrome and keeps
	// the system's own appearance; a book window is the page, even before its
	// book has arrived, so it opens on the paper rather than on the library's
	// desk.
	reading := a.book != nil || a.bookWindow
	if !reading {
		base.Dark = a.libraryChromeDark(base.Dark)
	}
	follow := !reading || a.themeSys || a.theme == ""
	paper := a.paperTheme(base.Dark)
	// The window's own controls are the system's, and they sit on this bar: let
	// them take its appearance rather than the desktop's.
	if a.syncWindowAppearance(windowSource(follow, paper)) && follow {
		// The bar was drawn in the appearance the last book forced, which this
		// frame's base theme still carries; the window follows the desktop's
		// own from here, so the library is drawn in it on this very frame.
		base.Dark = mygo.Theme.IsDark()
		if !reading {
			base.Dark = a.libraryChromeDark(base.Dark)
		}
		paper = a.paperTheme(base.Dark)
	}
	pal := reader.ForTheme(paper)
	if !reading {
		pal = systemChrome(base.Dark)
	}
	a.chrome = pal
	a.pagePal = pal
	if a.paint != nil {
		a.paint.SetPalette(pal)
	}
	key := skinKey{paper: paper, dark: base.Dark, accent: base.Accent, font: base.FontSize, library: !reading}
	if !a.skinSet || key != a.skinKey {
		a.skin = readerTheme(base, a.chrome)
		a.skinSet = true
		a.skinKey = key
	}
	c.SetTheme(&a.skin)
	// A phone paints the window behind the safe area in the same colour, and
	// colours the system's bar icons for what lies under them. Both cost a call
	// to the system only when the look changes, not every frame.
	a.syncPhoneChrome(&a.skin)
	// The page's ink and paper are baked into the page display lists, so a
	// change of paper costs a paint-only relayout: ensureLayout sees the new
	// palette on this frame and the pages on screen are drawn again.
	return &a.skin
}

// windowSource is the appearance the window's own controls are drawn in. They
// are the system's — minimize, maximize and close over the reading bar on
// Windows, GTK's title buttons on Linux — and the system paints them in the
// appearance it was given, not in the palette the bar is filled with. So the
// window takes the bar's own: the desktop's while it follows it, and the
// paper's while a book is open, which keeps white glyphs off a white page.
func windowSource(follow bool, paper reader.Theme) mygo.ThemeSource {
	if follow {
		return mygo.ThemeSystem
	}
	if paper.Dark() {
		return mygo.ThemeDark
	}
	return mygo.ThemeLight
}

// libraryChromeDark asks the desk for the desktop appearance the library
// should keep. A reader built by hand in a test has no desk and simply follows
// the frame's default.
func (a *app) libraryChromeDark(baseDark bool) bool {
	if a.desk == nil {
		return baseDark
	}
	return a.desk.libraryChromeDark(baseDark, mygo.Theme.Source() == mygo.ThemeSystem)
}

// syncWindowAppearance hands MyGo the appearance the window's controls are
// drawn in, once per change: a new source repaints the controls, the menus and
// the window's frame, so it costs a frame and is not a per-frame call. It
// reports that it changed the appearance, which the caller's frame still draws
// with the old one.
//
// macOS is left alone. Its traffic lights are coloured dots that read on every
// paper, and its menus are the desktop's own, not the reader's, so forcing the
// page's appearance on the app would darken them without a reason.
//
// The appearance is the app's, like the palette: with several windows, a
// library on the system's look and a book on a dark paper would otherwise fight
// over it every frame. The focused window decides, so a book's controls match
// the bar they sit on. The library remembers the desktop's own appearance while
// a book forces another one, so it does not turn over behind the book.
func (a *app) syncWindowAppearance(src mygo.ThemeSource) bool {
	if runtime.GOOS == "darwin" {
		return false
	}
	if a.desk != nil {
		if a.win == nil || !a.win.IsFocused() {
			return false
		}
		if !mygo.App.IsReady() {
			return false
		}
		if mygo.Theme.Source() == src {
			a.winSrc = src
			return false
		}
		mygo.Theme.SetSource(src)
		a.winSrc = src
		return true
	}
	if src == a.winSrc {
		return false
	}
	if !mygo.App.IsReady() {
		// A test's frames run without an application, and no window controls
		// are there to be coloured: MyGo is not running to be told.
		return false
	}
	a.winSrc = src
	mygo.Theme.SetSource(src)
	return true
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

// palette is the window's captured reading palette. A reader built by hand in
// a test may not have gone through applyTheme yet; it falls back to the legacy
// process-wide palette so such unit fixtures remain useful.
func (a *app) palette() reader.Palette {
	if a.pagePal.Theme != "" {
		return a.pagePal
	}
	return reader.Current()
}

// paperColor is the colour of the page itself: the sheet the book is printed
// on, from this window's captured palette.
func (a *app) paperColor() ui.Color {
	return reader.UIColor(a.palette().Paper)
}
