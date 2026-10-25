package reader

import (
	"image"
	"image/draw"
	"math"
	"os"

	"github.com/egoist/mygo/ui"
	ot "github.com/go-text/typesetting/font/opentype"
	"github.com/laix/layx/render/paint"
	"golang.org/x/image/vector"
)

// Drawing a page's glyphs from their outlines costs the painter more than the
// pixels do: every frame turns each run into a ui.Path, flattens it and hashes
// it for the engine's mask atlas. The same handful of glyphs, at the same few
// places on the device grid, come back frame after frame while a page is read.
//
// So they are rasterized once into an atlas of coverage, and drawn as images
// after that: a lookup and an image blit a frame instead of flatten and hash.
// The atlas holds white with the coverage in its alpha, so its colours follow
// the run's tint and a theme or a fade change rebuilds nothing.
//
// LAYX_GLYPH_ATLAS=1 or 0 forces the atlas or the outlines, for A/B and
// rollback; unset, the atlas is used where the frame is drawn on a GPU (see
// (*UIPainter).atlasWanted).
var glyphAtlasMode = os.Getenv("LAYX_GLYPH_ATLAS")

// glyphStatsOn, set by LAYX_GLYPH_STATS=1, counts what the painter does with
// glyphs, so a test, a benchmark or the app's performance log can say where a
// frame went.
var glyphStatsOn = os.Getenv("LAYX_GLYPH_STATS") != ""

// CountGlyphs turns the glyph counters on or off. The performance log turns
// them on for a session: an upload of an atlas layer is what a real machine
// notices and a headless benchmark does not.
func CountGlyphs(on bool) { glyphStatsOn = on }

const (
	// glyphLayerW and glyphLayerH size one atlas layer. A layer is a ui.Bitmap
	// and the unit the engine uploads when it changes, so it also sets what a
	// frame that adds glyphs costs to upload: 1024×512 pixels of RGBA is 2 MB.
	glyphLayerW, glyphLayerH = 1024, 512
	// glyphAtlasBudget is how much memory the layers may take before the least
	// recently used one goes. A glyph costs four bytes a pixel here, where the
	// engine's own mask atlas spends one, so this holds about three thousand
	// glyphs of a 17px book face at DPR 2.
	glyphAtlasBudget = 12 << 20
	// glyphPhases is how many places on the device grid a glyph's origin is
	// rasterized at, per axis: quarters of a pixel, which is the precision the
	// mask path snapped a glyph's outline to as well.
	glyphPhases = 4
)

// glyphAtlas is the glyphs the painter has rasterized, packed into layers.
// It belongs to one UIPainter and is used on the UI thread only.
type glyphAtlas struct {
	layers []*glyphLayer
	index  map[glyphKey]*glyphSlot
	bytes  int
	// raster reuses its buffers from one glyph to the next.
	raster  vector.Rasterizer
	scratch []uint8
	// pass is the buffer a grown glyph's second and later outlines rasterize
	// into, before the widest of them is kept.
	pass []uint8
	// stats is where the counters go, nil unless LAYX_GLYPH_STATS is set.
	stats *GlyphStats
	tick  uint32
}

// glyphKey names one rasterized glyph: the face and the glyph in it, the size
// and the device pixel ratio it is drawn at, and the place its origin falls on
// the device grid, in quarters of a pixel. The same glyph at another phase has
// its coverage in other pixels, so it is another bitmap.
//
// The ink is part of the key as well, and so is whether the glyph is grown:
// the correction of a glyph's coverage is the ink's (see texttone.go), and a
// booked body's ink and a link's are corrected differently, so they cannot
// share one bitmap.
type glyphKey struct {
	face  *glyphFace
	gid   uint32
	size  float32 // quantized to half a pixel
	dpr   float32
	phase uint8    // x in the low two bits, y in the high two
	tone  *toneLUT // nil when the coverage is drawn as rasterized
	bold  bool
}

// glyphSlot is where one glyph's coverage lies in the atlas, and where it goes
// on the device grid: (kx, ky) is from the glyph's device pixel origin to the
// bitmap's top-left corner, so drawing it needs only the origin's integer part,
// which is the same wherever the glyph is on the page.
type glyphSlot struct {
	layer  *glyphLayer
	x, y   int // in the layer
	w, h   int
	kx, ky int
}

// glyphLayer is one ui.Bitmap the glyphs are packed into, a shelf at a time: a
// row of glyphs as tall as its tallest, then the next row below.
type glyphLayer struct {
	bmp   *ui.Bitmap
	img   *image.RGBA
	keys  []glyphKey // what to forget when the layer goes
	x, y  int        // the free end of the shelf being filled
	shelf int        // how tall that shelf is
	used  int
	dirty bool
	last  uint32 // the tick of its latest use
}

// atlas returns the painter's atlas, made on first use.
func (u *UIPainter) atlas() *glyphAtlas {
	if u.glyphs == nil {
		u.glyphs = &glyphAtlas{index: make(map[glyphKey]*glyphSlot)}
		if glyphStatsOn {
			u.glyphs.stats = &u.stats
		}
	}
	return u.glyphs
}

// atlasWanted reports whether this frame draws its glyphs from the atlas. On a
// GPU it does: the two paths send the backend one textured quad for every glyph
// either way, and the atlas saves the geometry. Under mygo's own software
// rasterizer it does not, because that one samples an image bilinearly (four
// texels and twelve lerps a pixel), which costs four to five times what one
// byte of mask does: measured on one window, the frames of a glide went from
// 7.8 ms of CPU to 13.2 ms with the atlas there, and from 118 ms to 32 ms on the
// GPU. GlyphStats tells the two apart in a frame's numbers.
func (u *UIPainter) atlasWanted() bool {
	switch glyphAtlasMode {
	case "0":
		return false
	case "1":
		return true
	}
	return u.P != nil && u.P.Accelerated()
}

// GlyphStats is what the painter did with glyphs since the last
// ResetGlyphStats, when LAYX_GLYPH_STATS is set. Runs and Glyphs are every call
// and glyph handed to DrawGlyphs; the rest is the atlas: Lookups served by it,
// Hits that needed no rasterizing, Rasters that did, and its size.
type GlyphStats struct {
	Runs, Glyphs int
	Lookups      int
	Hits         int
	Rasters      int
	// Flattens counts the runs turned into a ui.Path, which the atlas path
	// never does: it is the work the atlas replaced.
	Flattens  int
	Slots     int
	Layers    int
	Bytes     int
	Uploads   int
	Evictions int
}

// HitRate is the share of atlas lookups that needed no rasterizing, from 0 to
// 1; it is 0 before the first lookup.
func (s GlyphStats) HitRate() float64 {
	if s.Lookups == 0 {
		return 0
	}
	return float64(s.Hits) / float64(s.Lookups)
}

// drawGlyphsAtlas draws one run from the atlas, rasterizing the glyphs it does
// not hold yet. gf and upem are the run's face and its units per em.
func (u *UIPainter) drawGlyphsAtlas(run paint.GlyphRun, gf *glyphFace, upem float32) {
	// Everything below is in device pixels: a glyph of size S DIPs at DPR d is
	// S*d tall, and its coverage belongs to the pixels of the screen, so the
	// size and the ratio both name the bitmap.
	dpr := u.P.Scale()
	if !(dpr > 0) {
		dpr = 1
	}
	// Half-pixel sizes share a bitmap: a reading slider that steps through
	// sizes should not fill the atlas with near copies of the same glyph.
	size := float32(math.Round(float64(run.Size)*2)) / 2
	alpha := float32(run.Color.A) / 255 * u.fade
	tint := ui.RGBA(run.Color.R, run.Color.G, run.Color.B, alpha)
	if tint.A == 0 { // faded out: nothing of it would show
		return
	}
	opts := ui.ImageOptions{Tint: tint}
	a := u.atlas()
	// How the run's coverage is corrected is the ink's business, and whether it
	// is grown is the book's: a weight the face does not have is drawn in
	// synthetic bold, as a browser draws one (see texttone.go).
	tone := u.toneFor(run.Color, run.Size)
	bold := run.EmulateBold && !fakeBoldOff
	// The run's origin on the device grid, and the phase it stands at, which is
	// the same for every glyph that shares its offsets.
	ox := float64(u.dx+run.X) * float64(dpr)
	oy := float64(u.dy+run.Y) * float64(dpr)
	us := float64(dpr)
	for i := range run.Glyphs {
		g := &run.Glyphs[i]
		gx := ox + float64(g.X)*us
		gy := oy + float64(g.Y)*us
		slot, ix, iy := a.slot(gf, g.ID, size, dpr, gx, gy, upem, tone, bold)
		if slot == nil {
			continue
		}
		src := ui.Rect{X: float32(slot.x), Y: float32(slot.y), W: float32(slot.w), H: float32(slot.h)}
		dst := ui.Rect{
			X: float32(ix+slot.kx) / dpr,
			Y: float32(iy+slot.ky) / dpr,
			W: float32(slot.w) / dpr,
			H: float32(slot.h) / dpr,
		}
		u.P.ImageRegion(slot.layer.bmp, src, dst, opts)
	}
}

// slot returns the atlas slot of glyph gid of gf drawn with its origin at
// device (gx, gy), rasterizing it if the atlas does not hold it yet, and the
// device pixel that origin falls on. A nil slot means the glyph has no ink, or
// no room was left for it.
func (a *glyphAtlas) slot(gf *glyphFace, gid uint32, size, dpr float32, gx, gy float64, upem float32, tone *toneLUT, bold bool) (*glyphSlot, int, int) {
	ix, px := glyphPhase(gx)
	iy, py := glyphPhase(gy)
	key := glyphKey{face: gf, gid: gid, size: size, dpr: dpr, phase: uint8(px | py<<2), tone: tone, bold: bold}
	if slot, ok := a.index[key]; ok {
		if slot != nil {
			a.touch(slot.layer)
			if a.stats != nil {
				a.stats.Lookups++
				a.stats.Hits++
			}
		}
		return slot, ix, iy
	}
	out := gf.outline(gid)
	if out == nil || len(out.segs) == 0 {
		a.index[key] = nil // a blank: no ink to draw, at any phase
		return nil, ix, iy
	}
	if a.stats != nil {
		a.stats.Lookups++
	}
	// The outline in device pixels, from the glyph's origin: y down, as the
	// screen has it.
	s := float64(size) * float64(dpr) / float64(upem)
	fx := float64(px) / glyphPhases
	fy := float64(py) / glyphPhases
	// A run whose weight the face does not have is grown by a fraction of a
	// device pixel each way, as a synthetic bold: the box has to hold that too.
	spread := float64(0)
	if bold {
		spread = fakeBoldSpread(float64(size * dpr))
	}
	// The ink's box, rounded out, one spare pixel each way for the coverage of
	// an edge that lands on the box's own boundary.
	left := int(math.Floor(fx + float64(out.minX)*s - spread))
	top := int(math.Floor(fy + float64(out.minY)*s - spread))
	right := int(math.Ceil(fx+float64(out.maxX)*s+spread)) + 1
	bottom := int(math.Ceil(fy+float64(out.maxY)*s+spread)) + 1
	w, h := right-left, bottom-top
	if w <= 0 || h <= 0 || w > glyphLayerW || h > glyphLayerH {
		a.index[key] = nil
		return nil, ix, iy
	}
	// The box above holds the control points' hull, which is wider than the ink
	// for a curved glyph. Rasterize it, then keep only the pixels the outline
	// actually reaches: a slot costs its area in the atlas, and every pixel of
	// it is sampled again every frame it shows.
	cov := a.coverage(w, h, out, s, fx, fy, left, top, tone, spread)
	x0, y0, x1, y1, ok := trimCoverage(cov, w, h)
	if !ok {
		a.index[key] = nil // nothing of it would show
		return nil, ix, iy
	}
	covW := w
	w, h = x1-x0, y1-y0
	layer, x, y := a.room(w, h)
	if layer == nil {
		a.index[key] = nil
		return nil, ix, iy
	}
	a.copyCoverage(layer, x, y, cov, covW, x0, y0, w, h)
	slot := &glyphSlot{layer: layer, x: x, y: y, w: w, h: h, kx: left + x0, ky: top + y0}
	a.index[key] = slot
	layer.keys = append(layer.keys, key)
	layer.dirty = true
	a.touch(layer)
	a.evict()
	if a.stats != nil {
		a.stats.Rasters++
	}
	return slot, ix, iy
}

// fakeBoldSpread is how far a synthetic bold grows a glyph, in device pixels.
// Skia strokes a fake bold with a pen 0.028 of the size across, and this is the
// radius of the same growth: half of it. The floor and the cap matter more than
// the fraction. A floor of a third of a pixel keeps small print from growing out
// of proportion, and the cap is what a union of nine copies can carry: past
// about two thirds of a pixel the ring of offsets stops reading as a thickened
// edge and starts reading as the same glyph drawn several times, which is what
// a heading at 30 DIP did before the cap.
func fakeBoldSpread(size float64) float64 {
	if fakeBoldOff || size <= 0 {
		return 0
	}
	return min(max(size*0.014, 0.25), 0.6)
}

// boldOffsets are the places a bold glyph's outline is rasterized at, relative
// to its own: the centre and a ring a spread away, which together grow the ink
// by the spread in every direction. Only a synthesized bold pays for
// them, and only once per phase.
var boldOffsets = [8][2]float64{
	{1, 0}, {0.7071, 0.7071}, {0, 1}, {-0.7071, 0.7071},
	{-1, 0}, {-0.7071, -0.7071}, {0, -1}, {0.7071, -0.7071},
}

// coverage rasterizes the outline of out into the atlas's scratch buffer, w by
// h pixels, its ink placed as at the glyph's origin (fx, fy), the buffer's
// corner being (kx, ky) from there. The result is the coverage of each pixel,
// 0 to 255, w bytes to a row, corrected for the ink's tone when tone is not
// nil and grown by spread when it is not zero.
func (a *glyphAtlas) coverage(w, h int, out *glyphOutline, s, fx, fy float64, kx, ky int, tone *toneLUT, spread float64) []uint8 {
	// rasterize draws the outline into buf, moved by (ox, oy) device pixels.
	rasterize := func(buf []uint8, ox, oy float64) {
		z := &a.raster
		z.Reset(w, h)
		z.DrawOp = draw.Src
		// The outline is in font units, y up: to pixels, y down, from the
		// glyph's origin, less the bitmap's corner.
		pt := func(p ot.SegmentPoint) (float32, float32) {
			return float32(fx + float64(p.X)*s - float64(kx) + ox), float32(fy - float64(p.Y)*s - float64(ky) + oy)
		}
		for i := range out.segs {
			seg := &out.segs[i]
			args := seg.ArgsSlice()
			switch seg.Op {
			case ot.SegmentOpMoveTo:
				z.MoveTo(pt(args[0]))
			case ot.SegmentOpLineTo:
				z.LineTo(pt(args[0]))
			case ot.SegmentOpQuadTo:
				bx, by := pt(args[0])
				cx, cy := pt(args[1])
				z.QuadTo(bx, by, cx, cy)
			case ot.SegmentOpCubeTo:
				bx, by := pt(args[0])
				cx, cy := pt(args[1])
				dx, dy := pt(args[2])
				z.CubeTo(bx, by, cx, cy, dx, dy)
			}
		}
		z.ClosePath()
		img := image.Alpha{Pix: buf, Stride: w, Rect: image.Rect(0, 0, w, h)}
		// An opaque source over an Alpha's pixels is the coverage itself.
		z.Draw(&img, img.Rect, image.Opaque, image.Point{})
	}
	dst := a.buf(&a.scratch, w, h)
	if spread <= 0 {
		rasterize(dst, 0, 0)
	} else {
		// Grown ink is the union of the outline drawn a ring of spreads away.
		// The union starts from an empty buffer every time: the scratch holds
		// the glyph before this one, and merging into it would grow a bold glyph
		// by whatever was rasterized last.
		pass := a.buf(&a.pass, w, h)
		clear(dst)
		rasterize(pass, 0, 0)
		for i, v := range pass {
			if v > dst[i] {
				dst[i] = v
			}
		}
		for _, off := range boldOffsets {
			rasterize(pass, off[0]*spread, off[1]*spread)
			for i, v := range pass {
				if v > dst[i] {
					dst[i] = v
				}
			}
		}
	}
	if tone != nil {
		// The ink's correction, baked in: the blend a viewer's eye gets is the
		// one the system's own text printers give it (see texttone.go).
		for i, v := range dst {
			if v != 0 {
				dst[i] = tone[v]
			}
		}
	}
	return dst
}

// buf returns the atlas's reusable w by h byte buffer.
func (a *glyphAtlas) buf(b *[]uint8, w, h int) []uint8 {
	if n := w * h; cap(*b) < n {
		*b = make([]uint8, n)
	} else {
		*b = (*b)[:n]
	}
	return *b
}

// trimCoverage returns the box of cov, w by h, that holds every pixel the
// outline reached.
func trimCoverage(cov []uint8, w, h int) (x0, y0, x1, y1 int, ok bool) {
	x0, y0, x1, y1 = w, h, 0, 0
	for y := 0; y < h; y++ {
		row := cov[y*w:]
		for x := 0; x < w; x++ {
			if row[x] == 0 {
				continue
			}
			x0, x1 = min(x0, x), max(x1, x+1)
			y0, y1 = min(y0, y), max(y1, y+1)
		}
	}
	return x0, y0, x1, y1, x1 > x0
}

// copyCoverage writes the w by h box of cov, covW bytes to a row, that starts
// at (sx, sy) into layer at (x, y), as premultiplied white: the colour a run's
// tint multiplies.
func (a *glyphAtlas) copyCoverage(layer *glyphLayer, x, y int, cov []uint8, covW, sx, sy, w, h int) {
	for row := 0; row < h; row++ {
		src := cov[(sy+row)*covW+sx : (sy+row)*covW+sx+w]
		dst := layer.img.Pix[(y+row)*layer.img.Stride+x*4:]
		for i, v := range src {
			dst[4*i+0], dst[4*i+1], dst[4*i+2], dst[4*i+3] = v, v, v, v
		}
	}
}

// room returns a free w by h rectangle in the layer being filled, opening
// another layer when it is full. A nil layer means the glyph fits in none.
func (a *glyphAtlas) room(w, h int) (*glyphLayer, int, int) {
	if n := len(a.layers); n > 0 {
		if x, y, ok := a.layers[n-1].place(w, h); ok {
			return a.layers[n-1], x, y
		}
	}
	layer := a.newLayer()
	x, y, _ := layer.place(w, h) // an empty layer takes what any layer takes
	return layer, x, y
}

// newLayer opens a layer and lets go of the least recently used one if the
// atlas is over its budget without it.
func (a *glyphAtlas) newLayer() *glyphLayer {
	layer := &glyphLayer{
		img:  image.NewRGBA(image.Rect(0, 0, glyphLayerW, glyphLayerH)),
		used: glyphLayerW * glyphLayerH * 4,
	}
	layer.bmp = ui.NewBitmap(layer.img)
	a.layers = append(a.layers, layer)
	a.bytes += layer.used
	a.touch(layer)
	a.evict()
	return layer
}

// place reserves a w by h rectangle in the layer, on the shelf being filled or
// on the next one.
func (l *glyphLayer) place(w, h int) (int, int, bool) {
	if w > glyphLayerW || h > glyphLayerH {
		return 0, 0, false
	}
	if l.x+w > glyphLayerW {
		l.y += l.shelf
		l.x, l.shelf = 0, 0
	}
	if l.y+h > glyphLayerH {
		return 0, 0, false
	}
	x, y := l.x, l.y
	l.x += w
	l.shelf = max(l.shelf, h)
	return x, y, true
}

// touch marks layer as just used, for the LRU.
func (a *glyphAtlas) touch(layer *glyphLayer) {
	a.tick++
	layer.last = a.tick
}

// evict lets go of whole layers, the least recently used first, while the
// atlas is over its budget. The layer being filled always stays, so a glyph
// never evicts its neighbour, and a frame that needs more than the budget
// keeps what it drew.
func (a *glyphAtlas) evict() {
	for a.bytes > glyphAtlasBudget && len(a.layers) > 1 {
		victim := 0
		for i, l := range a.layers[:len(a.layers)-1] {
			if l.last < a.layers[victim].last {
				victim = i
			}
		}
		layer := a.layers[victim]
		for _, key := range layer.keys {
			delete(a.index, key)
		}
		a.layers = append(a.layers[:victim], a.layers[victim+1:]...)
		a.bytes -= layer.used
		if a.stats != nil {
			a.stats.Evictions++
		}
	}
}

// flush uploads the layers written since the last flush. It must run on the UI
// thread, after the frame's pages are drawn and before the frame is rendered:
// the engine uploads a layer whose version changed, so a layer written after
// that would show its glyphs empty until the next frame.
func (a *glyphAtlas) flush() {
	for _, layer := range a.layers {
		if !layer.dirty {
			continue
		}
		layer.bmp.Update(layer.img)
		layer.dirty = false
		if a.stats != nil {
			a.stats.Uploads++
		}
	}
}

// glyphPhase returns the device pixel d falls on and how far into it, in
// quarters of a pixel: two glyphs that stand the same way in their pixels have
// the same coverage, wherever those pixels are.
func glyphPhase(d float64) (pixel, phase int) {
	q := int(math.Round(d * glyphPhases))
	pixel = int(math.Floor(float64(q) / glyphPhases))
	return pixel, q - pixel*glyphPhases
}
