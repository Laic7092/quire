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

// tileCache holds a scrolling column's tiles as pixels, drawn by the window's
// worker (tileworker.go) and composited here: a frame of a glide submits one
// quad a visible tile instead of one quad a glyph, and none of them are drawn in
// the frame that shows them.
//
// A tile's pixels bake the paper, the ink, the ratio and the subpixel place they
// were drawn at, so the cache belongs to one layout, palette and screen scale:
// tileKey names that set, and another one means a new cache.
type tileCache struct {
	key   tileKey
	entry map[int]*tileImage
	// asked is the frame each tile missing was last asked for, so a frame does
	// not ask again for one the worker has not started.
	asked map[int]int64
	total int
	tick  int64
}

// tileKey is what the tiles were drawn for.
type tileKey struct {
	layout *render.Layout
	scale  float32
	width  float32
	height float32
	fade   float32
	paper  paint.Color
}

// tileImage is one tile kept as pixels, and what it costs to keep them. above is
// the strip the bitmap holds over the tile's own top edge, which the composite
// takes back off its position.
type tileImage struct {
	bmp   *ui.Bitmap
	above float32
	bytes int
	at    int64
}

// tileBudget is how much of the tiles' pixels are kept, in bytes: the visible
// tiles, the ones beside them, and no more. A phone's are 3 MB a tile at a
// phone's ratio, and the renderer holds a texture of each it shows.
const tileBudget = 24 << 20

// tileStrip is how much of the column a tile's bitmap holds above and below the
// tile itself: a line belongs to the tile that holds its top edge, its ink
// reaches past that edge both ways, and a tight leading lets it reach further
// than its line box. Both strips are transparent, since an opaque paper one
// would cover the neighbouring tile's first or last line.
func (a *app) tileStrip() float32 {
	return a.opts.FontSize * (a.opts.LineHeight + 1)
}

// tileKeyNow is what this frame's tiles are drawn for.
func (a *app) tileKeyNow(column *render.Layout, scale float32) tileKey {
	pal := a.palette()
	return tileKey{layout: column, scale: scale, width: a.boxW, height: a.tileH, fade: a.paint.Fade(), paper: pal.Paper}
}

// syncTiles forgets every tile when what they were drawn for changed.
func (a *app) syncTiles(key tileKey) {
	if a.tiles.entry == nil || key != a.tiles.key || !tileBitmaps {
		a.tiles = tileCache{key: key, entry: make(map[int]*tileImage)}
	}
	a.tiles.tick++
}

// tileWorkerOf starts the window's worker the first time a tile is wanted: a
// window that never scrolls never starts one.
func (a *app) tileWorkerOf() *tileWorker {
	if a.tileWork == nil && tileBitmaps {
		a.tileWork = newTileWorker(a.fonts)
	}
	return a.tileWork
}

// pumpTiles takes what the worker finished since the last frame, and shows it in
// this one. It never waits: a tile that is not there yet is replayed instead.
func (a *app) pumpTiles() {
	if a.tiles.entry == nil || a.tileWork == nil {
		return
	}
	got := a.tileWork.take()
	if len(got) == 0 {
		return
	}
	for _, d := range got {
		if d.key != a.tiles.key || d.pix == nil {
			continue
		}
		if _, ok := a.tiles.entry[d.index]; ok {
			continue
		}
		w, h := d.pix.Bounds().Dx(), d.pix.Bounds().Dy()
		e := &tileImage{bmp: ui.NewBitmap(d.pix), above: a.tileStrip(), bytes: w * h * 4, at: a.tiles.tick}
		a.tiles.entry[d.index] = e
		a.tiles.total += e.bytes
	}
	// What came back is a frame to draw.
	a.services.Invalidate()
}

// askTile hands one tile to the worker, unless it is already here or on its way.
func (a *app) askTile(key tileKey, i int, tile *render.Tile, scale float32) {
	if !tileBitmaps || tile == nil || !(scale > 0) || key != a.tiles.key {
		return
	}
	w := a.tileWorkerOf()
	if w == nil {
		return
	}
	if _, ok := a.tiles.entry[i]; ok {
		return
	}
	if a.tiles.asked != nil && a.tiles.asked[i] == a.tiles.tick {
		return
	}
	if a.tiles.asked == nil {
		a.tiles.asked = make(map[int]int64)
	}
	a.tiles.asked[i] = a.tiles.tick
	w.ask(tileJob{
		key: key, index: i, tile: tile,
		w: tile.Size().Width, h: tile.Height(), strip: a.tileStrip(),
		ratio: scale, fade: a.paint.Fade(), pal: a.palette(),
	})
}

// drawTileBitmap puts one tile's pixels on the screen, one to one with the
// device pixels, wherever the column has carried it. The position is snapped to
// the device grid for the same reason the column is: a bitmap drawn between two
// pixels would be resampled, and every glyph in it blurred by the motion.
func drawTileBitmap(p *ui.Painter, e *tileImage, box ui.Rect, scale float32) bool {
	if p == nil || e == nil || e.bmp == nil || !(scale > 0) {
		return false
	}
	w, h := e.bmp.Size()
	if w <= 0 || h <= 0 {
		return false
	}
	dst := ui.Rect{
		X: roundDevices(box.X, scale),
		Y: roundDevices(box.Y-e.above, scale),
		W: float32(w) / scale,
		H: float32(h) / scale,
	}
	p.Image(e.bmp, dst, ui.FillBox)
	return true
}

// trimTiles drops the least recently shown tiles beyond tileBudget, never one
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
		delete(a.tiles.asked, index)
	}
}

// dropTiles forgets every tile, as another layout, paper or book does.
func (a *app) dropTiles() {
	a.tiles = tileCache{}
}
