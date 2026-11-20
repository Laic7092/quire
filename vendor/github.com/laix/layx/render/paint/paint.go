// Package paint defines the drawing contract between the layout engine and a
// host-provided rasterizer.
//
// The package intentionally contains no rasterizer, image decoder or font
// loader. A host implements Painter on top of a GPU, a software rasterizer or
// an e-ink driver.
//
// # Panics
//
// A Painter is host code and must handle its own panics. The library recovers
// panics raised by its own layout workers and converts them to render.ErrInternal
// (releasing the worker's concurrency token there), but it does not wrap calls
// into a Painter: a panic that escapes a Painter method propagates to the caller
// of Page.Draw or Tile.Draw. That includes the Save and Restore calls, so a
// panicking Painter can leave its own state (and its Save/Restore balance)
// inconsistent. Draw is pure replay of an immutable display list, so the library
// has no layout state to repair, but it cannot guarantee anything about the
// host's.
package paint

// Point is a position in device pixels.
type Point struct {
	X, Y float32
}

// Size is a width/height pair in device pixels.
type Size struct {
	Width, Height float32
}

// Rect is an axis-aligned rectangle in device pixels.
type Rect struct {
	X, Y, Width, Height float32
}

// Empty reports whether the rectangle has no area.
func (r Rect) Empty() bool { return r.Width <= 0 || r.Height <= 0 }

// Contains reports whether p is inside r. The top and left edges are included,
// the bottom and right edges are excluded.
func (r Rect) Contains(p Point) bool {
	return p.X >= r.X && p.X < r.X+r.Width && p.Y >= r.Y && p.Y < r.Y+r.Height
}

// Union returns the smallest rectangle containing both r and o.
func (r Rect) Union(o Rect) Rect {
	if r.Empty() {
		return o
	}
	if o.Empty() {
		return r
	}
	x0 := minf(r.X, o.X)
	y0 := minf(r.Y, o.Y)
	x1 := maxf(r.X+r.Width, o.X+o.Width)
	y1 := maxf(r.Y+r.Height, o.Y+o.Height)
	return Rect{X: x0, Y: y0, Width: x1 - x0, Height: y1 - y0}
}

// Insets describes the four edges of a box in device pixels.
type Insets struct {
	Top, Right, Bottom, Left float32
}

func minf(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func maxf(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

// Color is a non-premultiplied 8-bit RGBA color.
type Color struct {
	R, G, B, A uint8
}

// Opaque reports whether c is fully opaque.
func (c Color) Opaque() bool { return c.A == 0xff }

// Transparent reports whether c is fully transparent.
func (c Color) Transparent() bool { return c.A == 0 }

// Affine is a 2x3 affine transform in row-major order:
//
//	| A C E |
//	| B D F |
//	| 0 0 1 |
type Affine struct {
	A, B, C, D, E, F float32
}

// Identity returns the identity transform.
func Identity() Affine { return Affine{A: 1, D: 1} }

// ImageID is an opaque image handle assigned by the host.
//
// The engine only passes it back to Painters. It is scoped to the host's
// resource table, not to a Layout: if a Document is rebuilt with different
// images, a Painter must invalidate caches keyed by ImageID unless the host
// preserves the same mapping. Layout.Document() is a convenient generation
// key.
type ImageID uint32

// ImageOptions carries optional image drawing hints. It is a struct (not a
// variadic option) so that fields can be added without changing Painter.
type ImageOptions struct{}

// Font is an opaque font handle. Painter implementations use Data and Index
// to rasterize glyphs: Data is the original font file and Index selects a face
// inside a font collection (0 for a single-face file).
//
// Data is owned by the render.FontSet and must not be modified.
type Font struct {
	Family string
	Data   []byte
	Index  int
	// Weight is the CSS-style numeric weight of this face (100..900).
	Weight int
}

// Glyph is one positioned glyph.
//
// X and Y are offsets from the owning GlyphRun's origin (the baseline origin
// of the run). Cluster is the rune index, relative to the start of the line,
// that produced this glyph.
type Glyph struct {
	ID      uint32
	X, Y    float32
	Advance float32
	Cluster int32
}

// GlyphRun is a sequence of glyphs from a single font and size, drawn at a
// common baseline origin.
//
// The Glyphs slice is only valid for the duration of the Painter.DrawGlyphs
// call.
type GlyphRun struct {
	Font   *Font
	Size   float32
	Color  Color
	X, Y   float32
	Glyphs []Glyph
	// EmulateBold marks a run whose requested weight was >= 600 but whose
	// matched face is not bold. The core never fakes a face; it only reports
	// the condition so the host can embolden the glyphs (stroke, double draw,
	// synthetic weight) or ignore it. It does not affect advances or layout.
	EmulateBold bool
}

// Painter is the host drawing backend.
//
// Methods are called in display-list order. Save/Restore follow the usual
// stack semantics. Coordinates are in device pixels with the origin at the
// top-left of the target surface. A Draw call brackets its replay with Save and
// Restore, so the painter starts and ends at the caller's transform.
//
// A Painter is not required to be safe for concurrent use. Callers must use
// one Painter per goroutine.
//
// # Panics
//
// A Painter implementation must handle its own panics. The library does not
// recover a panic raised by a Painter method: it propagates to the goroutine
// that called Page.Draw or Tile.Draw, and the closing Restore of that Draw is
// not reached, so the painter's state is left as the panic left it. Recover at
// the boundary of each method (or of each Draw call) if the host must keep
// running; the library cannot do it without hiding host bugs. This is separate
// from the library's own workers, whose panics are recovered and returned as
// render.ErrInternal with their concurrency tokens released.
type Painter interface {
	Save()
	Restore()

	ClipRect(r Rect)
	Transform(m Affine)

	FillRect(r Rect, c Color)
	StrokeRect(r Rect, c Color, width float32)
	FillRoundRect(r Rect, radius float32, c Color)

	DrawImage(id ImageID, dst Rect, opts ImageOptions)
	DrawGlyphs(run GlyphRun)
}

// Clipped is an optional extension implemented by a Painter that knows which
// part of its current user space can affect the output.
//
// Page.Draw and Tile.Draw use it to skip display-list ops whose rectangles do
// not intersect Visible. This is pure replay culling: it does not shape, lay
// out or rasterize, and a Painter that does not implement Clipped receives
// every op. Visible is reported in the same coordinate space as the ops (that
// is, with the painter's current transform already accounted for), and an
// empty Rect means nothing is visible.
type Clipped interface {
	Visible() Rect
}
