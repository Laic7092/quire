package raster

import (
	"bytes"
	"unsafe"

	"github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
	"github.com/laix/layx/render/paint"
)

// faceKey identifies a font file plus one face inside it. layx reuses the same
// *paint.Font for a face across runs, but a host may not, so the key is the
// font bytes themselves (address and length) rather than the pointer to the
// paint.Font.
type faceKey struct {
	data  uintptr
	size  int
	index int
}

type faceEntry struct {
	face *font.Face
	// data keeps the font file alive for as long as the parsed face is cached:
	// go-text faces reference slices of it.
	data []byte
}

type outlineKey struct {
	face faceKey
	gid  uint32
}

func keyOf(f *paint.Font) (faceKey, bool) {
	if f == nil || len(f.Data) == 0 {
		return faceKey{}, false
	}
	return faceKey{
		data:  uintptr(unsafe.Pointer(unsafe.SliceData(f.Data))),
		size:  len(f.Data),
		index: f.Index,
	}, true
}

// face parses and caches the face described by f. A parse failure is cached
// as a nil face so a broken font is not reparsed on every run.
func (r *Rasterizer) face(f *paint.Font) (*font.Face, bool) {
	key, ok := keyOf(f)
	if !ok {
		return nil, false
	}
	if e, ok := r.faces[key]; ok {
		return e.face, e.face != nil
	}

	parsed, err := font.ParseTTC(bytes.NewReader(f.Data))
	if err != nil || len(parsed) == 0 {
		if r.faces == nil {
			r.faces = make(map[faceKey]faceEntry)
		}
		r.faces[key] = faceEntry{}
		return nil, false
	}
	index := f.Index
	if index < 0 || index >= len(parsed) {
		index = 0
	}
	if r.faces == nil {
		r.faces = make(map[faceKey]faceEntry)
	}
	r.faces[key] = faceEntry{face: parsed[index], data: f.Data}
	return parsed[index], true
}

// outlineFor returns the cached outline of gid in font units.
func (r *Rasterizer) outlineFor(key outlineKey, face *font.Face, gid uint32) []font.Segment {
	if segs, ok := r.outlines[key]; ok {
		return segs
	}
	out, ok := face.GlyphDataOutline(font.GID(gid))
	var segs []font.Segment
	if ok && len(out.Segments) > 0 {
		segs = make([]font.Segment, len(out.Segments))
		copy(segs, out.Segments)
	}
	if r.outlines == nil {
		r.outlines = make(map[outlineKey][]font.Segment)
	}
	r.outlines[key] = segs
	return segs
}

// DrawGlyphs implements paint.Painter.
//
// Every glyph is rasterized from the outlines in run.Font. That keeps the
// package honest - layx's glyph IDs are the only input, the string is never
// re-shaped - and means a run with a rotated or scaled transform is exact
// rather than an approximation from an unscaled bitmap.
func (r *Rasterizer) DrawGlyphs(run paint.GlyphRun) {
	if run.Font == nil || len(run.Glyphs) == 0 || run.Size <= 0 || run.Color.A == 0 {
		return
	}
	face, ok := r.face(run.Font)
	if !ok {
		return
	}
	upem := float32(face.Upem())
	if upem <= 0 {
		return
	}
	key, _ := keyOf(run.Font)
	scale := run.Size / upem
	// The ink's correction, once for the run: it depends on the colour and the
	// band of sizes, not on the glyph.
	var tone *Tone
	if r.ToneFor != nil {
		tone = r.ToneFor(run)
	}

	s := &r.path
	for i := range run.Glyphs {
		g := &run.Glyphs[i]
		segs := r.outlineFor(outlineKey{face: key, gid: g.ID}, face, g.ID)
		if len(segs) == 0 {
			continue
		}
		s.reset()
		b := newBuilder(s, func(x, y float32) point {
			return apply(r.state.m, point{
				X: run.X + g.X + x*scale,
				Y: run.Y + g.Y - y*scale, // font units grow up, device grows down
			})
		})
		addOutline(b, segs)
		if len(s.starts) == 0 {
			continue
		}
		r.fillShape(s, run.Color, tone)

		if run.EmulateBold {
			// The core only flags that the matched face is not bold; faking it
			// here keeps layout untouched. A second pass shifted along the
			// text's own x axis is cheap and rotates with the glyphs.
			dx := run.Size / 24
			if dx > 1.5 {
				dx = 1.5
			}
			ddx := r.state.m.A * dx
			ddy := r.state.m.B * dx
			if ddx == 0 && ddy == 0 {
				continue
			}
			for j := range s.pts {
				s.pts[j].X += ddx
				s.pts[j].Y += ddy
			}
			r.fillShape(s, run.Color, tone)
		}
	}
}

// addOutline appends a go-text outline to b, flattening its quadratic and
// cubic segments after the transform.
func addOutline(b *builder, segs []font.Segment) {
	for i := range segs {
		seg := &segs[i]
		args := seg.ArgsSlice()
		switch seg.Op {
		case ot.SegmentOpMoveTo:
			b.moveTo(args[0].X, args[0].Y)
		case ot.SegmentOpLineTo:
			b.lineTo(args[0].X, args[0].Y)
		case ot.SegmentOpQuadTo:
			b.quadTo(args[0].X, args[0].Y, args[1].X, args[1].Y)
		case ot.SegmentOpCubeTo:
			b.cubeTo(args[0].X, args[0].Y, args[1].X, args[1].Y, args[2].X, args[2].Y)
		}
	}
}
