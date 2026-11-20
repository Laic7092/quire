package store

import (
	"math"
	"time"

	"github.com/laic7092/layx/render"
)

// PageFor maps a persisted Position back onto a page of l.
//
// It tries, in order: the exact node (PageOf with the recorded chapter, node and
// rune offset), the host anchor (Locate then PageOf), the recorded progress
// (proportional page) and finally the first page of the recorded chapter. It
// reports which path produced the page, so the reader can show the adapter cost
// in its status bar.
//
// A position recorded against a different revision of the book is not
// detectable: PageOf succeeds whenever the node number exists in the new
// chapter, even if that node now holds different text. See
// docs/explore/friction-storage.md.
//
// how is one of "node", "anchor", "progress", "chapter", or "none" when the
// layout has no pages at all (nil, scroll mode, empty document). page is -1 in
// that case.
func PageFor(l *render.Layout, p Position) (page int, how string) {
	if l == nil {
		return -1, "none"
	}
	pages := l.PageCount()
	if pages <= 0 {
		return -1, "none"
	}

	// Node 0 is a valid render.NodeID (the chapter root), so a zero Node cannot
	// be told apart from "not recorded"; treat it as unset. Falling through
	// costs at most the few lines between a chapter root and the recorded
	// offset.
	if p.Node > 0 && p.Node <= math.MaxInt32 {
		loc := render.Location{
			Chapter: p.Chapter,
			Node:    render.NodeID(p.Node),
			Offset:  p.Offset,
		}
		if n, ok := l.PageOf(loc); ok {
			return n, "node"
		}
	}

	if p.Anchor != 0 {
		if loc, ok := l.Locate(render.AnchorID(p.Anchor)); ok {
			if n, ok := l.PageOf(loc); ok {
				return n, "anchor"
			}
		}
	}

	if q := float64(p.Progress); q > 0 && !math.IsNaN(q) && !math.IsInf(q, 0) {
		if q > 1 {
			q = 1
		}
		n := int(q * float64(pages))
		if n >= pages {
			n = pages - 1
		}
		if n < 0 {
			n = 0
		}
		return n, "progress"
	}

	// Chapter start. PageRange rejects an out-of-range chapter, so clamp
	// through ChapterPages, which always knows every chapter.
	if first, _, ok := l.PageRange(p.Chapter); ok {
		return first, "chapter"
	}
	if starts := l.ChapterPages(); len(starts) > 0 {
		i := p.Chapter
		if i < 0 {
			i = 0
		}
		if i >= len(starts) {
			i = len(starts) - 1
		}
		return starts[i], "chapter"
	}
	return -1, "none"
}

// PositionFor records page as a Position, the inverse of PageFor.
//
// A page slot is a viewport tall, so the probe is taken a little way into the
// slot of page (see pageSlotHeight). If the probe resolves to another page the
// page is treated as unreachable (a blank page) and the call reports false; the
// caller should keep the previous position instead of recording a wrong one.
func PositionFor(l *render.Layout, page int) (Position, bool) {
	if l == nil || page < 0 || page >= l.PageCount() {
		return Position{}, false
	}
	h, ok := pageSlotHeight(l)
	if !ok {
		return Position{}, false
	}
	eps := h / 512 // about one pixel for a 500px page slot
	for _, y := range []float32{float32(page)*h + eps, float32(page)*h + 1} {
		loc, ok := l.LocationAt(y)
		if !ok {
			continue
		}
		if n, ok := l.PageOf(loc); !ok || n != page {
			continue
		}
		p, ok := positionOf(l, loc)
		if !ok {
			continue
		}
		// The last page is the end of the book even though Progress names its
		// top and is therefore below 1.
		p.Finished = page >= l.PageCount()-1
		return p, true
	}
	return Position{}, false
}

// Locate maps a persisted Position to a Location of l in either mode: pages in
// a paged layout, a content offset in a scroll one. It tries the same paths as
// PageFor, in the same order: the exact node, the host anchor, the recorded
// progress and finally the chapter start. how names the path that produced the
// location ("node", "anchor", "progress", "chapter"), or is "none" when the
// layout cannot resolve any of them.
func Locate(l *render.Layout, p Position) (loc render.Location, how string) {
	if l == nil {
		return render.Location{}, "none"
	}

	// Node 0 is a valid render.NodeID (the chapter root), so a zero Node cannot
	// be told apart from "not recorded"; treat it as unset.
	if p.Node > 0 && p.Node <= math.MaxInt32 {
		loc := render.Location{
			Chapter: p.Chapter,
			Node:    render.NodeID(p.Node),
			Offset:  p.Offset,
		}
		if _, ok := l.OffsetOf(loc); ok {
			return loc, "node"
		}
	}

	if p.Anchor != 0 {
		if loc, ok := l.Locate(render.AnchorID(p.Anchor)); ok {
			return loc, "anchor"
		}
	}

	if q := float64(p.Progress); q > 0 && !math.IsNaN(q) && !math.IsInf(q, 0) {
		q = min(q, 1)
		if height := l.ContentHeight(); height > 0 {
			// Scroll mode: progress is a fraction of the whole content flow.
			if loc, ok := l.LocationAt(float32(q * float64(height))); ok {
				return loc, "progress"
			}
		} else if pages := l.PageCount(); pages > 0 {
			n := min(int(q*float64(pages)), pages-1)
			if loc, ok := l.LocationOfPage(max(n, 0)); ok {
				return loc, "progress"
			}
		}
	}

	// Chapter start, through the content coordinates both modes share.
	if top, ok := l.ChapterContentTop(p.Chapter); ok {
		if loc, ok := l.LocationAt(top); ok {
			return loc, "chapter"
		}
	}
	if starts := l.ChapterPages(); len(starts) > 0 {
		i := p.Chapter
		if i < 0 {
			i = 0
		}
		if i >= len(starts) {
			i = len(starts) - 1
		}
		if loc, ok := l.LocationOfPage(starts[i]); ok {
			return loc, "chapter"
		}
	}
	return render.Location{}, "none"
}

// PositionAt records loc as a Position: the node and offset for the exact
// path, the anchor for the reflow-stable one and the content progress for the
// last resort. It is the inverse of Locate and works in both modes.
func PositionAt(l *render.Layout, loc render.Location) (Position, bool) {
	if l == nil {
		return Position{}, false
	}
	// An anchor names the node; a location that carries one is resolved through
	// it, so Chapter and Node are the ones the layout knows.
	if loc.Anchor != render.NoAnchor {
		if resolved, ok := l.Locate(loc.Anchor); ok {
			resolved.Offset = loc.Offset
			loc = resolved
		}
	}
	if _, ok := l.OffsetOf(loc); !ok {
		return Position{}, false
	}
	return positionOf(l, loc)
}

// positionOf fills a Position from a location the layout has already resolved.
func positionOf(l *render.Layout, loc render.Location) (Position, bool) {
	p := Position{
		Chapter: loc.Chapter,
		Anchor:  uint32(loc.Anchor),
		Offset:  loc.Offset,
		Updated: time.Now(),
	}
	if loc.Node >= 0 {
		p.Node = uint32(loc.Node)
	}
	if pr, ok := l.Progress(loc); ok {
		p.Progress = pr
	}
	return p, true
}

// pageSlotHeight is the height of one page slot: the viewport height minus the
// page's top and bottom margins, which is exactly what render reports as
// PageContentHeight. It used to be recovered from ChapterContentTop, which
// silently failed for every layout whose only chapter starts on page 0 — a
// single-chapter book, which is what a short TXT is — so its position was never
// recorded.
func pageSlotHeight(l *render.Layout) (float32, bool) {
	if l == nil {
		return 0, false
	}
	h := l.PageContentHeight()
	if h <= 0 {
		return 0, false
	}
	return h, true
}
