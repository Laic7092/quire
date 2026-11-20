package render

import "github.com/laix/layx/render/paint"

// Aliases keep the common types reachable from the render package while the
// paint package remains the low-level drawing contract.
type (
	Painter      = paint.Painter
	Color        = paint.Color
	Point        = paint.Point
	Rect         = paint.Rect
	Size         = paint.Size
	Insets       = paint.Insets
	Affine       = paint.Affine
	ImageOptions = paint.ImageOptions
	GlyphRun     = paint.GlyphRun
	Glyph        = paint.Glyph
	Font         = paint.Font
)

// Identity returns the identity affine transform.
func Identity() Affine { return paint.Identity() }
