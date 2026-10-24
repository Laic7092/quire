package main

import (
	"context"
	"image"
	"image/png"
	"os"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/reader"
	"github.com/laix/layx-reader/locales"
)

// The reading window at the size the reference was measured from: 787×590
// DIPs, the Quiet paper, two 372 DIP pages inside 21 DIP margins, and the
// display settings open over the right page. These are the ratios the layout
// was copied from, kept here so a change of a constant cannot drift away from
// the picture unnoticed.
func TestReferenceReadingLayout(t *testing.T) {
	a := newWindow(t)
	w := float32(787 - 2*pagePadding)
	h := float32(590 - headerHeight - footerHeight)
	layout, err := reader.BuildLayout(context.Background(), a.doc, a.fonts, w/2, h, a.opts)
	if err != nil {
		t.Fatal(err)
	}
	a.layout, a.layW, a.layH = layout, w/2, h
	a.page = 4
	a.aaOpen = true

	tt := ui.NewTester(a.view, 787, 590)
	tt.Frame()
	tt.Frame()
	tt.Frame()

	box, ok := tt.Find(locales.PageLabel())
	if !ok {
		t.Fatalf("the page is missing: %q", tt.Texts())
	}
	if box.X != 21 || box.W != 745 || box.Y != 46 || box.H != 500 {
		t.Errorf("sheet = (%g,%g) %g×%g, want (21,46) 745×500", box.X, box.Y, box.W, box.H)
	}
	m := a.opts.Margins()
	if m.Left != 27 || m.Right != 27 || m.Top != 8 || m.Bottom != 38 {
		t.Errorf("page insets = %+v, want 27/27 sides and 8/38 head/foot", m)
	}
	if a.boxW != 745 {
		t.Errorf("body width = %g, want 745", a.boxW)
	}

	for _, want := range []string{
		locales.ThemesSettings(), locales.ThemeOriginal(), locales.ThemeQuiet(),
		locales.ThemePaper(), locales.ThemeBold(), locales.ThemeCalm(), locales.ThemeFocus(),
		locales.SettingsCustomize(), a.pageLabel(),
	} {
		if !tt.HasText(want) {
			t.Errorf("the reference state has no %q: %q", want, tt.Texts())
		}
	}

	// The paper, the toolbar's capsule and the popover are the colours the
	// reference pixels carry, not whatever the theme happened to be.
	img := tt.Image()
	wantColor := func(x, y int, r, g, b uint8, what string) {
		cr, cg, cb := pixel(img, x, y)
		if cr != r || cg != g || cb != b {
			t.Errorf("%s at (%d,%d) = #%02x%02x%02x, want #%02x%02x%02x", what, x, y, cr, cg, cb, r, g, b)
		}
	}
	wantColor(4, 300, 0x4a, 0x4a, 0x4d, "the page")
	wantColor(30, 10, 0x31, 0x31, 0x33, "the toolbar capsule")
	wantColor(600, 75, 0x30, 0x30, 0x33, "the popover")
	wantColor(600, 155, 0xff, 0xff, 0xff, "the Original card")
	wantColor(700, 215, 0xf0, 0xe2, 0xc9, "the Calm card")

	if os.Getenv("SNAP") != "" {
		f, err := os.Create("/tmp/snap-reference.png")
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
	}
}

func pixel(img image.Image, x, y int) (uint8, uint8, uint8) {
	r, g, b, _ := img.At(x, y).RGBA()
	return uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)
}
