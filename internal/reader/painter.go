package reader

import (
	"github.com/egoist/mygo/ui"
	"github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
	"github.com/laix/layx/render"
	"github.com/laix/layx/render/paint"
)

// UIPainter implements render's paint.Painter on top of MyGo's native
// ui.Painter. Glyphs are filled from the font's own outlines, as the wasm
// example does, but the path is a ui.Path rather than an SVG string.
//
// The display list always paints in page coordinates starting at (0,0); Draw
// offsets them to the element's rect and clips to it once, so the per-op
// Save/ClipRect/Restore of a page needs no state here.
type UIPainter struct {
	P *ui.Painter

	// faces are the go-text faces of the fonts the runs name, parsed on first
	// use: a page that mixes the serif and sans faces draws from both.
	faces map[*paint.Font]*glyphFace
	// bitmaps are the images the paginator placed, decoded once per id.
	bitmaps map[paint.ImageID]*ui.Bitmap
	dx, dy  float32
	// noCull turns off skipping the runs outside the clip, so a test can
	// paint them and prove the skip changes no pixel.
	noCull bool
}

// glyphFace is one parsed face and the outlines it has handed out so far.
type glyphFace struct {
	face     *font.Face
	outlines map[uint32][]font.Segment
}

// NewUIPainter prepares a painter for one element. The outline and image
// caches live as long as the painter, so keep it for the whole window and
// reuse it. Run the SetImage calls on the UI thread.
func NewUIPainter(p *ui.Painter, fonts *Fonts) *UIPainter {
	return &UIPainter{
		P:       p,
		faces:   make(map[*paint.Font]*glyphFace),
		bitmaps: make(map[paint.ImageID]*ui.Bitmap),
	}
}

// SetImage decodes data once and keeps the bitmap for id. It reports whether
// the image decoded; formats beyond PNG, JPEG, GIF, WebP and BMP (SVG, say)
// are skipped. Call it on the UI thread, after a build produced the bytes.
func (u *UIPainter) SetImage(id paint.ImageID, data []byte) bool {
	if u == nil || len(data) == 0 {
		return false
	}
	if _, ok := u.bitmaps[id]; ok {
		return true
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

// DrawCover fills box with the page colour and shows the cover inside it, at
// its aspect ratio. It reports whether a cover was available.
func (u *UIPainter) DrawCover(p *ui.Painter, id paint.ImageID, box ui.Rect, pad float32) bool {
	bitmap := u.Bitmap(id)
	if p == nil || bitmap == nil || box.W <= 0 || box.H <= 0 {
		return false
	}
	p.Fill(box, uiColor(Current().Paper), 0)
	r := ui.Rect{X: box.X + pad, Y: box.Y + pad, W: box.W - 2*pad, H: box.H - 2*pad}
	if r.W <= 0 || r.H <= 0 {
		r = box
	}
	// The cover keeps the shape the book was published in: it is centred in
	// the space, as tall as the page allows, and never stretched to fill it.
	iw, ih := bitmap.Size()
	if iw > 0 && ih > 0 {
		scale := min(r.W/float32(iw), r.H/float32(ih))
		w, h := float32(iw)*scale, float32(ih)*scale
		r = ui.Rect{X: r.X + (r.W-w)/2, Y: r.Y + (r.H-h)/2, W: w, H: h}
	}
	p.Image(bitmap, r, ui.FillBox)
	return true
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
	if u == nil || p == nil || tile == nil || box.W <= 0 || box.H <= 0 {
		return
	}
	u.P = p
	u.dx, u.dy = box.X, box.Y
	p.Clip(view, 0, func() {
		tile.Draw(u)
	})
}

func (u *UIPainter) Save()    {}
func (u *UIPainter) Restore() {}

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
		p.Fill(ui.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}, uiColor(colour), 0)
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
		p.Fill(ui.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}, uiColor(colour), 0)
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
	if u.P == nil || c.Transparent() || r.Empty() {
		return
	}
	u.P.Fill(u.rect(r), uiColor(c), 0)
}

func (u *UIPainter) StrokeRect(r paint.Rect, c paint.Color, width float32) {
	if u.P == nil || c.Transparent() || r.Empty() || width <= 0 {
		return
	}
	u.P.Stroke(u.rect(r), uiColor(c), 0, width)
}

func (u *UIPainter) FillRoundRect(r paint.Rect, radius float32, c paint.Color) {
	if u.P == nil || c.Transparent() || r.Empty() {
		return
	}
	u.P.Fill(u.rect(r), uiColor(c), radius)
}

// DrawImage paints an image the paginator placed. The destination rect is
// already in page coordinates, scaled and aligned by the engine.
func (u *UIPainter) DrawImage(id paint.ImageID, dst paint.Rect, _ paint.ImageOptions) {
	if u.P == nil || dst.Empty() {
		return
	}
	bitmap := u.bitmaps[id]
	if bitmap == nil {
		return
	}
	u.P.ImageRegion(bitmap, ui.Rect{}, u.rect(dst), ui.ImageOptions{})
}

func (u *UIPainter) DrawGlyphs(run paint.GlyphRun) {
	if u.P == nil || run.Font == nil || len(run.Glyphs) == 0 ||
		run.Color.Transparent() || run.Size <= 0 {
		return
	}
	gf := u.glyphFace(run.Font)
	if gf == nil {
		return
	}
	upem := float32(gf.face.Upem())
	if upem <= 0 {
		return
	}
	// A tile is one viewport tall and the column replays the tiles around the
	// viewport, so most of the runs handed here lie outside it. Turning their
	// outlines into a path, flattening and hashing it costs the painter more
	// than drawing the glyph does, and nothing below the clip would show:
	// skip them before any of that work. run.Y is the baseline in the tile's
	// own coordinates and dy is where the tile's top edge lands this frame.
	vis := u.P.Visible()
	baseline := u.dy + run.Y
	if !u.noCull && (baseline+run.Size < vis.Y || baseline-run.Size > vis.Y+vis.H) {
		return
	}
	scale := run.Size / upem

	var path ui.Path
	var any bool
	for i := range run.Glyphs {
		g := &run.Glyphs[i]
		segs := gf.outline(g.ID)
		if len(segs) == 0 {
			continue
		}
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
		u.P.FillPath(&path, uiColor(run.Color))
	}
}

// glyphFace parses the face a run names, once per font.
func (u *UIPainter) glyphFace(f *paint.Font) *glyphFace {
	if gf, ok := u.faces[f]; ok {
		return gf
	}
	face := faceOf(f.Data, f.Index)
	if face == nil {
		return nil
	}
	gf := &glyphFace{face: face, outlines: make(map[uint32][]font.Segment)}
	u.faces[f] = gf
	return gf
}

func (gf *glyphFace) outline(gid uint32) []font.Segment {
	if segs, ok := gf.outlines[gid]; ok {
		return segs
	}
	out, _ := gf.face.GlyphDataOutline(font.GID(gid))
	gf.outlines[gid] = out.Segments
	return out.Segments
}

func (u *UIPainter) rect(r paint.Rect) ui.Rect {
	return ui.Rect{X: u.dx + r.X, Y: u.dy + r.Y, W: r.Width, H: r.Height}
}

func uiColor(c paint.Color) ui.Color {
	if c.A == 0xff {
		return ui.RGB(c.R, c.G, c.B)
	}
	return ui.RGBA(c.R, c.G, c.B, float32(c.A)/255)
}
