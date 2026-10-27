package reader

import (
	"context"
	"fmt"
	"math"

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
	// FontSize is the reading size in CSS pixels. It is also the size the
	// document has to be built at: the converter bakes absolute sizes into the
	// document's own styles, so a layout cannot restyle a document built at
	// another size (see BuildLayout).
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
//
// A size that is not a number — a slider or a state file that lost a digit, or
// a value that never arrived — takes the default it stands in for: every
// comparison against NaN is false, so a NaN would slip through every limit
// below and reach the layout, where it would place no line and ask for a new
// layout on every frame, since NaN is not even equal to itself.
func (o Options) Normalized() Options {
	o.FontSize = defaultNaN(o.FontSize, DefaultFontSize)
	if o.FontSize == 0 {
		o.FontSize = DefaultFontSize
	}
	o.FontSize = clamp(o.FontSize, MinFontSize, MaxFontSize)
	o.LineHeight = defaultNaN(o.LineHeight, 1.18)
	if o.LineHeight == 0 {
		o.LineHeight = 1.18
	}
	o.LineHeight = clamp(o.LineHeight, MinLeading, MaxLeading)
	if o.Font != FontSans {
		o.Font = FontSerif
	}
	o.Margin = defaultNaN(o.Margin, Margin)
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
//
// In scroll mode the engine takes the sides into the column and leaves the head
// and foot to the viewport: its content coordinates start at the top of the
// flow, so a host that scrolls insets its viewport by PageTop and PageBottom
// itself (the reader's column does, in scrollInset).
func (o Options) Margins() paint.Insets {
	m := o.Normalized().Margin
	return paint.Insets{Top: PageTop, Right: m, Bottom: PageBottom, Left: m}
}

// BuildLayout lays out a whole document at width×height DIPs with the reading
// options given. The caller runs it off the UI thread: it shapes every line of
// the book.
//
// doc must have been built at opts.FontSize (book.Book.DocumentSized) — the
// reading size. The converter resolves the book's CSS into absolute sizes, so
// this request cannot restyle a document made at another size: the pages are
// set at the size the document was built with. Changing the reading size means
// building the document again, which is what the reader does when the setting
// moves.
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
	// The whole document, whatever prev covered: a layout of a chapter range
	// (RelayoutRange) carries its range in the engine's options, and a host that
	// hands one back here asks for the book, not for that range again.
	if n := prev.Document().ChapterCount(); n > 0 {
		lo.Range = &render.ChapterRange{First: 0, Last: n}
	}
	return prev.Relayout(ctx, lo)
}

// RelayoutRange lays the chapters in [first, last) of prev's document out again
// for the current palette and reading options: the few chapters a host can draw
// at once, laid out while the whole book is still too much to wait for. It is
// Relayout with a chapter range — it shapes and paginates those chapters and
// nothing else, and page numbers start at 0 for the range.
//
// It shares prev's engine and its variant cache, so a host that chains one range
// off the last — the chapters on screen at every size the window is dragged
// through — pays for those chapters only, and the whole book's Relayout later
// reuses what they computed.
func RelayoutRange(ctx context.Context, prev *render.Layout, fonts *Fonts, width, height float32, opts Options, first, last int) (*render.Layout, error) {
	if prev == nil || prev.Document() == nil {
		return nil, fmt.Errorf("reader: nil layout")
	}
	if first < 0 || last <= first || last > prev.Document().ChapterCount() {
		return nil, fmt.Errorf("reader: chapter range [%d,%d) outside the document's %d chapters",
			first, last, prev.Document().ChapterCount())
	}
	lo, err := layoutOptions(fonts, width, height, opts)
	if err != nil {
		return nil, err
	}
	lo.Range = &render.ChapterRange{First: first, Last: last}
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
	if err := checkViewport(width, height, opts); err != nil {
		return render.LayoutOptions{}, err
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

// checkViewport reports whether a viewport can hold a page laid out with opts.
// A size that is not a number, or an infinite one, is refused rather than passed
// on: the engine would build pages of that size and hand the painter
// coordinates no pixel can be cut to, which is a blank window and a page map
// nothing can be found in. It says so instead.
func checkViewport(width, height float32, opts Options) error {
	if !finite(width) || !finite(height) {
		return fmt.Errorf("reader: viewport %gx%g is not a finite size", width, height)
	}
	if width < 3*opts.Margin || height < 3*opts.Margin {
		return fmt.Errorf("reader: viewport %.0fx%.0f is too small", width, height)
	}
	return nil
}

// finite reports whether v is a real number: neither NaN nor an infinity.
func finite(v float32) bool { return !math.IsNaN(float64(v)) && !math.IsInf(float64(v), 0) }

// defaultNaN is v, or def when v is not a number.
func defaultNaN(v, def float32) float32 {
	if math.IsNaN(float64(v)) {
		return def
	}
	return v
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
