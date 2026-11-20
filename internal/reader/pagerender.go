package reader

import (
	"bytes"
	"image"
	_ "image/gif"  // DecodeImage
	_ "image/jpeg" // DecodeImage
	_ "image/png"  // DecodeImage
	"math"

	"github.com/laic7092/layx/raster"
	"github.com/laic7092/layx/render"
	"github.com/laic7092/layx/render/paint"
	_ "golang.org/x/image/bmp"  // DecodeImage
	_ "golang.org/x/image/webp" // DecodeImage
)

// DecodeImage decodes an encoded page image from Book.Build into the
// image.Image form layx/raster registers. It accepts the formats the reader
// accepts: PNG, JPEG, GIF (its first frame), WebP and BMP.
//
// Unlike MyGo's decoder this does not apply a JPEG's EXIF orientation. Page
// images in a book are upright and re-encoding one to apply a rotation would
// only cost a copy; a camera photo used as a cover is MyGo's path.
func DecodeImage(data []byte) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return img, nil
}

// PageRenderer rasterizes paged and scrolling output offscreen through
// github.com/laic7092/layx/raster, without MyGo, a window or a GPU. It is the
// headless sibling of UIPainter: the app still draws through the native painter,
// while a tool, a test or an exporter uses this to get pixels. Nothing here lays
// out or re-derives style - the page coordinates and the palette are already
// layx's.
//
// One surface is kept and reused, so the image DrawPage or DrawTile returns is
// valid until the next call: encode or copy it before drawing again. Images are
// decoded once and re-registered if the surface has to grow for another page.
//
// Draw on one goroutine. The renderer is not safe for concurrent use, which
// matches layx/raster itself.
type PageRenderer struct {
	surface *raster.Rasterizer
	images  map[paint.ImageID]image.Image
	w, h    int
	// tones is the same ink correction the screen's glyph atlas applies, so a
	// page exported to a PNG carries the weight it has in the window (see
	// texttone.go).
	tones toneCache
	// palette is the paper this renderer was created for. Its sheet is cleared
	// to this paper rather than the process-wide palette, so two renderers can
	// export different themes independently.
	palette Palette
	// bold tells the tone cache the run being drawn is a synthetic bold, since
	// the renderer draws that weight itself rather than letting the rasterizer
	// stamp a second, shifted copy of every glyph over the first.
	bold bool
}

// NewPageRenderer returns a renderer with no surface yet, using the palette in
// use. It is NewPageRendererWithPalette using Current, kept for callers that
// still follow the process-wide theme. The first DrawPage/DrawTile sizes the
// surface to that page.
func NewPageRenderer() *PageRenderer { return NewPageRendererWithPalette(Current()) }

// NewPageRendererWithPalette returns a renderer with no surface yet, using the
// palette given. The palette is captured: a later SetPalette does not change the
// sheet the renderer clears to.
func NewPageRendererWithPalette(palette Palette) *PageRenderer {
	return &PageRenderer{palette: palette}
}

// SetPalette changes the sheet colour a PageRenderer clears to. The surface is
// kept; the next DrawPage/DrawTile uses the new paper.
func (p *PageRenderer) SetPalette(palette Palette) {
	if p != nil {
		p.palette = palette
	}
}

// SetImage decodes data and registers it for the pages drawn afterwards. It
// reports whether the bytes were an image the reader accepts. The mapping is
// kept across draws and surfaces; ClearImages drops it when another book
// replaces the current one and image IDs start over.
func (p *PageRenderer) SetImage(id paint.ImageID, data []byte) bool {
	img, err := DecodeImage(data)
	if err != nil {
		return false
	}
	if p.images == nil {
		p.images = make(map[paint.ImageID]image.Image)
	}
	p.images[id] = img
	if p.surface != nil {
		p.surface.SetImage(id, img)
	}
	return true
}

// ClearImages drops the decoded images and the surface that holds them.
func (p *PageRenderer) ClearImages() {
	p.images = nil
	p.surface = nil
	p.w, p.h = 0, 0
}

// DrawPage rasterizes page at scale device pixels per DIP (1 is one pixel per
// DIP, 2 is Retina). The page's own display list starts with its paper fill, so
// the result is the reading page; the surface is also cleared to the
// renderer's palette paper first, so a display list built without a background
// still comes out on paper rather than on transparency.
func (p *PageRenderer) DrawPage(page *render.Page, scale float32) *image.RGBA {
	if page == nil {
		return image.NewRGBA(image.Rect(0, 0, 0, 0))
	}
	size := page.Size()
	return p.render(size.Width, size.Height, scale, page.Draw)
}

// DrawTile rasterizes one tile of a scrolling layout, the same way.
func (p *PageRenderer) DrawTile(tile *render.Tile, scale float32) *image.RGBA {
	return p.DrawTileSpan(tile, scale, 0, 0)
}

// DrawTilePad is DrawTileSpan with the same strip above and below.
func (p *PageRenderer) DrawTilePad(tile *render.Tile, scale, pad float32) *image.RGBA {
	return p.DrawTileSpan(tile, scale, pad, pad)
}

// DrawTileSpan rasterizes one tile into a surface above DIPs taller at its top
// and below taller at its bottom than the tile itself.
//
// A line belongs to the tile that holds its top edge. Its ink can still reach
// past that edge on both sides — overhanging into the tile beneath, and rising
// above the tile's own top when the leading is tight — so a host that keeps a
// tile as a bitmap needs a strip on either side, and the display list of a tile
// is painted with its clip dropped into it.
func (p *PageRenderer) DrawTileSpan(tile *render.Tile, scale, above, below float32) *image.RGBA {
	if tile == nil {
		return image.NewRGBA(image.Rect(0, 0, 0, 0))
	}
	if above < 0 {
		above = 0
	}
	if below < 0 {
		below = 0
	}
	size := tile.Size()
	// The strips are cleared to nothing, not to paper: they lie over the tiles
	// above and beneath, and opaque paper strips would cover the lines that
	// start just past the boundaries — the text would come out cut in half.
	// Only the ink that reaches into them is painted there.
	return p.renderCleared(paint.Color{}, size.Width, above, size.Height+above+below, scale, func(painter paint.Painter) {
		// A tile clips itself to its own height. The surface is taller than that
		// on purpose, so the clip is dropped and the surface's own one stands.
		tile.Draw(unclipped{painter})
	})
}

// renderPainter is what the display list is replayed into: it puts a run's
// baseline on the device grid, as the window's painter does (see
// UIPainter.baseline), and it centres the rasterizer's fake bold on the run.
type renderPainter struct {
	paint.Painter
	p     *PageRenderer
	scale float32
}

func (b renderPainter) DrawGlyphs(run paint.GlyphRun) {
	if textGrid {
		run.Y = gridBaseline(0, run.Y, b.scale)
	}
	bold := run.EmulateBold && !fakeBoldOff
	b.p.bold = bold
	// The weight is drawn here rather than by the rasterizer, which would stamp
	// a second copy a whole size/24 to the right: at 1.5x and above that copy
	// lands about a pixel away, so every stem comes out with a grey fringe
	// instead of a heavier stroke. Struck twice in the same place, the coverage
	// darkens and the edge thickens without moving anything — a double strike,
	// which is what a face without a bold is given here.
	run.EmulateBold = false
	b.Painter.DrawGlyphs(run)
	if bold {
		b.Painter.DrawGlyphs(run)
	}
	b.p.bold = false
}

// unclipped passes everything to a painter but the clip: a page or tile asked
// for into a surface that is deliberately larger than its own bounds.
type unclipped struct{ paint.Painter }

func (unclipped) ClipRect(paint.Rect) {}

func (p *PageRenderer) render(width, height, scale float32, draw func(paint.Painter)) *image.RGBA {
	return p.renderCleared(p.palette.Paper, width, 0, height, scale, draw)
}

// renderCleared is the render both entry points are made of: the surface is
// cleared to clear first, so a page comes out on paper and a tile's overhang
// strips come out transparent. above is room the drawing is moved down by, for
// a surface that starts above the content it holds.
func (p *PageRenderer) renderCleared(clear paint.Color, width, above, height, scale float32, draw func(paint.Painter)) *image.RGBA {
	// A ratio that is not a real positive number is 1: an export asked for at an
	// impossible ratio draws the page at 1x, where a NaN or an infinite one used
	// to come out as an empty image — a file with no page in it.
	if !finite(scale) || scale <= 0 {
		scale = 1
	}
	w := int(math.Ceil(float64(width) * float64(scale)))
	h := int(math.Ceil(float64(height) * float64(scale)))
	if w <= 0 || h <= 0 {
		return image.NewRGBA(image.Rect(0, 0, 0, 0))
	}

	s := p.surfaceFor(w, h)
	// Reset keeps the parsed fonts and the decoded images but restores the
	// identity transform and the full-surface clip, which is what a page and a
	// tile both expect to start from.
	s.Reset()
	s.Clear(clear)
	if scale != 1 || above > 0 {
		s.Transform(paint.Affine{A: scale, D: scale, F: above * scale})
	}
	// The window's painter puts every baseline on the device grid, so a column
	// has one type phase down its whole length; an offscreen render has to do
	// the same, or a page exported to PNG and the same page on screen would
	// break their lines at different places.
	draw(renderPainter{s, p, scale})
	return s.Image()
}

func (p *PageRenderer) surfaceFor(w, h int) *raster.Rasterizer {
	if p.surface == nil || p.w != w || p.h != h {
		s := raster.New(w, h)
		s.ToneFor = func(run paint.GlyphRun) *raster.Tone {
			return p.tones.toneFor(run.Color, run.Size, p.bold)
		}
		for id, img := range p.images {
			s.SetImage(id, img)
		}
		p.surface, p.w, p.h = s, w, h
	}
	return p.surface
}
