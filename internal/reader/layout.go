package reader

import (
	"context"
	"fmt"

	"github.com/laix/layx/render"
	"github.com/laix/layx/render/paint"
)

const (
	// Margin is the page margin on all four sides, in CSS pixels (DIPs).
	Margin = 56
	// DefaultFontSize is the reading size in CSS pixels.
	DefaultFontSize = 18
)

var (
	// Paper is the page background.
	Paper = paint.Color{R: 0xfb, G: 0xf8, B: 0xef, A: 0xff}
	// Ink is the body text color.
	Ink = paint.Color{R: 0x1a, G: 0x1a, B: 0x1a, A: 0xff}
)

// Margins returns the standard page margins.
func Margins() paint.Insets {
	return paint.Insets{Top: Margin, Right: Margin, Bottom: Margin, Left: Margin}
}

// BuildLayout lays out a whole document at width×height DIPs. The caller runs
// it off the UI thread: it shapes every line of the book.
func BuildLayout(ctx context.Context, doc *render.Document, fonts *Fonts, width, height, fontSize float32) (*render.Layout, error) {
	if doc == nil {
		return nil, fmt.Errorf("reader: nil document")
	}
	if fonts == nil || fonts.Set == nil {
		return nil, fmt.Errorf("reader: nil fonts")
	}
	if width < 3*Margin || height < 3*Margin {
		return nil, fmt.Errorf("reader: viewport %.0fx%.0f is too small", width, height)
	}
	if fontSize <= 0 {
		fontSize = DefaultFontSize
	}
	eng, err := render.New(render.Options{})
	if err != nil {
		return nil, err
	}
	return eng.Layout(ctx, doc, render.LayoutOptions{
		Viewport: render.Viewport{Width: width, Height: height},
		Typography: render.Typography{
			FontFamily: "book",
			FontSize:   fontSize,
			LineHeight: 1.6,
			Color:      Ink,
			Background: Paper,
		},
		Fonts:   fonts.Set,
		Margins: Margins(),
	})
}

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
