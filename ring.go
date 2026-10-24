package main

import "github.com/egoist/mygo/ui"

// ringPath is a circle that starts at twelve o'clock and runs clockwise, so
// Trim(0, p) fills a progress ring from the top the way the system's own do.
func ringPath(cx, cy, r float32) *ui.Path {
	const k = 0.5522847498
	var p ui.Path
	p.MoveTo(cx, cy-r)
	p.CubeTo(cx+r*k, cy-r, cx+r, cy-r*k, cx+r, cy)
	p.CubeTo(cx+r, cy+r*k, cx+r*k, cy+r, cx, cy+r)
	p.CubeTo(cx-r*k, cy+r, cx-r, cy+r*k, cx-r, cy)
	p.CubeTo(cx-r, cy-r*k, cx-r*k, cy-r, cx, cy-r)
	return p.Close()
}

// drawRing paints a progress ring in r: the whole circle in track, and the part
// done in ink, both width DIPs wide with round ends.
func drawRing(p *ui.Painter, r ui.Rect, progress float32, ink, track ui.Color, width float32) {
	cx, cy := r.X+r.W/2, r.Y+r.H/2
	radius := min(r.W, r.H)/2 - width/2
	if radius <= 0 {
		return
	}
	p.StrokePath(ringPath(cx, cy, radius), width, track)
	if progress <= 0 {
		return
	}
	p.StrokePath(ringPath(cx, cy, radius).Trim(0, min(progress, 1)), width, ink)
}
