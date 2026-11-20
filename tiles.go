package main

import (
	"math"
	"os"

	"github.com/egoist/mygo/ui"
	"github.com/laic7092/layx/render"
	"github.com/laic7092/layx/render/paint"
)

// tileBitmaps, cleared by QUIRE_TILE_BITMAPS=0, is the switch a reader flips to
// feel what compositing rastered tiles is worth: off, every frame replays the
// column's display lists and submits one quad a glyph.
var tileBitmaps = os.Getenv("QUIRE_TILE_BITMAPS") != "0"

// tileCache holds a scrolling column's tiles as pixels. A tile is drawn once
// into a canvas of its own and composited from then on, so a frame of a glide
// submits one quad a visible tile instead of one quad a glyph.
//
// The tile is drawn through the same UIPainter the window's column would be
// drawn with, at the screen's ratio: the glyphs come from the same atlas, the
// bold is the atlas's own, the tone is the same table. There is no second
// rasterizer and so nothing for the two paths to disagree about; the canvas's
// pixels are the device pixels a frame would have painted.
//
// A canvas bakes the paper, the ink, the ratio and the subpixel place it was
// drawn at, so the cache belongs to one layout, palette and screen scale:
// tileKey names that set, and another one means a new cache.
type tileCache struct {
	key   tileKey
	entry map[int]*tileImage
	total int
	tick  int64
}

// tileKey is what the canvases were drawn for.
type tileKey struct {
	layout *render.Layout
	scale  float32
	width  float32
	height float32
	fade   float32
	paper  paint.Color
}

// tileImage is one tile's canvas and what it costs to keep it. above is the
// strip the canvas holds above the tile's own top edge, which the composite
// takes back off its position.
type tileImage struct {
	canvas *ui.Canvas
	bytes  int
	at     int64
	above  float32
}

// tileBudget is how much of the tiles' pixels are kept, in bytes: enough for
// the visible tiles and the ones beside them at a phone's ratio, and no more.
// They are memory in the window's own texture, which is the price of drawing a
// tile once instead of a glyph a frame.
const tileBudget = 48 << 20

// tileCanvas returns the tile at i as a canvas, drawing it the first time. It
// returns nil when the tile cannot be drawn, and the caller then replays the
// display list for that tile this frame.
func (a *app) tileCanvas(p *ui.Painter, column *render.Layout, i int, tile *render.Tile, scale float32) *ui.Canvas {
	if a.paint == nil || p == nil || column == nil || tile == nil || !(scale > 0) || !tileBitmaps {
		return nil
	}
	pal := a.palette()
	key := tileKey{
		layout: column, scale: scale, width: a.boxW, height: a.tileH,
		fade: a.paint.Fade(), paper: pal.Paper,
	}
	if key != a.tiles.key || a.tiles.entry == nil {
		a.tiles = tileCache{key: key, entry: make(map[int]*tileImage)}
	}
	a.tiles.tick++
	now := a.tiles.tick
	if e := a.tiles.entry[i]; e != nil {
		e.at = now
		return e.canvas
	}
	// A line belongs to the tile that holds its top edge, but its ink reaches
	// past that edge both ways, and a tight leading lets it reach further than
	// its line box: the canvas keeps a line's height — plus an em — on either
	// side, so nothing of a line is cut at a boundary. Both strips are
	// transparent: an opaque paper one would cover the neighbouring tile's
	// first or last line.
	strip := a.opts.FontSize * (a.opts.LineHeight + 1)
	w, h := tile.Size().Width, tile.Height()
	cw := int(math.Ceil(float64(w) * float64(scale)))
	ch := int(math.Ceil(float64(h+2*strip) * float64(scale)))
	if cw <= 0 || ch <= 0 {
		return nil
	}
	cv := ui.NewCanvas(cw, ch)
	box := ui.Rect{X: 0, Y: strip, W: w, H: h}
	view := ui.Rect{X: 0, Y: 0, W: w, H: h + 2*strip}
	cv.Draw(p, func(cp *ui.Painter) {
		a.paint.DrawTileZoom(cp, tile, box, view, scale)
	})
	e := &tileImage{canvas: cv, bytes: cw * ch * 4, at: now, above: strip}
	a.tiles.entry[i] = e
	a.tiles.total += e.bytes
	// The canvas is drawn into this frame's scene, and the glyphs it took from
	// the atlas are uploaded after this frame: one more frame shows both.
	p.AnimationFrame()
	return cv
}

// drawTileCanvas puts one tile's canvas on the screen, its pixels one to one
// with the screen's, wherever the column has carried it. The position is snapped
// to the device grid for the same reason the column is: a canvas drawn between
// two pixels would be resampled, and every glyph in it blurred by the motion.
func drawTileCanvas(p *ui.Painter, cv *ui.Canvas, box ui.Rect, scale, above float32) {
	if p == nil || cv == nil || !(scale > 0) {
		return
	}
	bmp := cv.Bitmap()
	w, h := cv.Size()
	if bmp == nil || w <= 0 || h <= 0 {
		return
	}
	dst := ui.Rect{
		X: roundDevices(box.X, scale),
		Y: roundDevices(box.Y-above, scale),
		W: float32(w) / scale,
		H: float32(h) / scale,
	}
	p.Image(bmp, dst, ui.FillBox)
}

// prefetchTile draws one tile beside the viewport, so the frame that first
// shows it is a composite too. One a frame is enough: a tile is a screenful,
// and it takes more than a frame of a glide to reach the next one.
func (a *app) prefetchTile(p *ui.Painter, column *render.Layout, i int, scale float32) {
	if !tileBitmaps || column == nil || p == nil || i < 0 || i >= column.TileCount() {
		return
	}
	if _, ok := a.tiles.entry[i]; ok {
		return
	}
	if tile := column.Tile(i); tile != nil {
		a.tileCanvas(p, column, i, tile, scale)
	}
}

// trimTiles drops the least recently drawn tiles beyond tileBudget, never one
// the frame is showing. Everything else is a tile the reader is about to come
// back to, or one a glide left behind.
func (a *app) trimTiles(visible func(int) bool) {
	for len(a.tiles.entry) > 1 && a.tiles.total > tileBudget {
		oldest, index := int64(math.MaxInt64), -1
		for i, e := range a.tiles.entry {
			if visible != nil && visible(i) {
				continue
			}
			if e.at < oldest {
				oldest, index = e.at, i
			}
		}
		if index < 0 {
			return
		}
		a.tiles.total -= a.tiles.entry[index].bytes
		delete(a.tiles.entry, index)
	}
}

// dropTiles forgets every tile's canvas, as another layout, paper or book does.
func (a *app) dropTiles() {
	a.tiles = tileCache{}
}
