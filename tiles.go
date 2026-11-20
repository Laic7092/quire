package main

import (
	"image"
	"math"
	"os"

	"github.com/egoist/mygo/ui"
	"github.com/laic7092/layx/render"
	"github.com/laic7092/layx/render/paint"
	"github.com/laix/quire/internal/reader"
)

// tileBitmaps, cleared by QUIRE_TILE_BITMAPS=0, is the switch a reader flips to
// feel what compositing rastered tiles is worth: off, every frame replays the
// column's display lists and submits one quad a glyph.
var tileBitmaps = os.Getenv("QUIRE_TILE_BITMAPS") != "0"

// tileCache holds a scrolling column's tiles as pixels: a tile's display list is
// replayed into a bitmap once, and the frames after that composite the bitmaps.
// A frame of a glide then submits one quad a visible tile instead of one quad a
// glyph — a couple instead of some nine hundred — so what a scrolling frame
// costs follows the screen and not the number of characters on it.
//
// A bitmap bakes the paper, the ink, the ratio and the subpixel place it was
// rastered at, so the cache belongs to one layout, palette and screen scale:
// tileKey names that set, and another one means a new cache.
type tileCache struct {
	key      tileKey
	entry    map[int]*tileImage
	renderer *reader.PageRenderer
	total    int
	tick     int64
}

// tileKey is what the bitmaps were rastered for: another layout, screen scale
// or paper means the pictures are of a column that is no longer there.
type tileKey struct {
	layout *render.Layout
	scale  float32
	width  float32
	height float32
	fade   float32
	paper  paint.Color
}

// tileImage is one rastered tile and what it costs to keep it. above is the
// strip the bitmap holds above the tile's own top edge (see DrawTileSpan),
// which the composite has to take back off its position.
type tileImage struct {
	bmp   *ui.Bitmap
	bytes int
	at    int64
	above float32
}

// tileBudget is how much of the tiles' bitmaps are kept, in bytes: enough for
// the visible tiles and the one beside them at a phone's ratio, and no more.
// They are memory in the window's own texture, which is the price of drawing a
// tile once instead of a glyph a frame.
const tileBudget = 48 << 20

// tileBitmap returns the tile at i as a bitmap, rastering it the first time.
// It returns nil when the tile cannot be rastered, and the caller then replays
// the display list for that tile this frame.
func (a *app) tileBitmap(column *render.Layout, i int, tile *render.Tile, scale float32) *ui.Bitmap {
	if a.paint == nil || column == nil || tile == nil || !(scale > 0) || !tileBitmaps {
		return nil
	}
	pal := a.palette()
	key := tileKey{
		layout: column, scale: scale, width: a.boxW, height: a.tileH,
		fade: a.paint.Fade(), paper: pal.Paper,
	}
	if key != a.tiles.key || a.tiles.renderer == nil {
		// Another layout, paper or screen: what the bitmaps hold is a picture
		// of a column that is no longer there.
		a.tiles = tileCache{key: key, entry: make(map[int]*tileImage), renderer: reader.NewPageRendererWithPalette(pal)}
		for id, data := range a.images {
			a.tiles.renderer.SetImage(id, data)
		}
	}
	a.tiles.tick++
	now := a.tiles.tick
	if e := a.tiles.entry[i]; e != nil {
		e.at = now
		return e.bmp
	}
	// A line belongs to the tile that holds its top edge, but its ink reaches
	// past that edge both ways, and a tight leading lets it reach further than
	// its line box: the surface keeps a line's height — plus an em — on either
	// side, so nothing of a line is cut at a boundary.
	strip := a.opts.FontSize * (a.opts.LineHeight + 1)
	img := a.tiles.renderer.DrawTileSpan(tile, scale, strip, strip)
	// The renderer keeps one surface and hands out its pixels; a bitmap adopts
	// the pixels it is given. Copy them, or every cache entry would be the last
	// tile rastered, drawn again and again down the column.
	pixels := image.NewRGBA(img.Rect)
	copy(pixels.Pix, img.Pix)
	bmp := ui.NewBitmap(pixels)
	if bmp == nil {
		return nil
	}
	w, h := bmp.Size()
	if w <= 0 || h <= 0 {
		return nil
	}
	e := &tileImage{bmp: bmp, bytes: w * h * 4, at: now, above: strip}
	a.tiles.entry[i] = e
	a.tiles.total += e.bytes
	return bmp
}

// drawTileBitmap puts one rastered tile on the screen, one device pixel to one
// device pixel, wherever the column has carried it. The position is snapped to
// the device grid for the same reason the column is: a bitmap drawn between two
// pixels would be resampled, and every glyph in it blurred by the motion.
func drawTileBitmap(p *ui.Painter, bmp *ui.Bitmap, box ui.Rect, scale, above float32) {
	if p == nil || bmp == nil || !(scale > 0) {
		return
	}
	w, h := bmp.Size()
	if w <= 0 || h <= 0 {
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

// prefetchTile rasterizes one tile beside the viewport, so the frame that first
// shows it is a composite too. One a frame is enough: a tile is a screenful,
// and it takes more than a frame of a glide to reach the next one.
func (a *app) prefetchTile(column *render.Layout, i int, scale float32) {
	if !tileBitmaps || column == nil || i < 0 || i >= column.TileCount() {
		return
	}
	if _, ok := a.tiles.entry[i]; ok {
		return
	}
	if tile := column.Tile(i); tile != nil {
		a.tileBitmap(column, i, tile, scale)
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

// dropTiles forgets every rastered tile, as another layout, paper or book does.
func (a *app) dropTiles() {
	a.tiles = tileCache{}
}
