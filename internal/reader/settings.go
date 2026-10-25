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

// Turn is how a page gives way to the next one. The display settings offer
// both, and a scrolling book has neither: its column never turns.
type Turn string

const (
	// TurnSlide moves the spread leaving off the sheet while the next follows
	// it, as a page under a finger.
	TurnSlide Turn = "slide"
	// TurnFade dissolves the spread leaving into the one arriving in place.
	TurnFade Turn = "fade"
)

// Options are the reading choices the display settings change: the size, the
// leading, the face, the alignment, the margins, whether two pages share the
// window and how a page turns. Everything but the paper theme lives here,
// because the theme is a package-level palette the document is built against.
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
	// Scroll reads the book as one continuous column instead of pages: the
	// layout is built in render.ModeScroll and the reader scrolls it. The
	// spread does not apply; one column fills the sheet.
	Scroll bool
	// Turn is how a page gives way to the next. It changes nothing the layout
	// does, so Same ignores it: a new turn takes effect on the next turn, not
	// in a rebuild of the book.
	Turn Turn
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
		Turn:   TurnSlide,
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
	if o.Turn != TurnFade {
		o.Turn = TurnSlide
	}
	return o
}

// Same reports whether two sets of options would lay out the same text the same
// way, and so need no new layout. The spread is not included: it changes the
// sheet's width, which the layout job watches on its own. Neither is the turn:
// the pages already on screen hold every spread the transition draws. The
// scroll mode is included: it changes the layout's output mode and so its very
// geometry.
func (o Options) Same(other Options) bool {
	o, other = o.Normalized(), other.Normalized()
	return o.FontSize == other.FontSize && o.LineHeight == other.LineHeight &&
		o.Font == other.Font && o.Justify == other.Justify && o.Margin == other.Margin &&
		o.Scroll == other.Scroll
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
	lo, err := layoutOptions(fonts, width, height, opts)
	if err != nil {
		return nil, err
	}
	eng, err := render.New(render.Options{})
	if err != nil {
		return nil, err
	}
	return eng.Layout(ctx, doc, lo)
}

// Relayout lays prev's document out again for the current palette and reading
// options. The paper, the ink and the link colour are paint-only, so a theme
// change reuses the line boxes and the page maps and only the pages on screen
// are drawn again: the reader changes paper without re-shaping the book, however
// long it is.
//
// A settings change reuses what it can too — a new line height keeps the line
// boxes and only re-paginates — so this is the path every rebuild takes while
// the document itself stays the same.
func Relayout(ctx context.Context, prev *render.Layout, fonts *Fonts, width, height float32, opts Options) (*render.Layout, error) {
	if prev == nil || prev.Document() == nil {
		return nil, fmt.Errorf("reader: nil layout")
	}
	lo, err := layoutOptions(fonts, width, height, opts)
	if err != nil {
		return nil, err
	}
	return prev.Relayout(ctx, lo)
}

// Recolor prints prev's pages on the paper in use. The paper, the ink and the
// link colour are paint-only, so nothing is shaped and no page moves: the layout
// keeps its lines, its pages and its place, and only the pages drawn afterwards
// are painted again. It is the theme switch — a copy of the layout, not a pass
// over the book.
func Recolor(prev *render.Layout) *render.Layout {
	if prev == nil {
		return nil
	}
	palette := Current()
	return prev.WithColors(palette.Ink, palette.Paper, palette.Link)
}

// layoutOptions is the render request for the reading options and the palette in
// use, so a build and a relayout ask for exactly the same thing.
func layoutOptions(fonts *Fonts, width, height float32, opts Options) (render.LayoutOptions, error) {
	if fonts == nil || fonts.Set == nil {
		return render.LayoutOptions{}, fmt.Errorf("reader: nil fonts")
	}
	opts = opts.Normalized()
	if width < 3*opts.Margin || height < 3*opts.Margin {
		return render.LayoutOptions{}, fmt.Errorf("reader: viewport %.0fx%.0f is too small", width, height)
	}
	palette := Current()
	align := render.AlignLeft
	if opts.Justify {
		align = render.AlignJustify
	}
	// Scroll mode stacks the chapters into one flow and slices it into tiles
	// one viewport tall, which is what the reader draws while it scrolls.
	mode := render.ModePaged
	var tileHeight float32
	if opts.Scroll {
		mode, tileHeight = render.ModeScroll, height
	}
	return render.LayoutOptions{
		Viewport: render.Viewport{Width: width, Height: height},
		Typography: render.Typography{
			FontFamily: fonts.Family(opts.Font),
			FontSize:   opts.FontSize,
			LineHeight: opts.LineHeight,
			TextAlign:  align,
			Color:      palette.Ink,
			Background: palette.Paper,
			LinkColor:  palette.Link,
		},
		Fonts:      fonts.Set,
		Margins:    opts.Margins(),
		Mode:       mode,
		TileHeight: tileHeight,
	}, nil
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
