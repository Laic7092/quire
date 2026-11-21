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

// warmTiles, cleared by QUIRE_WARM=0, stops the worker from rasterizing the
// glyphs of the tile the column is heading for (see reader.WarmTile): the frame
// rasterizes them itself, which is the rollback and the A/B.
var warmTiles = os.Getenv("QUIRE_WARM") != "0"

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
		a.tileWork = newTileWorker(a.fonts, a.paint)
		// The book was opened before this worker existed: its painter draws the
		// tiles, and a tile with an image in it draws nothing for it.
		for id, data := range a.tileImgs {
			a.tileWork.setImage(id, data)
		}
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
// The renderer's canvases are not the worker's: it draws only when the worker is
// the way the tiles are rastered.
func (a *app) askTile(key tileKey, i int, tile *render.Tile, scale float32) {
	if !tileBitmaps || a.tileGPU() || tile == nil || !(scale > 0) || key != a.tiles.key {
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

// askWarm hands the worker one tile to warm: the glyphs it draws are rasterized
// now, on the worker's thread, for the frame that will bake it. The renderer
// bakes a tile in the frame that first shows it, so a tile the frame has not
// reached yet is the one worth warming (see reader.WarmTile).
func (a *app) askWarm(key tileKey, i int, tile *render.Tile, scale float32) {
	if !warmTiles || tile == nil || !(scale > 0) || key != a.gpu.key || a.paint == nil {
		return
	}
	if a.gpu.entry[i] != nil {
		return // baked already: its glyphs went through this window's painter
	}
	if a.warmAsked == nil {
		a.warmAsked = make(map[int]int64)
	}
	if a.warmAsked[i] == a.gpu.tick {
		return
	}
	a.warmAsked[i] = a.gpu.tick
	w := a.tileWorkerOf()
	if w == nil {
		return
	}
	w.ask(tileJob{key: key, index: i, warm: true, tile: tile, strip: a.tileStrip(), ratio: scale})
}

// tileGPU says whether the tiles are rastered by the renderer, in canvases of
// the GPU's, instead of by the window's worker.
func (a *app) tileGPU() bool {
	if !tileBitmaps || a.desk == nil {
		return false
	}
	return a.desk.tileDraw == tileDrawGPU
}

// tileDrawGPU and tileDrawWorker name the two ways a tile is rastered, as the
// setting and the state file spell them.
const (
	tileDrawGPU    = "gpu"
	tileDrawWorker = "worker"
)

// gpuTile is one tile drawn in a canvas of the renderer's: the display list was
// submitted into it once, and the frame shows it as one quad from then on.
type gpuTile struct {
	cv    *ui.Canvas
	above float32
	at    int64
}

// gpuTiles holds the tiles the renderer drew, and the canvases they are in. A
// canvas is drawn anew by Canvas.Draw, which keeps its texture: the pool is
// canvases whose tile was dropped, never a canvas the cache still shows.
type gpuTiles struct {
	key   tileKey
	entry map[int]*gpuTile
	pool  []*ui.Canvas
	w, h  int
	tick  int64
}

// syncGPU forgets every canvas when what the tiles were drawn for changed.
func (a *app) syncGPU(key tileKey) {
	if !a.tileGPU() {
		return
	}
	if a.gpu.entry == nil || key != a.gpu.key {
		a.dropGPU()
		a.gpu = gpuTiles{key: key, entry: make(map[int]*gpuTile)}
		a.warmAsked = nil
	}
	a.gpu.tick++
}

// dropGPU returns every tile's canvas to the pool, for a new key to draw into.
func (a *app) dropGPU() {
	for _, e := range a.gpu.entry {
		if e != nil && e.cv != nil {
			a.gpu.pool = append(a.gpu.pool, e.cv)
		}
	}
	a.gpu.entry = nil
}

// canvasOf returns a canvas of w×h pixels to draw a tile into: one the pool
// keeps of that size, or a new one. A canvas of another size is not resized:
// its texture is a tile's, and the next tile of this column has the same size.
func (a *app) canvasOf(w, h int) *ui.Canvas {
	if a.gpu.w == w && a.gpu.h == h && len(a.gpu.pool) > 0 {
		cv := a.gpu.pool[len(a.gpu.pool)-1]
		a.gpu.pool = a.gpu.pool[:len(a.gpu.pool)-1]
		return cv
	}
	a.gpu.w, a.gpu.h = w, h
	return ui.NewCanvas(w, h)
}

// drawTileCanvas draws one tile with the renderer and shows it in the same
// frame: a canvas drawn in a frame is rendered before that frame, so what
// Canvas.Draw submits is on screen now rather than next frame, and the pixels
// never come back to the CPU. False when the GPU path is not the one in use.
func (a *app) drawTileCanvas(p *ui.Painter, key tileKey, i int, tile *render.Tile, box, view ui.Rect, scale float32) bool {
	if !a.tileGPU() || p == nil || tile == nil || !(scale > 0) || key != a.gpu.key {
		return false
	}
	if e := a.gpu.entry[i]; e != nil {
		return drawTilePixels(p, e.cv.Bitmap(), e.above, box, scale)
	}
	strip := a.tileStrip()
	tw, th := tile.Size().Width, tile.Height()
	w := int(math.Ceil(float64(tw) * float64(scale)))
	h := int(math.Ceil(float64(th+2*strip) * float64(scale)))
	if w <= 0 || h <= 0 {
		return false
	}
	cv := a.canvasOf(w, h)
	// The canvas is the tile and its two strips, in its own pixels: the tile
	// sits below the strip above it, as the worker's offscreen frame does.
	in := ui.Rect{X: 0, Y: strip, W: tw, H: th}
	of := ui.Rect{W: tw, H: th + 2*strip}
	perf.tileAt(i, func() {
		cv.Draw(p, func(cp *ui.Painter) { a.paint.DrawTileZoom(cp, tile, in, of, scale) })
	})
	a.gpu.entry[i] = &gpuTile{cv: cv, above: strip, at: a.gpu.tick}
	return drawTilePixels(p, cv.Bitmap(), strip, box, scale)
}

// trimGPU keeps the canvases of the tiles near the viewport: one the column has
// scrolled away from goes back to the pool, its texture to be drawn into anew.
func (a *app) trimGPU(visible func(int) bool) {
	const keep = 6
	for len(a.gpu.entry) > keep {
		oldest, index := int64(math.MaxInt64), -1
		for i, e := range a.gpu.entry {
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
		if cv := a.gpu.entry[index].cv; cv != nil {
			a.gpu.pool = append(a.gpu.pool, cv)
		}
		delete(a.gpu.entry, index)
	}
}

// drawTileBitmap puts one tile's pixels on the screen, one to one with the
// device pixels, wherever the column has carried it. The position is snapped to
// the device grid for the same reason the column is: a bitmap drawn between two
// pixels would be resampled, and every glyph in it blurred by the motion.
func drawTileBitmap(p *ui.Painter, e *tileImage, box ui.Rect, scale float32) bool {
	if e == nil {
		return false
	}
	return drawTilePixels(p, e.bmp, e.above, box, scale)
}

// drawTilePixels is a tile's bitmap on the screen, snapped to the device grid.
func drawTilePixels(p *ui.Painter, bmp *ui.Bitmap, above float32, box ui.Rect, scale float32) bool {
	if p == nil || bmp == nil || !(scale > 0) {
		return false
	}
	w, h := bmp.Size()
	if w <= 0 || h <= 0 {
		return false
	}
	dst := ui.Rect{
		X: roundDevices(box.X, scale),
		Y: roundDevices(box.Y-above, scale),
		W: float32(w) / scale,
		H: float32(h) / scale,
	}
	p.Image(bmp, dst, ui.FillBox)
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
	a.dropGPU()
}
