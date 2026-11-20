package render

import (
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/laix/layx/render/paint"
)

// Options configures an Engine.
type Options struct {
	// Workers is the maximum number of goroutines the Engine runs in parallel,
	// including the calling goroutine. 0 means GOMAXPROCS. It is a budget, not
	// a per-call limit: concurrent layouts, page builds and prefetches split it
	// between them, so the whole library never oversubscribes the machine.
	Workers int
	// PageCache is the number of rendered pages cached per Layout. 0 uses 4.
	PageCache int
	// TileCache is the number of scroll-mode tiles cached per Layout. 0 uses 8.
	TileCache int
	// LineCache is the maximum number of shaped lines retained per Layout.
	// 0 uses 256. The cache is bounded, so it never grows with book size.
	LineCache int
	Limits    Limits
	// Measure enables phase timing, reported by Engine.Stats. It is off by
	// default so the layout path performs no clock reads; enabling it does not
	// change layout results.
	Measure bool
	// OnIssue, if non-nil, receives one call per diagnostic issue raised by a
	// Layout or Relayout call. Issues are delivered after the call completes,
	// on the goroutine that called it, so the callback never runs concurrently
	// with itself and may call back into the library. The same list is
	// available afterwards from Layout.Issues.
	//
	// Diagnostics cover what the library decides to drop or substitute rather
	// than fail on: an unknown font family, runes no face covers, content the
	// IR cannot express. They never change layout results.
	OnIssue func(Issue)
}

// Limits bounds the resources consumed by a single Layout call. A zero field
// uses the corresponding default.
//
// Every limit but MaxImagePixels is a hard cap: a layout that would exceed one
// fails with ErrLimitExceeded instead of allocating its way out of memory.
// MaxImagePixels drops and reports the image it bounds, because one image must
// not cost the reader the book.
type Limits struct {
	MaxNodes     int
	MaxDepth     int
	MaxTextRunes int
	// MaxImagePixels bounds one image's declared box, not the document's images
	// added up: it is what keeps a single absurd tag from asking the host for a
	// gigabyte-sized bitmap. An image over it is dropped and reported as
	// IssueDroppedContent, and the rest of the book lays out as usual.
	MaxImagePixels int64
	// MaxPages bounds the number of pages a paged layout may produce. It is
	// what stops a configuration (one-nanometre margins, say) from turning a
	// modest document into millions of page boxes.
	MaxPages int
	// MaxTiles bounds the number of tiles a scroll layout may produce. The
	// tile count is content height divided by TileHeight, so it is the limit
	// that bites when TileHeight is tiny.
	MaxTiles int
	// MaxLines bounds the number of line boxes a layout may build. It catches
	// documents that stay under MaxTextRunes but break into an absurd number
	// of very short lines.
	MaxLines int
}

// MinTileHeight is the smallest accepted LayoutOptions.TileHeight in CSS
// pixels. Below it one extra pixel of content height adds another tile, so the
// tile count explodes without the input being unusual; a layout with a smaller
// non-zero TileHeight fails with ErrInvalidOptions.
const MinTileHeight = 16

// DefaultLimits returns the limits used for zero-valued Limits fields.
func DefaultLimits() Limits {
	return Limits{
		MaxNodes:       1 << 20,
		MaxDepth:       256,
		MaxTextRunes:   20_000_000,
		MaxImagePixels: 64 << 20,
		MaxPages:       100_000,
		MaxTiles:       100_000,
		MaxLines:       2_000_000,
	}
}

func (l Limits) withDefaults() Limits {
	d := DefaultLimits()
	if l.MaxNodes <= 0 {
		l.MaxNodes = d.MaxNodes
	}
	if l.MaxDepth <= 0 {
		l.MaxDepth = d.MaxDepth
	}
	if l.MaxTextRunes <= 0 {
		l.MaxTextRunes = d.MaxTextRunes
	}
	if l.MaxImagePixels <= 0 {
		l.MaxImagePixels = d.MaxImagePixels
	}
	if l.MaxPages <= 0 {
		l.MaxPages = d.MaxPages
	}
	if l.MaxTiles <= 0 {
		l.MaxTiles = d.MaxTiles
	}
	if l.MaxLines <= 0 {
		l.MaxLines = d.MaxLines
	}
	return l
}

// DefaultLinkColor is the colour of link text whose document style names none
// and whose host sets no Typography.LinkColor: the browser's own link blue.
var DefaultLinkColor = paint.Color{R: 0x1a, G: 0x5f, B: 0xc4, A: 0xff}

// Viewport is the page or window size in CSS pixels.
type Viewport struct {
	Width, Height float32
}

// Typography carries document-wide defaults. Per-node styles override them.
type Typography struct {
	FontFamily    string
	FontSize      float32
	LineHeight    float32
	LetterSpacing float32
	// WordSpacing is added to word-separating spaces (U+0020 / U+00A0). Zero
	// is the CSS initial value.
	WordSpacing float32
	// ParagraphGap is inserted between top-level block units when their own
	// margins are zero.
	ParagraphGap float32
	TextAlign    TextAlign
	Color        paint.Color
	// Background is painted behind each page. A transparent value means the
	// host is responsible for the page background.
	Background paint.Color
	// LinkColor paints link text whose style names no colour of its own, as an
	// epub built with Options.ThemeLinks leaves it. It is paint-only: like
	// Color, changing it needs a Relayout, not a new document. A transparent
	// value means DefaultLinkColor. A link whose document style names a colour
	// keeps that colour.
	LinkColor paint.Color
	// Rules carries the document-wide line-breaking and pagination policies.
	// Its zero value is the spec default: no optional typography, and no
	// widow/orphan protection. See TextRules.
	Rules TextRules
}

func (t Typography) withDefaults() Typography {
	if t.FontFamily == "" {
		t.FontFamily = "default"
	}
	if t.FontSize <= 0 {
		t.FontSize = 18
	}
	if t.LineHeight <= 0 {
		t.LineHeight = 1.5
	}
	if t.TextAlign == AlignDefault || t.TextAlign > AlignJustify {
		t.TextAlign = AlignLeft
	}
	if t.Color.Transparent() {
		t.Color = paint.Color{R: 0, G: 0, B: 0, A: 0xff}
	}
	if t.LinkColor.Transparent() {
		t.LinkColor = DefaultLinkColor
	}
	// Rules pads widows and orphans to their CSS initial value; everything
	// else in it is already zero when unset (no optional typography).
	t.Rules = t.Rules.withDefaults()
	return t
}

// Mode selects how the continuous flow is exposed.
type Mode uint8

const (
	// ModePaged slices the flow into pages. It is the default and the only
	// mode that supports PageCount/Page.
	ModePaged Mode = iota
	// ModeScroll exposes the whole book as one continuous content stream,
	// sliced into fixed-height tiles by Tile.
	ModeScroll
)

// LayoutOptions describes one layout variant.
type LayoutOptions struct {
	Viewport   Viewport
	Typography Typography
	Fonts      *FontSet
	// Margins are page margins in CSS pixels. In scroll mode the left and
	// right values still inset content; the top and bottom values are page
	// viewport insets and do not appear in content coordinates.
	Margins paint.Insets
	// Mode selects paged (default) or scrolling output. It does not affect
	// line breaking or vertical stacking, so toggling it reuses cached flow.
	Mode Mode
	// TileHeight is the height of a scroll tile in content pixels. Zero uses
	// Viewport.Height. It is ignored in paged mode. A non-zero value below
	// MinTileHeight is rejected with ErrInvalidOptions: one extra pixel of
	// content height would add another tile, so such a configuration can turn a
	// short book into hundreds of thousands of tile boxes.
	TileHeight float32
	// Hyphenator supplies intra-word break points for blocks whose style sets
	// Hyphenate. Nil disables hyphenation, and the zero value keeps the old
	// line breaking. Its Name participates in BreakKey.
	Hyphenator Hyphenator
	// Range restricts the layout to chapters [First, Last) of the document;
	// nil means the whole document. Page numbers start at 0 for the range.
	Range *ChapterRange
}

func (o LayoutOptions) validate() (LayoutOptions, error) {
	// Non-finite floats before anything else. The range checks below are all
	// "x <= 0"-shaped and NaN compares false to every one of them, so without
	// this pass a NaN would sail through and poison page geometry. The check
	// also has to precede withDefaults, which treats non-positive values
	// (including NaN) as unset.
	if err := validateLayoutOptionsFinite(o); err != nil {
		return o, err
	}
	if o.Viewport.Width <= 0 || o.Viewport.Height <= 0 {
		return o, docErrorf(ErrInvalidOptions, "viewport must be positive")
	}
	if o.Mode > ModeScroll {
		return o, docErrorf(ErrInvalidOptions, "unknown mode %d", o.Mode)
	}
	if o.TileHeight < 0 {
		return o, docErrorf(ErrInvalidOptions, "tile height must not be negative")
	}
	if o.TileHeight > 0 && o.TileHeight < MinTileHeight {
		return o, docErrorf(ErrInvalidOptions, "tile height %v is below the minimum %d", o.TileHeight, MinTileHeight)
	}
	contentW := o.Viewport.Width - o.Margins.Left - o.Margins.Right
	contentH := o.Viewport.Height - o.Margins.Top - o.Margins.Bottom
	if contentW <= 0 || contentH <= 0 {
		return o, docErrorf(ErrInvalidOptions, "margins leave no content box")
	}
	if o.Margins.Left < 0 || o.Margins.Right < 0 || o.Margins.Top < 0 || o.Margins.Bottom < 0 {
		return o, docErrorf(ErrInvalidOptions, "margins must not be negative")
	}
	if o.Fonts == nil {
		return o, docErrorf(ErrNoFonts, "Fonts is required")
	}
	o.Typography = o.Typography.withDefaults()
	return o, nil
}

func contentWidth(o LayoutOptions) float32 {
	return o.Viewport.Width - o.Margins.Left - o.Margins.Right
}

func contentHeight(o LayoutOptions) float32 {
	return o.Viewport.Height - o.Margins.Top - o.Margins.Bottom
}

// Engine owns layout workers and configuration. It holds no document state.
//
// An Engine may be used by any number of goroutines at once. Its parallel
// phases draw from one process-wide budget of Workers goroutines, so concurrent
// layouts, page builds and prefetches share the machine evenly instead of each
// starting a full set of workers.
type Engine struct {
	workers   int
	pageCache int
	tileCache int
	lineCache int
	limits    Limits

	// budget caps the total number of helper goroutines this Engine runs.
	budget *budget
	// active is the number of parallel phases in flight; it divides the
	// budget between them.
	active atomic.Int64
	// slot hands out per-worker ids for the load-balance diagnostics.
	slot atomic.Int64

	// pools holds one textWorker pool per font set. Font faces, shapers and
	// scratch buffers are per worker, so pooling them by font set is what keeps
	// a relayout from paying for them again.
	poolsMu sync.Mutex
	pools   map[*FontSet]*sync.Pool

	// shapes counts shaping calls. Layout variants that reuse cached line boxes
	// must not increment it; tests use it to prove the cache works.
	shapes atomic.Int64

	// onIssue is Options.OnIssue. Each Layout/Relayout replays its collected
	// issues into it after the workers join, on the calling goroutine.
	onIssue func(Issue)

	measure bool
	stats   statsCounters
}

// New creates an Engine.
func New(opts Options) (*Engine, error) {
	if opts.Workers < 0 {
		return nil, docErrorf(ErrInvalidOptions, "negative worker count")
	}
	if opts.PageCache < 0 {
		return nil, docErrorf(ErrInvalidOptions, "negative page cache size")
	}
	if opts.TileCache < 0 {
		return nil, docErrorf(ErrInvalidOptions, "negative tile cache size")
	}
	if opts.LineCache < 0 {
		return nil, docErrorf(ErrInvalidOptions, "negative line cache size")
	}
	workers := opts.Workers
	if workers == 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	if workers < 1 {
		workers = 1
	}
	pageCache := opts.PageCache
	if pageCache == 0 {
		pageCache = 4
	}
	tileCache := opts.TileCache
	if tileCache == 0 {
		tileCache = 8
	}
	lineCache := opts.LineCache
	if lineCache == 0 {
		lineCache = 256
	}
	return &Engine{
		workers:   workers,
		pageCache: pageCache,
		tileCache: tileCache,
		lineCache: lineCache,
		limits:    opts.Limits.withDefaults(),
		budget:    newBudget(workers),
		onIssue:   opts.OnIssue,
		measure:   opts.Measure,
		stats:     statsCounters{busy: make([]atomic.Int64, workers)},
	}, nil
}

// Workers returns the configured worker count.
func (e *Engine) Workers() int { return e.workers }
