package render

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/laix/layx/render/paint"
)

// Tile is one fixed-height slice of the continuous scroll flow.
//
// A Tile is a DisplayList plus a content-coordinate range, not a bitmap: the
// render package has no rasterizer. A host draws Tile.Draw into its own
// surface and may cache the resulting pixels per tile. Tile coordinates are
// local to the tile: (0,0) is the tile's top-left corner.
//
// Tiles are immutable once built and may be drawn concurrently as long as each
// goroutine uses its own Painter.
type Tile struct {
	layout *Layout
	index  int
	top    float32
	height float32
	ops    []drawOp
	links  []Link
	// incomplete marks a display list that a recovered panic or a failed
	// parallel paint step cut short. Tile never caches such a tile; building it
	// again is cheap compared to serving a half-drawn frame from the cache.
	incomplete bool
}

// Index returns the tile number. Tile i covers content y
// [i*TileHeight, (i+1)*TileHeight).
func (t *Tile) Index() int { return t.index }

// Top returns the content y of the tile's top edge.
func (t *Tile) Top() float32 { return t.top }

// Height returns the tile's height. The last tile is also full height; its
// content may end before its bottom edge.
func (t *Tile) Height() float32 { return t.height }

// Size returns the tile surface size. Width is always the layout viewport
// width; height is the configured TileHeight.
func (t *Tile) Size() paint.Size {
	return paint.Size{Width: t.layout.opts.Viewport.Width, Height: t.height}
}

// Draw replays the tile display list into painter.
func (t *Tile) Draw(painter paint.Painter) {
	if painter == nil {
		return
	}
	visible, cull := visibleRect(painter)
	painter.Save()
	painter.ClipRect(paint.Rect{X: 0, Y: 0, Width: t.Size().Width, Height: t.Size().Height})
	for i := range t.ops {
		op := &t.ops[i]
		if cull && !replayOp(op, visible) {
			continue
		}
		switch op.kind {
		case opFill:
			painter.FillRect(op.rect, op.color)
		case opRoundFill:
			painter.FillRoundRect(op.rect, op.radius, op.color)
		case opImage:
			painter.DrawImage(op.image, op.rect, paint.ImageOptions{})
		case opGlyphs:
			painter.DrawGlyphs(op.run)
		}
	}
	painter.Restore()
}

// Links returns the host links on this tile.
func (t *Tile) Links() []Link {
	out := make([]Link, len(t.links))
	copy(out, t.links)
	return out
}

// initScroll computes the chapter offsets and tile geometry. It is a no-op for
// paged layouts, whose Page maps stay authoritative.
//
// Content coordinates follow the task contract:
//
//   - y=0 is the top of the whole book's body, not the top of each chapter.
//   - Chapters are stacked with no page margins in between; each chapter keeps
//     its own root block margins.
//   - Margins.Top/Bottom are page viewport insets and are not part of content
//     coordinates. Margins.Left/Right still inset the tile display lists.
//
// For a ranged layout only the included chapters contribute height, so content
// y starts at 0 at the first included chapter.
//
// initScroll enforces Limits.MaxTiles before allocating the tile cache: the
// tile count is content height divided by TileHeight, and a tiny TileHeight can
// otherwise turn a short book into millions of tile boxes.
func (l *Layout) initScroll() error {
	if l.opts.Mode != ModeScroll {
		return nil
	}
	l.chapterTop = make([]float32, len(l.chapters))
	var y float32
	for ci := l.firstChapter; ci < l.lastChapter; ci++ {
		l.chapterTop[ci] = y
		y += l.chapters[ci].height
	}
	l.contentHeight = y
	height := l.opts.TileHeight
	if height <= 0 {
		height = l.opts.Viewport.Height
	}
	l.tileHeight = height
	if height > 0 && y > 0 {
		l.tileCount = int(math.Ceil(float64(y) / float64(height)))
	}
	if maxTiles := l.engine.limits.MaxTiles; maxTiles > 0 && l.tileCount > maxTiles {
		return docErrorf(ErrLimitExceeded, "tiles %d > limit %d", l.tileCount, maxTiles)
	}
	l.tileCache = make(map[int]*Tile)
	return nil
}

// ContentHeight returns the scroll flow's total height in content pixels, or 0
// for a paged layout.
func (l *Layout) ContentHeight() float32 {
	if l.opts.Mode != ModeScroll {
		return 0
	}
	return l.contentHeight
}

// TileCount returns the number of fixed-height tiles, or 0 for a paged layout.
func (l *Layout) TileCount() int {
	if l.opts.Mode != ModeScroll {
		return 0
	}
	return l.tileCount
}

// TileHeight returns the tile height this layout was built with. A zero
// TileHeight option means "use the viewport height", and that resolved value
// is what this returns; hosts need it to map a scroll offset to a tile index
// without remembering the option they passed in.
func (l *Layout) TileHeight() float32 {
	if l.opts.Mode != ModeScroll {
		return 0
	}
	return l.tileHeight
}

// Tile returns tile i, building its DisplayList on first access. Tiles are
// cached up to Options.TileCache with an LRU policy. It returns nil in paged
// mode and for out-of-range indices. Like a page, a tile shapes its lines on the
// engine's budget; Prefetch and Tiles warm a window ahead of time.
func (l *Layout) Tile(i int) *Tile {
	if l.opts.Mode != ModeScroll || i < 0 || i >= l.tileCount {
		return nil
	}
	limit := l.engine.tileCache
	if limit > 0 {
		l.tileMu.Lock()
		if t, ok := l.tileCache[i]; ok {
			l.tileOrder = touchInt(l.tileOrder, i)
			l.tileMu.Unlock()
			return t
		}
		l.tileMu.Unlock()
	}
	t := l.buildTile(i)
	if limit <= 0 || t.incomplete {
		// A tile cut short by a recovered internal error must not enter the
		// cache: the host would keep drawing a partial frame for every later
		// request. It is returned once, and the next Tile(i) retries the build.
		return t
	}
	l.tileMu.Lock()
	if existing, ok := l.tileCache[i]; ok {
		l.tileMu.Unlock()
		return existing
	}
	for len(l.tileCache) >= limit && len(l.tileOrder) > 0 {
		old := l.tileOrder[0]
		l.tileOrder = l.tileOrder[1:]
		delete(l.tileCache, old)
	}
	l.tileCache[i] = t
	l.tileOrder = append(l.tileOrder, i)
	l.tileMu.Unlock()
	return t
}

// Tiles builds tiles [lo, hi). It is the explicit prefetch API: there is no
// hidden goroutine, so the host decides whether to call it on its own worker.
// The range is clamped to [0, TileCount].
//
// Tiles are independent, so the range is built on the engine's whole budget and
// a scroll prefetch does not leave the cores idle. Prefetch is the same thing
// with a context and the cache-size clamp.
func (l *Layout) Tiles(lo, hi int) []*Tile {
	if l.opts.Mode != ModeScroll {
		return nil
	}
	lo = clampInt(lo, 0, l.tileCount)
	hi = clampInt(hi, 0, l.tileCount)
	if hi <= lo {
		return nil
	}
	out := make([]*Tile, hi-lo)
	// A window that is nearly all cached is one tile of real work, and helpers
	// would only add goroutines that have nothing to build: the steady state of
	// a scroll that slides one tile at a time. Below two misses, build in place.
	if hi-lo < 2 || l.tileMisses(lo, hi) < 2 {
		for k := range out {
			out[k] = l.Tile(lo + k)
		}
		return out
	}
	// Tiles has no error return, so a recovered internal error must not leave
	// nil entries for the caller to dereference. Workers stop claiming tasks
	// after the first failure; rebuild the rest in place. Tile/buildTile recover
	// internally (and do not cache incomplete results), so this cannot panic.
	if err := l.engine.parallelFor(context.Background(), hi-lo, func(k int) {
		out[k] = l.Tile(lo + k)
	}); err != nil {
		for k := range out {
			if out[k] == nil {
				out[k] = l.Tile(lo + k)
			}
		}
	}
	return out
}

// tileMisses counts the tiles of [lo, hi) that are not in the cache: the work a
// parallel warm-up would actually have to do.
func (l *Layout) tileMisses(lo, hi int) int {
	if l.engine.tileCache <= 0 {
		return hi - lo
	}
	n := 0
	l.tileMu.Lock()
	for i := lo; i < hi; i++ {
		if _, ok := l.tileCache[i]; !ok {
			n++
		}
	}
	l.tileMu.Unlock()
	return n
}

func touchInt(order []int, v int) []int {
	for i := range order {
		if order[i] == v {
			copy(order[i:], order[i+1:])
			order[len(order)-1] = v
			return order
		}
	}
	return append(order, v)
}

func (l *Layout) buildTile(i int) (t *Tile) {
	if l.engine.measure {
		defer l.engine.noteTile(time.Now())
	}
	top := float32(i) * l.tileHeight
	bottom := top + l.tileHeight
	t = &Tile{layout: l, index: i, top: top, height: l.tileHeight}
	// Building a display list shapes text; like every other internal step it
	// must turn a bug into a broken tile rather than a host crash. The tile is
	// marked incomplete so Tile does not cache it (see Tile.incomplete).
	var buildErr error
	defer func() {
		if r := recover(); r != nil {
			buildErr = internalErr(r)
		}
		if buildErr != nil {
			t.incomplete = true
		}
	}()
	if bg := l.opts.Typography.Background; !bg.Transparent() {
		t.ops = append(t.ops, drawOp{
			kind:  opFill,
			rect:  paint.Rect{X: 0, Y: 0, Width: l.opts.Viewport.Width, Height: l.tileHeight},
			color: bg,
		})
	}

	// A tile may span two chapters: do not assume it belongs to one. The
	// included chapters are sorted by content top, so start at the first one
	// whose flow reaches into this tile.
	first, last := l.firstChapter, l.lastChapter
	ci := first + sort.Search(last-first, func(j int) bool {
		ci := first + j
		return l.chapterTop[ci]+l.chapters[ci].height > top
	})
	// Boxes are painted first, behind every chapter's text in this tile.
	tileClip := paint.Rect{X: 0, Y: 0, Width: l.opts.Viewport.Width, Height: l.tileHeight}
	for bx := ci; bx < last; bx++ {
		ct := l.chapterTop[bx]
		if ct >= bottom {
			break
		}
		t.ops = appendBoxOps(t.ops, l.chapters[bx].boxes, tileClip, l.opts.Margins.Left, ct-top)
	}
	// Collect the tile's elements in paint order. Lines are the only elements
	// that need shaping, and they are independent, so they are gathered first
	// and painted in parallel; images are copied straight through.
	type tileElem struct {
		line  int
		image drawOp
		link  string
		isOp  bool
	}
	var (
		jobs  []lineJob
		elems []tileElem
	)
	dx := l.opts.Margins.Left
	for ; ci < last; ci++ {
		ct := l.chapterTop[ci]
		if ct >= bottom {
			break
		}
		cf := &l.chapters[ci]
		localTop := top - ct
		// Items are ordered by content y. Skip whole runs before the tile in
		// O(log n), then walk the few items that intersect it.
		start := sort.Search(cf.count(), func(k int) bool {
			return cf.itemBottom(k) > localTop
		})
		for k := start; k < cf.count(); k++ {
			itv := cf.item(k)
			it := &itv
			itemTop := ct + it.y
			if itemTop >= bottom {
				break
			}
			switch it.kind {
			case itemLine:
				// A line belongs to exactly one tile: the tile containing its
				// top edge. Including straddling lines in both tiles would
				// duplicate glyphs and break the paged/scroll consistency
				// invariant.
				if itemTop < top {
					continue
				}
				elems = append(elems, tileElem{line: len(jobs)})
				jobs = append(jobs, lineJob{it: itv, chapter: ci, x: dx, y: itemTop - top})
			case itemImage:
				if itemTop+it.height <= top {
					continue
				}
				// Images are atomic and can be taller than a tile. Include
				// them in every tile they intersect; the painter clips, and
				// image draws contribute no glyphs to the consistency check.
				r := paint.Rect{
					X: dx + it.x, Y: itemTop - top,
					Width: it.width, Height: it.height,
				}
				elems = append(elems, tileElem{
					isOp:  true,
					image: drawOp{kind: opImage, rect: r, image: it.image},
					link:  it.link,
				})
			}
		}
	}
	painter := l.newPainter(jobs)
	defer painter.close()
	if painter.err != nil {
		// Parallel painting recovered a panic. Keep the tile out of the cache;
		// the loop below still appends the lines that were built.
		buildErr = painter.err
	}
	for i := range elems {
		e := &elems[i]
		if e.isOp {
			t.ops = append(t.ops, e.image)
			if e.link != "" {
				t.links = append(t.links, Link{Rect: e.image.rect, URL: e.link})
			}
			continue
		}
		painter.paint(e.line, &t.ops, &t.links)
	}
	return t
}

// chapterAtContentY returns the chapter whose range contains y. It clamps to
// the first and last chapter for y outside the flow.
func (l *Layout) chapterAtContentY(y float32) int {
	first, last := l.firstChapter, l.lastChapter
	if first >= last {
		return -1
	}
	j := first + sort.Search(last-first, func(i int) bool { return l.chapterTop[first+i] > y }) - 1
	if j < first {
		j = first
	}
	if j >= last {
		j = last - 1
	}
	return j
}

// itemAt returns the item containing y in a chapter's content coordinates.
// first/last bound the search (all items for scroll, one page for paged).
//
// tol is the rounding slack a y taken from OffsetOf may carry: the content
// coordinate is summed at book scale and subtracted again here, so a value that
// names an item's top edge can land a float32 ulp below it. Only a gap larger
// than that slack belongs to the line above; treating rounding as a gap made
// LocationAt(OffsetOf(loc)) return the previous line on long books.
func itemAt(cf *chapterFlow, first, last int, y, tol float32) int {
	total := cf.count()
	first = clampInt(first, 0, total)
	last = clampInt(last, first, total)
	if last <= first {
		return -1
	}
	n := last - first
	// Search with the slack folded in: a y that names an item's top can land a
	// rounding error below the previous line's bottom, and then a plain
	// `bottom > y` search would stop on the previous line.
	k := sort.Search(n, func(i int) bool {
		return cf.itemBottom(first+i) > y+tol
	})
	if k >= n {
		k = n - 1
	}
	idx := first + k
	if idx > first && cf.ys[idx] > y && cf.ys[idx]-y > tol {
		idx--
	}
	return idx
}

// contentSlack is the float32 rounding slack of a content y that is converted
// to a chapter-local y and back. OffsetOf adds the chapter (or page slot) base
// and LocationAt subtracts it, so an item top can come back two ulps low at the
// magnitude of the whole-book coordinate. The floor keeps small layouts exact.
func contentSlack(y float32) float32 {
	if y < 0 {
		y = -y
	}
	return y*(2.0/(1<<23)) + 1e-3
}

// LocationAt maps a book content y to the nearest line (or image) location.
//
// Content coordinates are:
//
//   - Scroll mode: one continuous stream, y=0 at the top of the book's body.
//   - Paged mode: a virtual scroll of the page viewports, where page p covers
//     [p*contentHeight, (p+1)*contentHeight). The page top margin is a viewport
//     inset, so the first item on a page maps to the start of its page slot.
//
// The returned location points at the start of the containing line box.
// OffsetOf(loc) returns that line box's top edge, so a y taken from OffsetOf
// round-trips exactly.
func (l *Layout) LocationAt(y float32) (Location, bool) {
	if l.opts.Mode == ModeScroll {
		ci := l.chapterAtContentY(y)
		if ci < 0 {
			return Location{}, false
		}
		cf := &l.chapters[ci]
		idx := itemAt(cf, 0, cf.count(), y-l.chapterTop[ci], contentSlack(y))
		if idx < 0 {
			return Location{}, false
		}
		return l.locationAtItem(cf, ci, idx)
	}
	info, _, local, ok := l.pagedContentPos(y)
	if !ok {
		return Location{}, false
	}
	cf := &l.chapters[info.chapter]
	idx := itemAt(cf, info.first, info.last, info.top+local, contentSlack(y))
	if idx < 0 {
		return Location{}, false
	}
	return l.locationAtItem(cf, info.chapter, idx)
}

// OffsetOf returns the content y of the line box (or image) containing loc.
//
// It accepts both modes and the same content coordinate system as LocationAt.
// The y is the top edge of the box, not the baseline, so
// LocationAt(OffsetOf(loc)) resolves back to the same line.
func (l *Layout) OffsetOf(loc Location) (float32, bool) {
	loc, ok := l.resolveLocation(loc)
	if !ok {
		return 0, false
	}
	if loc.Chapter < 0 || loc.Chapter >= len(l.chapters) {
		return 0, false
	}
	cf := &l.chapters[loc.Chapter]
	idx, ok := cf.itemOf(loc)
	if !ok || idx < 0 || idx >= cf.count() {
		return 0, false
	}
	if l.opts.Mode == ModeScroll {
		return l.contentYOfItem(cf, loc.Chapter, 0, cf.ys[idx]), true
	}
	page := cf.pageOfItem(idx)
	if page < 0 || page >= len(cf.pages) {
		return 0, false
	}
	return l.contentYOfItem(cf, loc.Chapter, page, cf.ys[idx]), true
}

// contentYOfItem maps a chapter content y to book content coordinates. page is
// the item's chapter-local page index and is used by paged layouts only; in
// scroll mode the chapters are simply stacked (see initScroll).
//
// This is the single definition of the paged content coordinates, shared by
// OffsetOf, Selection and Progress so the three can never disagree.
func (l *Layout) contentYOfItem(cf *chapterFlow, chapter, page int, y float32) float32 {
	if l.opts.Mode == ModeScroll {
		return l.chapterTop[chapter] + y
	}
	h := contentHeight(l.opts)
	return float32(cf.firstPage+page)*h + (y - cf.pages[page].top)
}

// Progress maps loc to a reading progress in [0,1] and reports whether the
// location could be resolved. 0 is the start of the book and 1 its end.
//
// Progress is defined in both modes and is monotone in reading order: a
// location that comes later in the document never reports a smaller value. It
// is derived from the same content coordinates as OffsetOf (and thus from
// Location and the document, never from a pixel scroll offset or from tile
// geometry), so it does not move when the host scrolls and does not change when
// TileHeight changes. Re-laying out with different typography re-derives it
// from the new content coordinates, which is what "position stable, geometry
// recomputed" means for a percentage.
//
//   - Paged mode: contentY / (PageCount * contentHeight), where contentHeight
//     is the viewport height minus the top and bottom margins. This is the
//     usual "page number plus position within the page", because paged content
//     coordinates are page slots that include the page margins.
//   - Scroll mode: contentY / ContentHeight, a position in the continuous
//     stream that has no page margins in it.
//
// The two denominators measure different things, so the same Location may
// report slightly different values in the two modes. Persist the Location, not
// the percentage. A Location points at the top edge of its line box, so the
// last line of a book reports a value slightly below 1; the result is clamped
// to [0,1] regardless. Progress returns false when loc cannot be resolved or
// when the layout has no extent to measure against.
func (l *Layout) Progress(loc Location) (float32, bool) {
	y, ok := l.OffsetOf(loc)
	if !ok {
		return 0, false
	}
	var total float32
	if l.opts.Mode == ModeScroll {
		total = l.contentHeight
	} else {
		total = float32(len(l.pages)) * contentHeight(l.opts)
	}
	if total <= 0 {
		return 0, false
	}
	p := y / total
	if p < 0 {
		return 0, true
	}
	if p > 1 {
		return 1, true
	}
	return p, true
}

// HitTest maps a content point to a document location. It is available in both
// modes and uses the same content coordinates as LocationAt/OffsetOf.
//
// pt.X is measured from the content box's left edge (page Margins.Left
// excluded), matching Page.HitTest's page-local coordinate after margins;
// pt.Y is the content y used by LocationAt and OffsetOf.
func (l *Layout) HitTest(pt paint.Point) (Location, bool) {
	x, y := pt.X, pt.Y
	if l.opts.Mode == ModeScroll {
		ci := l.chapterAtContentY(y)
		if ci < 0 {
			return Location{}, false
		}
		cf := &l.chapters[ci]
		w := l.leaseWorker()
		defer l.releaseWorker(w)
		return l.hitTestItems(w, cf, ci, 0, cf.count(), x, y-l.chapterTop[ci])
	}
	info, _, local, ok := l.pagedContentPos(y)
	if !ok {
		return Location{}, false
	}
	cf := &l.chapters[info.chapter]
	w := l.leaseWorker()
	defer l.releaseWorker(w)
	return l.hitTestItems(w, cf, info.chapter, info.first, info.last, x, info.top+local)
}

// pagedContentPos maps a book-wide virtual page coordinate to a page and a
// local content y within that page.
func (l *Layout) pagedContentPos(y float32) (*pageInfo, int, float32, bool) {
	h := contentHeight(l.opts)
	if h <= 0 || len(l.pages) == 0 {
		return nil, 0, 0, false
	}
	if y < 0 {
		y = 0
	}
	max := float32(len(l.pages)) * h
	if y >= max {
		y = max - 0.001
	}
	p := int(y / h)
	if p < 0 {
		p = 0
	}
	if p >= len(l.pages) {
		p = len(l.pages) - 1
	}
	local := y - float32(p)*h
	if local < 0 {
		local = 0
	}
	return &l.pages[p], p, local, true
}

// locationAtItem returns a Location at the first rune of item idx.
func (l *Layout) locationAtItem(cf *chapterFlow, chapter, idx int) (Location, bool) {
	if idx < 0 || idx >= cf.count() {
		return Location{}, false
	}
	itv := cf.item(idx)
	it := &itv
	if it.kind == itemImage {
		return Location{
			Chapter: chapter,
			Node:    it.node,
			Anchor:  cf.nodeAnchorOf(it.node),
		}, true
	}
	if it.para == nil {
		return Location{}, false
	}
	seg := it.para.segAt(int(it.start))
	if seg == nil {
		return Location{}, false
	}
	off := int(it.start) - int(seg.start)
	if off < 0 {
		off = 0
	}
	return Location{
		Chapter: chapter,
		Node:    seg.node,
		Anchor:  cf.nodeAnchorOf(seg.node),
		Offset:  off,
	}, true
}
