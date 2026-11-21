package reader

import (
	"math"
	"os"
	"sync"

	"github.com/egoist/mygo/ui"
	"github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
	"github.com/laic7092/layx/render"
	"github.com/laic7092/layx/render/paint"
)

// UIPainter implements render's paint.Painter on top of MyGo's native
// ui.Painter. Glyphs are drawn from an atlas of the font's own outlines, one
// bitmap a glyph, as the wasm example draws them from a ui.Path: see
// glyph_atlas.go for what that costs and when the outlines are drawn instead.
//
// The display list always paints in page coordinates starting at (0,0); Draw
// offsets them to the element's rect and clips to it once, so the per-op
// Save/ClipRect/Restore of a page needs no state here.
type UIPainter struct {
	P *ui.Painter

	// faces are the go-text faces of the fonts the runs name, parsed on first
	// use: a page that mixes the serif and sans faces draws from both. The
	// worker's painter shares them (see ShareGlyphCaches), so a face and its
	// outlines are parsed once for the two of them.
	faces *faceCache
	// bitmaps are the images the paginator placed, decoded once per id.
	bitmaps map[paint.ImageID]*ui.Bitmap
	// glyphs is the atlas of rasterized glyphs, made on first use: a page's
	// outlines are turned into pixels once and blitted after that.
	glyphs *glyphAtlas
	// tones are the coverage corrections built so far, one per ink and band of
	// sizes (see texttone.go), shared with the worker's painter: a correction is
	// part of a glyph's key, so two painters correcting one ink apart would
	// rasterize the same glyph twice.
	tones *toneCache
	// covers is the rasterized glyphs this painter and the worker's share: a
	// glyph one of them rasterized is a copy for the other (see glyphcover.go).
	covers *GlyphCovers
	// warming says this painter replays a display list for its glyphs alone,
	// rasterizing them for the frame that will draw them (see WarmTile): it has
	// no surface, and nothing it is handed reaches one.
	warming bool
	stats   GlyphStats
	dx, dy  float32
	// fade multiplies the alpha of everything drawn, so a host can dissolve one
	// page over another. It is 1 unless SetFade says otherwise.
	fade float32
	// dpr overrides the ratio the painter draws at, for drawing into a surface
	// of its own: a canvas of device pixels, whose painter is in canvas pixels
	// and so reports 1. Zero follows the window's painter.
	dpr float32
	// zoom multiplies every coordinate handed to the surface: a canvas painter
	// works in its own pixels, so a display list in DIPs is drawn through this.
	// Zero means 1, the window's own painter.
	zoom float32
	// palette is the paper and ink this painter was created for. DrawCover uses
	// its paper rather than the process-wide palette, so two painters can draw
	// two themes at once.
	palette Palette
	// noCull turns off skipping the runs outside the clip, so a test can
	// paint them and prove the skip changes no pixel. It turns off the painter's
	// Visible as well, so the test covers both culls: the painter's own, which
	// drops a run by its baseline, and render's replay, which drops every op
	// whose rectangle misses the viewport.
	noCull bool
}

// faceCache is the faces the painter draws from and the outlines it has taken
// from them, keyed by the font the display list names. Two painters that draw
// one window's text share one, on two threads.
type faceCache struct {
	mu sync.Mutex
	m  map[*paint.Font]*glyphFace
}

// of returns the face of f, parsed on first use.
func (c *faceCache) of(f *paint.Font) *glyphFace {
	if f == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if gf, ok := c.m[f]; ok {
		return gf
	}
	face := faceOf(f.Data, f.Index)
	if face == nil {
		return nil
	}
	if c.m == nil {
		c.m = make(map[*paint.Font]*glyphFace)
	}
	gf := &glyphFace{face: face, outlines: make(map[uint32]*glyphOutline)}
	c.m[f] = gf
	return gf
}

// all returns the faces parsed so far, for a test that walks them.
func (c *faceCache) all() []*glyphFace {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*glyphFace, 0, len(c.m))
	for _, gf := range c.m {
		out = append(out, gf)
	}
	return out
}

// glyphFace is one parsed face and the outlines it has handed out so far.
type glyphFace struct {
	face     *font.Face
	mu       sync.Mutex
	outlines map[uint32]*glyphOutline
}

// glyphOutline is one glyph's outline, cached: the segments in font units, y up
// as the font has them, and the box they cover in the screen's own way, y down.
// Control points bound a curve, so the box is the ink's, rounded out.
type glyphOutline struct {
	segs                   []font.Segment
	minX, minY, maxX, maxY float32
}

// ink sets the box the outline covers, y down.
func (o *glyphOutline) ink() {
	o.minX, o.minY = float32(math.MaxFloat32), float32(math.MaxFloat32)
	o.maxX, o.maxY = -float32(math.MaxFloat32), -float32(math.MaxFloat32)
	for i := range o.segs {
		for _, p := range o.segs[i].ArgsSlice() {
			o.minX, o.maxX = min(o.minX, p.X), max(o.maxX, p.X)
			o.minY, o.maxY = min(o.minY, -p.Y), max(o.maxY, -p.Y)
		}
	}
	if len(o.segs) == 0 {
		o.minX, o.minY, o.maxX, o.maxY = 0, 0, 0, 0
	}
}

// NewUIPainter prepares a painter for one element with the palette in use. It
// is NewUIPainterWithPalette using Current, kept for callers that still follow
// the process-wide theme. The outline and image caches live as long as the
// painter, so keep it for the whole window and reuse it. Run the SetImage calls
// on the UI thread.
func NewUIPainter(p *ui.Painter, fonts *Fonts) *UIPainter {
	return NewUIPainterWithPalette(p, fonts, Current())
}

// NewUIPainterWithPalette prepares a painter for one element with the palette
// given. It captures the palette: a later SetPalette does not change the paper
// this painter draws under a cover.
func NewUIPainterWithPalette(p *ui.Painter, fonts *Fonts, palette Palette) *UIPainter {
	return &UIPainter{
		P:       p,
		faces:   &faceCache{},
		tones:   &toneCache{},
		bitmaps: make(map[paint.ImageID]*ui.Bitmap),
		fade:    1,
		palette: palette,
	}
}

// SetFade makes what the painter draws next translucent: f multiplies the alpha
// of every sheet, run, image and veil, so a host can dissolve one page over
// another. The pages start with their own opaque sheet, so drawing the arriving
// page at f over the leaving one is a cross-fade of the two, text and paper
// alike. f is clamped to 0..1, and drawing is opaque after SetFade(1).
// SetPalette changes the paper a UIPainter draws under a cover. Page display
// lists carry their own colours; this only affects painter-owned chrome such as
// the sheet behind a cover, so glyph and image caches are preserved.
func (u *UIPainter) SetPalette(palette Palette) {
	if u != nil {
		u.palette = palette
	}
}

func (u *UIPainter) SetFade(f float32) {
	if u == nil {
		return
	}
	if f != f { // a progress that never arrived draws the page, not a blank one
		f = 1
	}
	u.fade = min(max(f, 0), 1)
}

// Fade returns the translucency the painter draws at, 1 when opaque. A host
// that bakes what it draws into a bitmap keys that bitmap on it.
func (u *UIPainter) Fade() float32 {
	if u == nil {
		return 1
	}
	return u.fade
}

// SetScale makes the painter draw as if the window's ratio were dpr, which is
// what an offscreen surface needs: its own painter reports 1, while the pixels
// it holds are a device pixel each. Zero follows the painter it is given.
func (u *UIPainter) SetScale(dpr float32) {
	if u == nil {
		return
	}
	u.dpr = dpr
}

// z is how many surface pixels a DIP becomes: the canvas's zoom, or 1 for the
// window's own painter.
func (u *UIPainter) z() float32 {
	if u == nil || u.zoom <= 0 {
		return 1
	}
	return u.zoom
}

// scale is the ratio this painter draws at: the canvas's, when it has one.
func (u *UIPainter) scale() float32 {
	if u == nil {
		return 1
	}
	if u.dpr > 0 {
		return u.dpr
	}
	if u.P != nil && u.P.Scale() > 0 {
		return u.P.Scale()
	}
	return 1
}

// faded is c with the painter's fade in its alpha.
func (u *UIPainter) faded(c paint.Color) paint.Color {
	if u.fade >= 1 {
		return c
	}
	c.A = uint8(float32(c.A) * u.fade)
	return c
}

// SetImage decodes data once and keeps the bitmap for id. It reports whether
// the image is there to draw afterwards; formats beyond PNG, JPEG, GIF, WebP
// and BMP (SVG, say) are skipped. Call it on the UI thread, after a build
// produced the bytes. An id that is already decoded is left alone: a host may
// hand the same image again, and the bytes it hands may be gone by then.
func (u *UIPainter) SetImage(id paint.ImageID, data []byte) bool {
	if u == nil {
		return false
	}
	if _, ok := u.bitmaps[id]; ok {
		return true
	}
	if len(data) == 0 {
		return false
	}
	bitmap, err := ui.DecodeBitmap(data)
	if err != nil {
		return false
	}
	if u.bitmaps == nil {
		u.bitmaps = make(map[paint.ImageID]*ui.Bitmap)
	}
	u.bitmaps[id] = bitmap
	return true
}

// ClearImages drops the decoded images, as when another book replaces the
// current one and the image IDs start over.
func (u *UIPainter) ClearImages() {
	if u != nil {
		u.bitmaps = make(map[paint.ImageID]*ui.Bitmap)
	}
}

// Bitmap returns the decoded image for id, or nil. A host that shows a cover
// outside the page flow draws this bitmap itself.
func (u *UIPainter) Bitmap(id paint.ImageID) *ui.Bitmap {
	if u == nil {
		return nil
	}
	return u.bitmaps[id]
}

// DrawCover fills box with the painter's palette paper and shows the cover
// inside it, at its aspect ratio. It reports whether a cover was available.
//
// A painter faded with SetFade draws the cover translucent and leaves the sheet
// under it to whoever drew it: a host dissolving the cover into the page behind
// it, as a book opening at a saved place does, has the page there already.
func (u *UIPainter) DrawCover(p *ui.Painter, id paint.ImageID, box ui.Rect, pad float32) bool {
	bitmap := u.Bitmap(id)
	if p == nil || bitmap == nil || box.W <= 0 || box.H <= 0 {
		return false
	}
	if u.fade >= 1 {
		p.Fill(box, uiColor(u.palette.Paper), 0)
	}
	r := coverBox(bitmap, box, pad)
	if u.fade >= 1 {
		p.Image(bitmap, r, ui.FillBox)
	} else {
		p.ImageRegion(bitmap, ui.Rect{}, r, ui.ImageOptions{Tint: ui.RGBA(0xff, 0xff, 0xff, u.fade)})
	}
	return true
}

// coverBox is where the cover is drawn inside box: the box less pad, holding the
// shape the book was published in, as tall as the space allows, centred in it and
// never stretched to fill it.
func coverBox(bitmap *ui.Bitmap, box ui.Rect, pad float32) ui.Rect {
	r := ui.Rect{X: box.X + pad, Y: box.Y + pad, W: box.W - 2*pad, H: box.H - 2*pad}
	if r.W <= 0 || r.H <= 0 {
		r = box
	}
	iw, ih := bitmap.Size()
	if iw > 0 && ih > 0 {
		scale := min(r.W/float32(iw), r.H/float32(ih))
		w, h := float32(iw)*scale, float32(ih)*scale
		r = ui.Rect{X: r.X + (r.W-w)/2, Y: r.Y + (r.H-h)/2, W: w, H: h}
	}
	return r
}

// DrawPage replays a page of the render layout into the element at box,
// through p. The painter keeps its glyph outline cache between calls.
func (u *UIPainter) DrawPage(p *ui.Painter, page *render.Page, box ui.Rect) {
	if u == nil || p == nil || page == nil || box.W <= 0 || box.H <= 0 {
		return
	}
	u.P = p
	u.dx, u.dy = box.X, box.Y
	p.Clip(box, 0, func() {
		page.Draw(u)
	})
}

// DrawTile replays one tile of a scroll layout into the element at box, cut to
// view, the visible part of the scrolling column. The tile is a display list in
// its own coordinates, so box is wherever its top edge lands this frame.
func (u *UIPainter) DrawTile(p *ui.Painter, tile *render.Tile, box ui.Rect, view ui.Rect) {
	u.drawTileZoom(p, tile, box, view, 0)
}

// DrawTileZoom draws one tile of a scrolling layout into a painter of its own:
// an offscreen canvas whose pixels are device pixels, at zoom of them a DIP.
// Everything the painter draws is taken through the same glyph atlas and the
// same tone table as the window's own column, so a tile kept as a bitmap holds
// the text the reader would have seen drawn directly — not a second rasterizer's
// idea of it. clip is in DIPs, the canvas's coordinates are zoom times that.
func (u *UIPainter) DrawTileZoom(p *ui.Painter, tile *render.Tile, box, view ui.Rect, zoom float32) {
	u.drawTileZoom(p, tile, box, view, zoom)
}

func (u *UIPainter) drawTileZoom(p *ui.Painter, tile *render.Tile, box, view ui.Rect, zoom float32) {
	if u == nil || p == nil || tile == nil || box.W <= 0 || box.H <= 0 {
		return
	}
	u.P = p
	u.dx, u.dy = box.X, box.Y
	prevZoom, prevDPR, prevCull := u.zoom, u.dpr, u.noCull
	if zoom > 0 {
		u.zoom, u.dpr, u.noCull = zoom, zoom, true
	}
	z := u.z()
	clip := ui.Rect{X: view.X * z, Y: view.Y * z, W: view.W * z, H: view.H * z}
	p.Clip(clip, 0, func() {
		tile.Draw(u)
	})
	u.zoom, u.dpr, u.noCull = prevZoom, prevDPR, prevCull
}

func (u *UIPainter) Save()    {}
func (u *UIPainter) Restore() {}

// Visible implements paint.Clipped: the part of the page or tile that can reach
// the screen, in the display list's own coordinates. DrawPage and DrawTile clip
// to the element and to the region being shown, so an op whose rectangle misses
// this draws nothing of itself; render's replay skips those before the painter
// is handed them, which is the dispatch a tile's hidden lines cost otherwise.
// A tile is shorter than the viewport and the column replays the tiles around
// it, so a frame is handed a tile or two beyond what it shows.
//
// The painter asked not to cull — a test painting a column whole, to prove the
// cull changes no pixel — reports everything visible.
func (u *UIPainter) Visible() paint.Rect {
	if u == nil || u.P == nil || u.noCull {
		return paint.Rect{X: -1e6, Y: -1e6, Width: 2e6, Height: 2e6}
	}
	v := u.P.Visible()
	return paint.Rect{X: v.X - u.dx, Y: v.Y - u.dy, Width: v.W, Height: v.H}
}

// textGrid, cleared by LAYX_TEXT_GRID=0, stops the painter from putting a run's
// baseline on the device grid: the switch that shows what the snapping is worth.
var textGrid = os.Getenv("LAYX_TEXT_GRID") != "0"

// baseline returns the y a run's baseline is drawn at, in the painter's own
// coordinates: the run's own place, moved to the nearest whole device pixel.
//
// A line of type is rasterized once per place its origin falls on the device
// grid, in quarters of a pixel, and a page's lines are not that far apart on
// any screen whose ratio is not a whole number: at 150% a line of this reader's
// body type stands 30.09 device pixels below the one before it, so the phase
// each line falls on walks 0.09 of a pixel a line and steps to the next quarter
// every two or three lines. Nothing about that is crisp or fuzzy in a single
// line — every line is drawn at the phase it has — but down the column it is a
// seam every two or three lines, where the edges of the type change together,
// and a reader calls that a line they cannot see. Snapping the baseline holds
// one phase down the whole column, the way snapping the column holds one phase
// through the whole glide (see drawScroll).
//
// It is honest about what it costs: the line moves by up to half a device
// pixel, and the leading between two lines varies by one. Both are under what a
// typesetter grid-fits away already, and neither is visible in a still page; the
// seam is.
func (u *UIPainter) baseline(y float32) float32 {
	if !textGrid || (u.P == nil && !u.warming) {
		return y
	}
	return gridBaseline(u.dy, y, u.scale())
}

// gridBaseline is y, a baseline in the coordinates of a box at dy, moved to the
// nearest whole device pixel of a screen at dpr. It works on where the line
// lands and not on where it sits in the content — the column's own offset is a
// whole device pixel already (drawScroll), so a baseline snapped this way
// travels with the column pixel for pixel, and two frames of a glide stay one
// picture moved.
func gridBaseline(dy, y, dpr float32) float32 {
	if !(dpr > 0) {
		return y
	}
	screen := float64(dy+y) * float64(dpr)
	return float32(math.Round(screen))/dpr - dy
}

// FillQuads fills highlight rectangles on one page: the quads whose Page is
// page, placed inside box, which must be the box the page was drawn in. Quads
// carry page coordinates, margins included, so they line up with DrawPage's
// output as they are. Each rectangle is cut to box, so a page drawn smaller
// than the layout it came from never paints on the desk around it.
//
// It paints over the text rather than under it, as the page's display list
// starts with its own opaque sheet: the colours are translucent veils, the way
// a reader marks a line it does not want to hide.
func (u *UIPainter) FillQuads(p *ui.Painter, box ui.Rect, page int, quads []render.SelectionQuad, colour paint.Color) {
	if u == nil || p == nil || colour.Transparent() {
		return
	}
	for i := range quads {
		q := &quads[i]
		if q.Page != page || q.Rect.Empty() {
			continue
		}
		x0 := max(box.X+q.Rect.X, box.X)
		y0 := max(box.Y+q.Rect.Y, box.Y)
		x1 := min(box.X+q.Rect.X+q.Rect.Width, box.X+box.W)
		y1 := min(box.Y+q.Rect.Y+q.Rect.Height, box.Y+box.H)
		if x1 <= x0 || y1 <= y0 {
			continue
		}
		p.Fill(ui.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}, uiColor(u.faded(colour)), 0)
	}
}

// FillScrollQuads fills highlight rectangles of a scroll layout: the quads
// carry content coordinates, so view is the visible column whose top-left
// shows content (0, scrollY), and margins are already included in Content.X.
// Each rectangle is cut to view, so a highlight never paints over the bars.
func (u *UIPainter) FillScrollQuads(p *ui.Painter, view ui.Rect, quads []render.SelectionQuad, colour paint.Color, scrollY float32) {
	if u == nil || p == nil || colour.Transparent() {
		return
	}
	for i := range quads {
		q := &quads[i]
		if q.Content.Empty() {
			continue
		}
		x0 := max(view.X+q.Content.X, view.X)
		y0 := max(view.Y+q.Content.Y-scrollY, view.Y)
		x1 := min(view.X+q.Content.X+q.Content.Width, view.X+view.W)
		y1 := min(view.Y+q.Content.Y+q.Content.Height-scrollY, view.Y+view.H)
		if x1 <= x0 || y1 <= y0 {
			continue
		}
		p.Fill(ui.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}, uiColor(u.faded(colour)), 0)
	}
}

// ClipRect is a no-op: DrawPage and DrawTile clip once to the element and the
// region being shown. A page's own clip is the same rectangle, and a tile's
// would cut the lines that overhang a tile boundary: a line belongs to the tile
// holding its top edge, so its lower part must still be drawn by that tile.
func (u *UIPainter) ClipRect(r paint.Rect) {}

// Transform is unsupported in this phase: layx's display lists for paged
// reflowable books do not use it.
func (u *UIPainter) Transform(m paint.Affine) {}

func (u *UIPainter) FillRect(r paint.Rect, c paint.Color) {
	if u.P == nil || c.Transparent() || r.Empty() || u.fade <= 0 {
		return
	}
	u.P.Fill(u.rect(r), uiColor(u.faded(c)), 0)
}

func (u *UIPainter) StrokeRect(r paint.Rect, c paint.Color, width float32) {
	if u.P == nil || c.Transparent() || r.Empty() || width <= 0 || u.fade <= 0 {
		return
	}
	u.P.Stroke(u.rect(r), uiColor(u.faded(c)), 0, width)
}

func (u *UIPainter) FillRoundRect(r paint.Rect, radius float32, c paint.Color) {
	if u.P == nil || c.Transparent() || r.Empty() || u.fade <= 0 {
		return
	}
	u.P.Fill(u.rect(r), uiColor(u.faded(c)), radius)
}

// DrawImage paints an image the paginator placed. The destination rect is
// already in page coordinates, scaled and aligned by the engine.
func (u *UIPainter) DrawImage(id paint.ImageID, dst paint.Rect, _ paint.ImageOptions) {
	if u.P == nil || dst.Empty() || u.fade <= 0 {
		return
	}
	bitmap := u.bitmaps[id]
	if bitmap == nil {
		return
	}
	opts := ui.ImageOptions{}
	if u.fade < 1 {
		opts.Tint = ui.RGBA(0xff, 0xff, 0xff, u.fade)
	}
	u.P.ImageRegion(bitmap, ui.Rect{}, u.rect(dst), opts)
}

func (u *UIPainter) DrawGlyphs(run paint.GlyphRun) {
	if u.P == nil || run.Font == nil || len(run.Glyphs) == 0 ||
		run.Color.Transparent() || run.Size <= 0 || u.fade <= 0 {
		return
	}
	// The baseline goes on the device grid before anything is drawn (see
	// baseline below), so both glyph paths put a line where the other does.
	run.Y = u.baseline(run.Y)
	gf := u.glyphFace(run.Font)
	if gf == nil {
		return
	}
	upem := float32(gf.face.Upem())
	if upem <= 0 {
		return
	}
	// A tile is shorter than the viewport and the column replays the tiles
	// around it, so runs handed here can lie outside it. Turning their
	// outlines into a path, flattening and hashing it costs the painter more
	// than drawing the glyph does, and nothing below the clip would show:
	// skip them before any of that work. run.Y is the baseline in the tile's
	// own coordinates and dy is where the tile's top edge lands this frame.
	vis := u.P.Visible()
	base := u.dy + run.Y
	if !u.noCull && (base+run.Size < vis.Y || base-run.Size > vis.Y+vis.H) {
		return
	}
	if glyphStatsOn.Load() {
		u.stats.Runs++
		u.stats.Glyphs += len(run.Glyphs)
	}
	if u.atlasWanted() {
		u.drawGlyphsAtlas(run, gf, upem)
		return
	}
	if glyphStatsOn.Load() {
		u.stats.Flattens++
	}
	scale := run.Size / upem

	var path ui.Path
	var any bool
	for i := range run.Glyphs {
		g := &run.Glyphs[i]
		out := gf.outline(g.ID)
		if out == nil || len(out.segs) == 0 {
			continue
		}
		segs := out.segs
		ox := u.dx + run.X + g.X
		oy := u.dy + run.Y + g.Y
		for j := range segs {
			s := &segs[j]
			args := s.ArgsSlice()
			switch s.Op {
			case ot.SegmentOpMoveTo:
				path.MoveTo(ox+args[0].X*scale, oy-args[0].Y*scale)
				any = true
			case ot.SegmentOpLineTo:
				path.LineTo(ox+args[0].X*scale, oy-args[0].Y*scale)
			case ot.SegmentOpQuadTo:
				path.QuadTo(
					ox+args[0].X*scale, oy-args[0].Y*scale,
					ox+args[1].X*scale, oy-args[1].Y*scale,
				)
			case ot.SegmentOpCubeTo:
				path.CubeTo(
					ox+args[0].X*scale, oy-args[0].Y*scale,
					ox+args[1].X*scale, oy-args[1].Y*scale,
					ox+args[2].X*scale, oy-args[2].Y*scale,
				)
			}
		}
		path.Close()
	}
	if any {
		z := u.z()
		if z != 1 {
			u.P.Transform(ui.Scaling(z, z), 0, 0, func() {
				u.P.FillPath(&path, uiColor(u.faded(run.Color)))
				if run.EmulateBold && !fakeBoldOff {
					u.P.StrokePath(&path, max(0.35, run.Size*0.028), uiColor(u.faded(run.Color)))
				}
			})
		} else {
			u.P.FillPath(&path, uiColor(u.faded(run.Color)))
			if run.EmulateBold && !fakeBoldOff {
				// A path is not corrected or grown the way the atlas is, but a
				// weight the book asks for and the face does not have is still
				// asked for: the outline is stroked as synthetic bold.
				u.P.StrokePath(&path, max(0.35, run.Size*0.028), uiColor(u.faded(run.Color)))
			}
		}
	}
}

// FlushGlyphs uploads the glyphs the atlas rasterized this frame. Call it on
// the UI thread after the pages are drawn: the engine uploads a layer whose
// version changed, so a layer written after that shows its glyphs from the
// next frame. It does nothing while the atlas is off or unchanged.
func (u *UIPainter) FlushGlyphs() {
	if u == nil || u.glyphs == nil {
		return
	}
	u.glyphs.flush()
}

// GlyphStats returns what the painter did with glyphs since the last reset,
// counted only when LAYX_GLYPH_STATS is set: LAYX_GLYPH_STATS=1 go run . It is
// the numbers that say whether the atlas pays for itself.
func (u *UIPainter) GlyphStats() GlyphStats {
	if u == nil {
		return GlyphStats{}
	}
	s := u.stats
	if a := u.glyphs; a != nil {
		s.Slots, s.Layers, s.Bytes = len(a.index), len(a.layers), a.bytes
	}
	return s
}

// ResetGlyphStats starts the counters of GlyphStats over, as a test does to
// measure one frame.
func (u *UIPainter) ResetGlyphStats() {
	if u == nil {
		return
	}
	u.stats = GlyphStats{}
	if u.glyphs != nil {
		u.glyphs.stats = &u.stats
	}
}

// glyphFace parses the face a run names, once per font.
func (u *UIPainter) glyphFace(f *paint.Font) *glyphFace {
	if u == nil {
		return nil
	}
	return u.facesOf().of(f)
}

// facesOf returns the face cache, made on first use.
func (u *UIPainter) facesOf() *faceCache {
	if u.faces == nil {
		u.faces = &faceCache{}
	}
	return u.faces
}

// tonesOf returns the ink corrections, made on first use.
func (u *UIPainter) tonesOf() *toneCache {
	if u.tones == nil {
		u.tones = &toneCache{}
	}
	return u.tones
}

// ShareGlyphCaches makes this painter draw from another's faces, ink
// corrections and rasterized glyphs. The worker's painter shares the frame's,
// so a glyph the frame is about to draw is already rasterized when the worker
// walked that part of the column first, and a glyph the two of them need is
// parsed and corrected once.
func (u *UIPainter) ShareGlyphCaches(with *UIPainter) {
	if u == nil || with == nil || u == with {
		return
	}
	u.faces = with.facesOf()
	u.tones = with.tonesOf()
	u.covers = with.coversOf()
}

func (gf *glyphFace) outline(gid uint32) *glyphOutline {
	gf.mu.Lock()
	defer gf.mu.Unlock()
	if out, ok := gf.outlines[gid]; ok {
		return out
	}
	data, _ := gf.face.GlyphDataOutline(font.GID(gid))
	out := &glyphOutline{segs: data.Segments}
	out.ink()
	gf.outlines[gid] = out
	return out
}

func (u *UIPainter) rect(r paint.Rect) ui.Rect {
	z := u.z()
	return ui.Rect{X: (u.dx + r.X) * z, Y: (u.dy + r.Y) * z, W: r.Width * z, H: r.Height * z}
}

func uiColor(c paint.Color) ui.Color {
	if c.A == 0xff {
		return ui.RGB(c.R, c.G, c.B)
	}
	return ui.RGBA(c.R, c.G, c.B, float32(c.A)/255)
}
