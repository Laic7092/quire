package reader

import (
	"context"
	"fmt"

	"github.com/laix/layx/render"
	"github.com/laix/layx/render/paint"
)

// Font choices the display settings offer. A book is set in either the serif
// face, which is what a printed book uses, or the sans one.
const (
	FontSerif = "serif"
	FontSans  = "sans"
)

// Options are the reading choices the display settings change: the size, the
// leading, the face, the alignment, the margins and whether two pages share the
// window. Everything but the paper theme lives here, because the theme is a
// package-level palette the document is built against.
type Options struct {
	// FontSize is the reading size in CSS pixels.
	FontSize float32
	// LineHeight is the line box, as a multiple of the font size.
	LineHeight float32
	// Font is FontSerif or FontSans.
	Font string
	// Justify stretches every line but the last to the measure, as a book does.
	Justify bool
	// Margin is the page inset, all four sides, in CSS pixels.
	Margin float32
	// Spread shows two pages side by side when the window is wide enough.
	Spread bool
}

// DefaultOptions is what a reader starts with: 17px on 1.18 leading, in the
// serif face, justified, with Books' own page insets and two pages at a time.
func DefaultOptions() Options {
	return Options{
		FontSize:   DefaultFontSize,
		LineHeight: 1.18,
		Font:       FontSerif,
		Justify:    true,
		Margin:     Margin,
		// Books opens a book as an open book: two pages side by side, and one
		// as soon as the window is too narrow for the pair.
		Spread: true,
	}
}

// Limits of the settings the panel offers, in the order it offers them.
const (
	MinFontSize = 12
	MaxFontSize = 36
	MinLeading  = 1.05
	MaxLeading  = 2.4
	MinMargin   = 16
	MaxMargin   = 120

	// SpreadMinWidth is the narrowest page that still reads as a book at two
	// pages: below it the spread setting is ignored and one page fills the
	// sheet. Books opens the book as soon as there is room for the pair, which
	// on a Mac window is nearly always.
	SpreadMinWidth = 330
)

// Normalized returns o with every value inside its limits and a face the
// settings know.
func (o Options) Normalized() Options {
	if o.FontSize == 0 {
		o.FontSize = DefaultFontSize
	}
	o.FontSize = clamp(o.FontSize, MinFontSize, MaxFontSize)
	if o.LineHeight == 0 {
		o.LineHeight = 1.18
	}
	o.LineHeight = clamp(o.LineHeight, MinLeading, MaxLeading)
	if o.Font != FontSans {
		o.Font = FontSerif
	}
	if o.Margin == 0 {
		o.Margin = Margin
	}
	o.Margin = clamp(o.Margin, MinMargin, MaxMargin)
	return o
}

// Same reports whether two sets of options would lay out the same text the same
// way, and so need no new layout. The spread is not included: it changes the
// sheet's width, which the layout job watches on its own.
func (o Options) Same(other Options) bool {
	o, other = o.Normalized(), other.Normalized()
	return o.FontSize == other.FontSize && o.LineHeight == other.LineHeight &&
		o.Font == other.Font && o.Justify == other.Justify && o.Margin == other.Margin
}

// Margins is the standard page inset, all four sides.
func Margins() paint.Insets { return DefaultOptions().Margins() }

// Margins is the page inset for the options. The sides follow the reader's
// margin setting; the head and foot keep Books' own proportions, so the page's
// measure grows with the window rather than its type drifting down the page.
func (o Options) Margins() paint.Insets {
	m := o.Normalized().Margin
	return paint.Insets{Top: PageTop, Right: m, Bottom: PageBottom, Left: m}
}

// BuildLayout lays out a whole document at width×height DIPs with the reading
// options given. The caller runs it off the UI thread: it shapes every line of
// the book.
func BuildLayout(ctx context.Context, doc *render.Document, fonts *Fonts, width, height float32, opts Options) (*render.Layout, error) {
	if doc == nil {
		return nil, fmt.Errorf("reader: nil document")
	}
	if fonts == nil || fonts.Set == nil {
		return nil, fmt.Errorf("reader: nil fonts")
	}
	opts = opts.Normalized()
	if width < 3*opts.Margin || height < 3*opts.Margin {
		return nil, fmt.Errorf("reader: viewport %.0fx%.0f is too small", width, height)
	}
	eng, err := render.New(render.Options{})
	if err != nil {
		return nil, err
	}
	palette := Current()
	align := render.AlignLeft
	if opts.Justify {
		align = render.AlignJustify
	}
	return eng.Layout(ctx, doc, render.LayoutOptions{
		Viewport: render.Viewport{Width: width, Height: height},
		Typography: render.Typography{
			FontFamily: fonts.Family(opts.Font),
			FontSize:   opts.FontSize,
			LineHeight: opts.LineHeight,
			TextAlign:  align,
			Color:      palette.Ink,
			Background: palette.Paper,
		},
		Fonts:   fonts.Set,
		Margins: opts.Margins(),
	})
}

func clamp(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
