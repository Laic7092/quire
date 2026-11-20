package render

import (
	"github.com/go-text/typesetting/font"
	"github.com/laix/layx/render/paint"
)

// faceFor finds the parsed face behind a paint.Font handle. A decoration uses
// it for underline/strikeout metrics; a missing face falls back to em-relative
// values.
func (w *textWorker) faceFor(ref *paint.Font) *font.Face {
	if ref == nil {
		return nil
	}
	for i, r := range w.refs {
		if r == ref {
			return w.faces[i]
		}
	}
	return nil
}

// decorationMetrics returns the distance from the baseline down to the top of
// the decoration, plus its thickness, in CSS pixels. Positive "down" means
// below the baseline.
//
// Font metrics win when the face provides a usable thickness:
// underlinePosition/underlineThickness for underlines and
// strikethroughPosition/strikethroughThickness for line-through. Missing
// metrics fall back to size/10 below and size/30 above the baseline with
// size/16 thickness.
func decorationMetrics(face *font.Face, size float32, kind TextDecoration) (down, thickness float32) {
	if size <= 0 {
		size = 18
	}
	if face != nil && face.Upem() > 0 {
		scale := size / float32(face.Upem())
		switch kind {
		case TextDecorationUnderline:
			if thick := face.LineMetric(font.UnderlineThickness); thick > 0 {
				return -face.LineMetric(font.UnderlinePosition) * scale, thick * scale
			}
		case TextDecorationLineThrough:
			if thick := face.LineMetric(font.StrikethroughThickness); thick > 0 {
				return -face.LineMetric(font.StrikethroughPosition) * scale, thick * scale
			}
		}
	}
	switch kind {
	case TextDecorationLineThrough:
		return size * 0.3, size / 16
	default:
		return size * 0.1, size / 16
	}
}

// lineDecorations builds one fill rectangle per decorated run. Each run uses
// its own size and face metrics, so a line mixing 12px and 24px text gets two
// underline positions rather than one averaged line. runs must already carry
// absolute page/tile coordinates.
func (l *Layout) lineDecorations(w *textWorker, it *flowItem, chapter int, runs []paint.GlyphRun) []drawOp {
	if !it.lineHasDecoration() {
		return nil
	}
	shaped, _ := l.shapeLine(w, it, chapter)
	if len(shaped) > len(runs) {
		return nil
	}
	var out []drawOp
	// runs may contain one extra synthetic hyphen run with no decoration.
	for i := 0; i < len(shaped); i++ {
		st := shaped[i].style
		if st.decoration == TextDecorationNone {
			continue
		}
		run := &runs[i]
		if len(run.Glyphs) == 0 {
			continue
		}
		x1 := run.X
		for gi := range run.Glyphs {
			g := &run.Glyphs[gi]
			if end := run.X + g.X + g.Advance; end > x1 {
				x1 = end
			}
		}
		if x1 <= run.X {
			continue
		}
		down, thickness := decorationMetrics(w.faceFor(run.Font), run.Size, st.decoration)
		if thickness <= 0 {
			continue
		}
		color := st.decorationColor
		if color.Transparent() {
			color = run.Color
		}
		if color.Transparent() {
			continue
		}
		out = append(out, drawOp{kind: opFill, color: color, rect: paint.Rect{
			X:      run.X,
			Y:      run.Y + down,
			Width:  x1 - run.X,
			Height: thickness,
		}})
	}
	return out
}

// lineHasDecoration is a cheap pre-check so the common undecorated line does
// not perform a second shaped-line lookup.
func (it *flowItem) lineHasDecoration() bool {
	if it.para == nil {
		return false
	}
	for si := range it.para.segs {
		seg := &it.para.segs[si]
		if int(seg.end) <= int(it.start) || int(seg.start) >= int(it.end) {
			continue
		}
		if seg.style.TextDecoration != TextDecorationNone {
			return true
		}
	}
	return false
}
