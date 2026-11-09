package reader

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx/render"
	"github.com/laix/layx/render/paint"
)

func checkLayoutColors(t *testing.T, layout *render.Layout, palette Palette, what string) {
	t.Helper()
	got := layout.Typography()
	if got.Background != palette.Paper || got.Color != palette.Ink || got.LinkColor != palette.Link {
		t.Errorf("%s has background %+v, ink %+v, link %+v; want %+v, %+v, %+v",
			what, got.Background, got.Color, got.LinkColor, palette.Paper, palette.Ink, palette.Link)
	}
}

// The palette-explicit build and relayout paths take the palette they are
// given, not whatever process-wide theme happens to be current. Two layouts
// built from different palettes stay independent.
func TestLayoutWithPaletteUsesExplicitColors(t *testing.T) {
	doc, fonts := selectionBook(t)
	saved := Current()
	defer SetPalette(saved)

	SetPalette(Quiet) // deliberately different from both explicit palettes
	opts := Options{FontSize: 18}
	original, err := BuildLayoutWithPalette(context.Background(), doc, fonts, Original, 420, 560, opts)
	if err != nil {
		t.Fatal(err)
	}
	bold, err := BuildLayoutWithPalette(context.Background(), doc, fonts, Bold, 420, 560, opts)
	if err != nil {
		t.Fatal(err)
	}
	checkLayoutColors(t, original, Original, "the layout built with Original")
	checkLayoutColors(t, bold, Bold, "the layout built with Bold")

	// A relayout takes its palette from the argument too, and leaves the
	// layout it was based on untouched.
	calm, err := RelayoutWithPalette(context.Background(), original, fonts, Calm, 420, 560, opts)
	if err != nil {
		t.Fatal(err)
	}
	checkLayoutColors(t, calm, Calm, "the relayout with Calm")
	checkLayoutColors(t, original, Original, "the layout a Calm relayout was based on")
	if calm.PageCount() != original.PageCount() {
		t.Errorf("the Calm relayout changed %d pages to %d", original.PageCount(), calm.PageCount())
	}
}

// RecolorWithPalette is the paint-only theme switch with an explicit palette.
// The process-wide palette is irrelevant, and the source layout keeps its own
// colours.
func TestRecolorWithPaletteUsesExplicitColors(t *testing.T) {
	doc, fonts := selectionBook(t)
	saved := Current()
	defer SetPalette(saved)

	layout, err := BuildLayoutWithPalette(context.Background(), doc, fonts, Original, 420, 560, Options{FontSize: 18})
	if err != nil {
		t.Fatal(err)
	}
	SetPalette(Bold)
	recoloured := RecolorWithPalette(layout, Calm)
	if recoloured == layout {
		t.Fatal("RecolorWithPalette returned the same layout")
	}
	checkLayoutColors(t, recoloured, Calm, "the recoloured layout")
	checkLayoutColors(t, layout, Original, "the layout before recolouring")
	if recoloured.PageCount() != layout.PageCount() {
		t.Errorf("recolouring changed %d pages to %d", layout.PageCount(), recoloured.PageCount())
	}
	if RecolorWithPalette(nil, Calm) != nil {
		t.Error("RecolorWithPalette(nil) is not nil")
	}
}

// A range relayout also captures its palette from the argument.
func TestRelayoutRangeWithPaletteUsesExplicitColors(t *testing.T) {
	doc, fonts := chaptersBook(t, 3)
	saved := Current()
	defer SetPalette(saved)

	SetPalette(Original)
	full, err := BuildLayoutWithPalette(context.Background(), doc, fonts, Bold, 420, 560, Options{FontSize: 18})
	if err != nil {
		t.Fatal(err)
	}
	part, err := RelayoutRangeWithPalette(context.Background(), full, fonts, Focus, 420, 560, Options{FontSize: 18}, 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	checkLayoutColors(t, part, Focus, "the range relayout with Focus")
	checkLayoutColors(t, full, Bold, "the layout a Focus range relayout was based on")
	if loc, ok := part.LocationOfPage(0); !ok || loc.Chapter != 1 {
		t.Fatalf("page 0 of the range starts at %+v, %v; want chapter 1", loc, ok)
	}
}

func solidPNG(t *testing.T, c color.RGBA) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// UIPainter.DrawCover fills the cover's box from the palette stored in the
// painter. Two painters created with different palettes draw different paper
// even while the process-wide palette names yet another theme.
func TestUIPainterWithPaletteDrawsCoverOnItsPaper(t *testing.T) {
	const imageID = render.ImageID(41)
	data := solidPNG(t, color.RGBA{R: 0xff, A: 0xff})
	saved := Current()
	defer SetPalette(saved)
	SetPalette(Calm) // neither painter's palette

	draw := func(palette Palette) *image.RGBA {
		up := NewUIPainterWithPalette(nil, nil, palette)
		if !up.SetImage(imageID, data) {
			t.Fatal("the cover PNG did not decode")
		}
		return ui.Render(func(c *ui.Context) {
			ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
				if !up.DrawCover(p, imageID, r, 8) {
					t.Fatal("DrawCover drew no cover")
				}
			})
		}, 40, 40, 1)
	}

	for _, palette := range []Palette{Original, Bold} {
		img := draw(palette)
		c := img.RGBAAt(1, 1)
		if c.R != palette.Paper.R || c.G != palette.Paper.G || c.B != palette.Paper.B {
			t.Errorf("a cover painter for %s filled its corner with %v, want %+v",
				palette.Theme, c, palette.Paper)
		}
	}
}

// The headless renderer clears its surface to the paper stored in it. The
// process-wide palette must not leak into either of two renderers created with
// different palettes.
func TestPageRendererWithPaletteClearsToItsPaper(t *testing.T) {
	saved := Current()
	defer SetPalette(saved)

	SetPalette(Calm) // neither renderer's palette
	for _, palette := range []Palette{Original, Bold} {
		pr := NewPageRendererWithPalette(palette)
		img := pr.render(2, 2, 1, func(paint.Painter) {})
		c := img.RGBAAt(0, 0)
		if c.R != palette.Paper.R || c.G != palette.Paper.G || c.B != palette.Paper.B {
			t.Errorf("a renderer for %s cleared to %v, want %+v",
				palette.Theme, c, palette.Paper)
		}
	}
}
