package reader

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx/render/paint"
)

// luma is how light a colour reads, on the weights contrast follows: the
// relative luminance of WCAG, from the sRGB channels.
func luma(c paint.Color) float64 {
	lin := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

// contrast is the ratio of two luminances, 1 for two colours of one lightness
// and 21 for black on white.
func contrast(a, b paint.Color) float64 {
	la, lb := luma(a), luma(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// over is a translucent colour painted on an opaque one: what a veil of
// selection or a search hit leaves on the page.
func over(fg, bg paint.Color) paint.Color {
	a := float64(fg.A) / 255
	mix := func(f, b uint8) uint8 {
		return uint8(float64(f)*a + float64(b)*(1-a) + 0.5)
	}
	return paint.Color{R: mix(fg.R, bg.R), G: mix(fg.G, bg.G), B: mix(fg.B, bg.B), A: 0xff}
}

// Every paper is a reading surface: the body type has to be plainly readable on
// it — far past the 4.5:1 a small text is asked for — a link has to be told from
// the body and still read, the secondary ink has to be dimmer than the body and
// not a fog, and a selected passage has to keep its text readable and its veil
// visible. These are floors, not targets: a palette that moves under them is a
// page that got harder to read.
func TestPaletteContrast(t *testing.T) {
	for _, theme := range ThemeOrder {
		p := ForTheme(theme)
		if p.Theme != theme {
			t.Errorf("%q palette says %q", theme, p.Theme)
		}
		body := contrast(p.Ink, p.Paper)
		if body < 7 {
			t.Errorf("%q: body type reads at %.2f:1, want at least 7", theme, body)
		}
		link := contrast(p.Link, p.Paper)
		if link < 4.5 {
			t.Errorf("%q: a link reads at %.2f:1, want at least 4.5", theme, link)
		}
		muted := contrast(p.Muted, p.Paper)
		if muted < 2.9 {
			t.Errorf("%q: secondary text reads at %.2f:1, want at least 2.9", theme, muted)
		}
		if muted > body {
			t.Errorf("%q: secondary text reads heavier (%.2f) than the body (%.2f)", theme, muted, body)
		}
		for name, veil := range map[string]paint.Color{
			"selection": p.Select, "match": p.Match, "note": p.Note,
		} {
			if veil.A == 0 || veil.A == 0xff {
				t.Errorf("%q: the %s veil has alpha %d, want a translucent one", theme, name, veil.A)
				continue
			}
			on := over(veil, p.Paper)
			if got := contrast(on, p.Paper); got < 1.1 {
				t.Errorf("%q: the %s veil reads at %.2f:1 over the paper, want a visible one", theme, name, got)
			}
			if got := contrast(p.Ink, on); got < 3.5 {
				t.Errorf("%q: text under the %s veil reads at %.2f:1, want at least 3.5", theme, name, got)
			}
		}
	}
}

// A palette is a page and the chrome around it: nothing in it may be a hole, the
// paper must be its own desk, and the two dark papers are the dark ones.
func TestPaletteIsWhole(t *testing.T) {
	for _, theme := range ThemeOrder {
		p := ForTheme(theme)
		// The veils over the page are translucent on purpose; every other
		// colour is a surface or an ink and has to be whole.
		for name, c := range map[string]paint.Color{
			"paper": p.Paper, "ink": p.Ink, "link": p.Link, "desk": p.Desk,
			"side": p.Side, "chrome": p.Chrome, "popover": p.Popover, "hover": p.Hover,
			"pressed": p.Pressed, "line": p.Line, "text": p.Text, "muted": p.Muted,
			"accent": p.Accent, "onAccent": p.OnAccent, "raised": p.Raised,
			"toolbar": p.Toolbar, "onToolbar": p.OnToolbar,
		} {
			if c.A != 0xff {
				t.Errorf("%q: %s is %+v, want an opaque colour", theme, name, c)
			}
		}
		if p.Desk != p.Paper {
			t.Errorf("%q: the desk %+v is not the paper %+v; the sheet would show its edge", theme, p.Desk, p.Paper)
		}
		if got := theme.Dark(); got != (luma(p.Paper) < 0.5) {
			t.Errorf("%q: Dark() = %v on a paper of luma %.2f", theme, got, luma(p.Paper))
		}
	}
}

// A theme name round-trips through its palette, the four names the papers had
// before still name them, and a name the reader does not know is not silently
// taken for one it does.
func TestParseTheme(t *testing.T) {
	for _, theme := range ThemeOrder {
		if got := ParseTheme(string(theme)); got != theme {
			t.Errorf("ParseTheme(%q) = %q", theme, got)
		}
	}
	for name, want := range map[string]Theme{
		"white": ThemeOriginal, "gray": ThemePaper, "sepia": ThemeCalm, "night": ThemeQuiet,
	} {
		if got := ParseTheme(name); got != want {
			t.Errorf("ParseTheme(%q) = %q, want %q", name, got, want)
		}
	}
	for _, name := range []string{"", "graphite", "Original", " sepia"} {
		if got := ParseTheme(name); got != "" {
			t.Errorf("ParseTheme(%q) = %q, want no paper", name, got)
		}
		if got := ForTheme(ParseTheme(name)); got.Theme != ThemeQuiet {
			t.Errorf("an unknown paper %q reads as %q, want the fallback", name, got.Theme)
		}
	}
	if System(true) != ThemeQuiet || System(false) != ThemeOriginal {
		t.Errorf("the system's papers: dark %q, light %q", System(true), System(false))
	}
}

// SetPalette publishes the paper the next layout and the next cover are made of,
// and Current hands back a copy: one host changing a field of it does not
// repaint another's pages.
func TestPaletteRoundTrip(t *testing.T) {
	saved := Current()
	t.Cleanup(func() { SetPalette(saved) })
	SetPalette(Calm)
	got := Current()
	if got.Theme != ThemeCalm || got.Paper != Calm.Paper || got.Ink != Calm.Ink {
		t.Fatalf("Current = %+v, want the Calm paper", got)
	}
	got.Paper = paint.Color{R: 1, G: 2, B: 3, A: 0xff}
	if again := Current(); again.Paper != Calm.Paper {
		t.Errorf("a copy of the palette wrote through to the palette: %+v", again.Paper)
	}
	// Both names for a paper are the same palette.
	if White != Original || Gray != Paper || Sepia != Calm || Night != Quiet {
		t.Error("the papers' old names do not name them")
	}
	// The chrome and the page come out of one palette: opaque colours are the
	// toolkit's own, and a veil keeps its alpha.
	if got, want := UIColor(paint.Color{R: 0x12, G: 0x34, B: 0x56, A: 0xff}), ui.RGB(0x12, 0x34, 0x56); got != want {
		t.Errorf("UIColor(0xff alpha) = %+v, want %+v", got, want)
	}
	if got, want := UIColor(Original.Select), ui.RGBA(Original.Select.R, Original.Select.G, Original.Select.B,
		float32(Original.Select.A)/255); got != want {
		t.Errorf("UIColor(veil) = %+v, want %+v", got, want)
	}
}
