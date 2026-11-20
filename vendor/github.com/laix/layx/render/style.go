package render

import "github.com/laix/layx/render/paint"

// Slant selects an upright or italic face.
type Slant uint8

const (
	SlantNone Slant = iota
	SlantItalic
)

// TextAlign controls horizontal alignment within the line box.
type TextAlign uint8

const (
	// AlignDefault inherits Typography.TextAlign (which itself defaults to
	// left).
	AlignDefault TextAlign = iota
	AlignLeft
	AlignRight
	AlignCenter
	AlignJustify
)

// TextDecoration selects an optional decorative line drawn with the text.
// It never affects layout.
type TextDecoration uint8

const (
	TextDecorationNone TextDecoration = iota
	TextDecorationUnderline
	TextDecorationLineThrough
)

// Border is one edge of a block box. Width is in CSS pixels; zero means no
// border. Transparent colors draw nothing.
type Border struct {
	Width float32
	Color paint.Color
}

// Style is an already cascaded set of layout properties.
//
// Zero values mean "use the document default" for font family, size, line
// height, color and weight. Margins are absolute CSS pixels; zero means zero.
//
// Every float field must be finite. DocumentBuilder rejects NaN and infinities
// with a *DocumentError wrapping ErrInvalidDocument, naming the StyleID and the
// field; a non-finite value would otherwise reach page geometry and silently
// corrupt the output instead of failing.
//
// Inherited typographic fields treat zero (or the zero enum, or false) as
// "unset". That makes an explicit reset inexpressible: a child cannot cancel a
// parent's FirstLineIndent, LetterSpacing, WordSpacing, Hyphenate, FontSlant or
// TextDecoration with a zero value. Build styles from a fully cascaded tree and
// leave zero only where inheritance is intended.
type Style struct {
	FontFamily string
	FontWeight int
	FontSlant  Slant
	// FontSize is in CSS pixels. Zero uses Typography.FontSize.
	FontSize float32
	// LineHeight is a multiplier of FontSize. Zero uses Typography.LineHeight.
	LineHeight    float32
	LetterSpacing float32
	// WordSpacing adds to the advance of word-separating spaces (U+0020 and
	// U+00A0), like CSS word-spacing. It affects line breaking.
	WordSpacing float32
	// Hyphenate enables intra-word breaks supplied by LayoutOptions.Hyphenator
	// for this block and its descendants. It has no effect when no hyphenator
	// is configured.
	Hyphenate           bool
	Color               paint.Color
	TextAlign           TextAlign
	TextDecoration      TextDecoration
	TextDecorationColor paint.Color
	// FirstLineIndent shifts the first line of a paragraph.
	FirstLineIndent float32

	MarginTop    float32
	MarginBottom float32
	MarginLeft   float32
	MarginRight  float32

	// Border widths shrink the content box like padding; border colors are
	// paint-only. BorderTop/Bottom also add to the block's vertical extent.
	BorderTop    Border
	BorderRight  Border
	BorderBottom Border
	BorderLeft   Border
	// Background paints the block's border box behind its content. It is
	// paint-only. CornerRadius rounds that background and is clamped at draw
	// time; it has no effect on layout and no effect without a background.
	Background   paint.Color
	CornerRadius float32

	BreakBefore bool
	BreakAfter  bool
	// KeepTogether asks the paginator not to split this block if it fits on a
	// single page.
	KeepTogether bool
	// Widows and Orphans override LayoutOptions defaults for this block when
	// non-zero.
	Widows  int
	Orphans int
}
