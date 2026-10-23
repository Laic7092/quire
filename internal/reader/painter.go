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
	P    *ui.Painter
	Face *font.Face

	outlines map[uint32][]font.Segment
	dx, dy   float32
}

// NewUIPainter prepares a painter for one element. The outline cache lives as
// long as the painter, so keep it for the whole window and reuse it.
func NewUIPainter(p *ui.Painter, fonts *Fonts) *UIPainter {
	up := &UIPainter{P: p, outlines: make(map[uint32][]font.Segment)}
	if fonts != nil {
		up.Face = fonts.Face
	}
	return up
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

func (u *UIPainter) Save()    {}
func (u *UIPainter) Restore() {}

// ClipRect is already provided by DrawPage's outer clip; pages only clip to
// their own viewport.
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

// DrawImage is out of scope: the EPUB importer never emits images.
func (u *UIPainter) DrawImage(id paint.ImageID, dst paint.Rect, opts paint.ImageOptions) {}

func (u *UIPainter) DrawGlyphs(run paint.GlyphRun) {
	if u.P == nil || u.Face == nil || run.Font == nil || len(run.Glyphs) == 0 ||
		run.Color.Transparent() || run.Size <= 0 {
		return
	}
	upem := float32(u.Face.Upem())
	if upem <= 0 {
		return
	}
	scale := run.Size / upem

	var path ui.Path
	var any bool
	for i := range run.Glyphs {
		g := &run.Glyphs[i]
		segs := u.outline(g.ID)
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

func (u *UIPainter) outline(gid uint32) []font.Segment {
	if segs, ok := u.outlines[gid]; ok {
		return segs
	}
	out, _ := u.Face.GlyphDataOutline(font.GID(gid))
	u.outlines[gid] = out.Segments
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
