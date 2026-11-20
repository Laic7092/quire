package render

import (
	"time"

	"github.com/laix/layx/render/paint"
)

type drawOpKind uint8

const (
	opFill drawOpKind = iota
	opRoundFill
	opImage
	opGlyphs
)

type drawOp struct {
	kind   drawOpKind
	rect   paint.Rect
	color  paint.Color
	radius float32
	image  paint.ImageID
	run    paint.GlyphRun
}

// Page is an immutable, cached page. Draw may be called concurrently as long
// as each goroutine uses its own Painter.
type Page struct {
	layout  *Layout
	number  int
	chapter int
	top     float32
	ops     []drawOp
	links   []Link
	// incomplete marks a display list that a recovered panic or a failed
	// parallel paint step cut short. Page never caches such a page; building it
	// again is cheap compared to serving a half-drawn frame from the cache.
	incomplete bool
}

// Number returns the zero-based global page number.
func (p *Page) Number() int { return p.number }

// Size returns the page viewport.
func (p *Page) Size() paint.Size {
	return paint.Size{Width: p.layout.opts.Viewport.Width, Height: p.layout.opts.Viewport.Height}
}

// Draw replays the page display list into painter.
//
// Draw is pure replay: it does not lay out, shape, allocate or depend on a
// previous Draw. The same Page may be drawn repeatedly, in any order, from any
// number of painters; a host that animates a turn can draw old and new pages
// into one painter with different transforms.
//
// Draw calls into host code (the paint.Painter implementation), and the library
// does not recover panics from it: a panic in a painter method propagates out of
// Draw into the calling goroutine, exactly like a panic in any other host
// callback. The painter must guard its own boundary if it wants to keep going;
// locking a Painter to one goroutine and never panicking from it is the host's
// side of this contract (see the paint package documentation). Panics raised by
// the library's own layout and display-list work are a different matter: those
// are recovered and returned as errors wrapping ErrInternal.
func (p *Page) Draw(painter paint.Painter) {
	if painter == nil {
		return
	}
	visible, cull := visibleRect(painter)
	painter.Save()
	painter.ClipRect(paint.Rect{X: 0, Y: 0, Width: p.Size().Width, Height: p.Size().Height})
	for i := range p.ops {
		op := &p.ops[i]
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

// Links returns the host links on this page.
func (p *Page) Links() []Link {
	out := make([]Link, len(p.links))
	copy(out, p.links)
	return out
}

// Page returns page n, or nil if n is out of range. The page is rendered on
// first access and cached according to Options.PageCache.
//
// Building a page shapes its lines on the engine's budget, so a page turn uses
// the whole machine instead of one core. Prefetch warms a window ahead of time.
func (l *Layout) Page(n int) *Page {
	if l.opts.Mode == ModeScroll {
		return nil
	}
	if n < 0 || n >= len(l.pages) {
		return nil
	}
	limit := l.engine.pageCache
	if limit > 0 {
		l.cacheMu.Lock()
		if p, ok := l.cache[n]; ok {
			l.cacheMu.Unlock()
			return p
		}
		l.cacheMu.Unlock()
	}
	p := l.renderPage(n)
	if limit <= 0 || p.incomplete {
		// A page cut short by a recovered internal error must not enter the
		// cache: the host would keep drawing a partial frame for every later
		// request. It is returned once (draw whatever was built), and the next
		// Page(n) retries the build.
		return p
	}
	l.cacheMu.Lock()
	if existing, ok := l.cache[n]; ok {
		l.cacheMu.Unlock()
		return existing
	}
	for len(l.cache) >= limit && len(l.cacheOrder) > 0 {
		old := l.cacheOrder[0]
		l.cacheOrder = l.cacheOrder[1:]
		delete(l.cache, old)
	}
	l.cache[n] = p
	l.cacheOrder = append(l.cacheOrder, n)
	l.cacheMu.Unlock()
	return p
}

func (l *Layout) renderPage(n int) (p *Page) {
	if l.engine.measure {
		defer l.engine.notePage(time.Now())
	}
	info := &l.pages[n]
	cf := &l.chapters[info.chapter]

	p = &Page{layout: l, number: n, chapter: info.chapter, top: info.top}
	// Building a display list shapes text; like every other internal step it
	// must turn a bug into a broken page rather than a host crash. The page is
	// marked incomplete so Page does not cache it (see Page.incomplete).
	var buildErr error
	defer func() {
		if r := recover(); r != nil {
			buildErr = internalErr(r)
		}
		if buildErr != nil {
			p.incomplete = true
		}
	}()
	if bg := l.opts.Typography.Background; !bg.Transparent() {
		p.ops = append(p.ops, drawOp{
			kind:  opFill,
			rect:  paint.Rect{X: 0, Y: 0, Width: l.opts.Viewport.Width, Height: l.opts.Viewport.Height},
			color: bg,
		})
	}
	dx := l.opts.Margins.Left
	dy := l.opts.Margins.Top - info.top
	p.ops = appendBoxOps(p.ops, cf.boxes, paint.Rect{
		X: l.opts.Margins.Left, Y: l.opts.Margins.Top,
		Width: contentWidth(l.opts), Height: contentHeight(l.opts),
	}, dx, dy)

	first, last := info.first, info.last
	if first < 0 {
		first = 0
	}
	if n := cf.count(); last > n {
		last = n
	}
	// Lines are the only items that need shaping, and they are independent, so
	// they are collected first and built in parallel. Everything else is a
	// cheap copy in the loop below.
	var jobs []lineJob
	for i := first; i < last; i++ {
		if it := cf.item(i); it.kind == itemLine {
			jobs = append(jobs, lineJob{it: it, chapter: info.chapter, x: dx, y: dy + it.y})
		}
	}
	painter := l.newPainter(jobs)
	defer painter.close()
	if painter.err != nil {
		// Parallel painting recovered a panic. Keep the page out of the cache;
		// the loop below still appends the lines that were built.
		buildErr = painter.err
	}

	k := 0
	for i := first; i < last; i++ {
		itv := cf.item(i)
		it := &itv
		itemY := dy + it.y
		switch it.kind {
		case itemImage:
			r := paint.Rect{X: dx + it.x, Y: itemY, Width: it.width, Height: it.height}
			p.ops = append(p.ops, drawOp{kind: opImage, rect: r, image: it.image})
			if it.link != "" {
				p.links = append(p.links, Link{Rect: r, URL: it.link})
			}
		case itemLine:
			painter.paint(k, &p.ops, &p.links)
			k++
		}
	}
	return p
}

// runColor resolves a run's paint colour. A style that names a colour keeps it
// (author CSS wins); a link whose style names none takes the layout's
// LinkColor, and everything else the layout's ink. Both come from Typography,
// so a paint-only Relayout repaints them without re-breaking lines.
func (w *textWorker) runColor(link bool, c paint.Color) paint.Color {
	if !c.Transparent() {
		return c
	}
	if link && !w.typo.LinkColor.Transparent() {
		return w.typo.LinkColor
	}
	return w.typo.Color
}

type glyphPos struct {
	runeIndex int
	x0, x1    float32
}

func isJustifySpace(r rune) bool {
	switch r {
	case ' ', '\u00a0', '\u3000':
		return true
	}
	return false
}

// buildLine shapes one line and returns drawable runs plus per-glyph positions
// relative to the line item's left edge. Shaped runs come from the Layout's
// bounded line cache; justification is applied here, on every use.
func (l *Layout) buildLine(w *textWorker, it *flowItem, chapter int) ([]paint.GlyphRun, []glyphPos) {
	p := it.para
	if p == nil || int(it.end) <= int(it.start) {
		return nil, nil
	}
	runs, weights := l.shapeLine(w, it, chapter)
	if len(runs) == 0 {
		return nil, nil
	}
	extra := float32(0)
	if it.align == AlignJustify && !it.last && it.avail > it.width {
		extra = it.avail - it.width
	}
	glyphCount := 0
	for i := range runs {
		glyphCount += len(runs[i].out.Glyphs)
	}

	// Justification slack is shared by spaces and CJK inter-character gaps.
	// Distributing it to spaces alone (the old behaviour) opens huge holes in
	// mixed Chinese/Latin lines, which usually contain only a few spaces.
	factor := float32(0)
	if extra > 0 && glyphCount > 1 {
		total := float32(0)
		for _, wgt := range weights {
			total += wgt
		}
		if total > 0 {
			factor = extra / total
		} else {
			// No stretchable slot (e.g. one long Latin word): fall back to
			// spreading the slack over every interior gap.
			for k := 0; k+1 < len(weights); k++ {
				weights[k] = 1
			}
			factor = extra / float32(glyphCount-1)
		}
	}

	pen := it.x
	out := make([]paint.GlyphRun, 0, len(runs))
	pos := make([]glyphPos, 0, glyphCount)
	k := 0
	for i := range runs {
		sr := &runs[i]
		ref := sr.ref
		color := w.runColor(sr.style.link, sr.style.color)
		run := paint.GlyphRun{
			Font:  ref,
			Size:  float32(sr.out.Size) / 64,
			Color: color,
			X:     pen,
			Y:     it.baseline,
			// Weight matching happened in FontSet.order / shapeRange; the
			// core only flags that the matched face is not bold. Whether to
			// synthesize a bold face is the host's decision.
			EmulateBold: emulateBold(sr.style.fontWeight, ref),
		}
		run.Glyphs = make([]paint.Glyph, 0, len(sr.out.Glyphs))
		for _, g := range sr.out.Glyphs {
			idx := g.TextIndex()
			gx := pen + float32(g.XOffset)/64
			adv := float32(g.Advance) / 64
			glyph := paint.Glyph{
				ID:      uint32(g.GlyphID),
				X:       gx - run.X,
				Y:       float32(g.YOffset) / 64,
				Advance: adv,
				Cluster: int32(idx - int(it.start)),
			}
			pen += adv
			if factor > 0 && k < len(weights) && weights[k] > 0 {
				add := weights[k] * factor
				pen += add
				glyph.Advance += add
			}
			pos = append(pos, glyphPos{runeIndex: idx, x0: gx - it.x, x1: pen - it.x})
			k++
			run.Glyphs = append(run.Glyphs, glyph)
		}
		out = append(out, run)
	}
	if it.para != nil && it.para.hyphenAt(it.end) {
		at := int(it.end) - 1
		st := it.para.styleAt(at)
		var langTag string
		var link bool
		if seg := it.para.segAt(at); seg != nil {
			langTag, link = seg.lang, seg.link != ""
		}
		if ref, size, hg, ok := w.shapeHyphen(st, langTag); ok {
			color := w.runColor(link, st.Color)
			adv := float32(hg.Advance) / 64
			if measured, ok := it.para.hyphAdv[it.end]; ok {
				adv = measured
			}
			hrun := paint.GlyphRun{
				Font: ref, Size: size, Color: color, X: pen, Y: it.baseline,
				EmulateBold: emulateBold(st.FontWeight, ref),
			}
			hrun.Glyphs = []paint.Glyph{{
				ID:      uint32(hg.GlyphID),
				Y:       float32(hg.YOffset) / 64,
				Advance: adv,
				Cluster: int32(it.end - it.start),
			}}
			x0 := pen
			pen += adv
			pos = append(pos, glyphPos{runeIndex: int(it.end), x0: x0 - it.x, x1: pen - it.x})
			out = append(out, hrun)
		}
	}
	return out, pos
}

// justifyWeights marks the glyphs that can absorb justification slack. A
// weight sits on the glyph before the gap it widens: spaces take slack on
// their own advance, and CJK inter-character gaps take slack at full weight.
// Gaps touching punctuation get half weight, so fullwidth punctuation does
// not turn into a hole.
//
// When punctuation compression is on, the slack must not flow back into the
// half em the trim removed. That space sits after the trimmed opening mark, so
// the weight on that mark is zeroed.
func justifyWeights(text []rune, runs []shapedRun, compress bool) []float32 {
	idx := make([]int, 0, 64)
	for i := range runs {
		for _, g := range runs[i].out.Glyphs {
			idx = append(idx, g.TextIndex())
		}
	}
	weights := make([]float32, len(idx))
	for k, i := range idx {
		if i >= 0 && i < len(text) && isJustifySpace(text[i]) {
			weights[k] = 1
		}
	}
	for k := 1; k < len(idx); k++ {
		a, b := idx[k-1], idx[k]
		if a < 0 || b != a+1 || a >= len(text) || b >= len(text) {
			continue
		}
		ra, rb := text[a], text[b]
		if isJustifySpace(ra) || isJustifySpace(rb) {
			continue
		}
		if !isIdeographic(ra) && !isIdeographic(rb) {
			continue
		}
		if isCJKPunctuation(ra) || isCJKPunctuation(rb) {
			weights[k-1] += 0.5
		} else {
			weights[k-1] += 1
		}
	}
	if compress {
		for k := 1; k < len(idx); k++ {
			a, b := idx[k-1], idx[k]
			if b != a+1 {
				continue
			}
			target, ok := trimTarget(text, a, b)
			if !ok {
				continue
			}
			// The slack must not flow back into the half em the trim removed.
			// An opening mark loses its leading half, so the removed space is
			// the gap after it; a closing mark loses its trailing half, which
			// is the gap between the pair.
			if target == b {
				weights[k] = 0
			} else {
				weights[k-1] = 0
			}
		}
	}
	return weights
}

// shapeLine returns the cached shaped runs for a line, shaping on a miss. The
// returned slice is shared and must be treated as read-only.
func (l *Layout) shapeLine(w *textWorker, it *flowItem, chapter int) ([]shapedRun, []float32) {
	if it.para == nil || int(it.end) <= int(it.start) {
		return nil, nil
	}
	key := lineKey{chapter: chapter, para: it.para, start: it.start, end: it.end}
	if e, ok := l.lineCache.get(key); ok {
		return e.runs, e.weights
	}
	text := w.paraText(it.para)
	runs := w.shapeRange(it.para, text, int(it.start), int(it.end), w.defaultLang)
	var weights []float32
	if it.align == AlignJustify && !it.last && it.avail > it.width {
		// The weights depend only on the text, so they are computed with the
		// shaping they belong to and cached with it: drawing a justified line
		// must not materialize the paragraph again.
		weights = justifyWeights(text, runs, w.typo.Rules.PunctuationCompression)
	}
	l.lineCache.put(&lineEntry{key: key, runs: runs, weights: weights})
	return runs, weights
}

// lineRuneX maps a paragraph rune range to line-local x coordinates. Positions
// that fall inside a glyph cluster snap to that cluster's edge.
func (l *Layout) lineRuneX(w *textWorker, it *flowItem, chapter int, lo, hi int) (float32, float32) {
	return runeXFromPos(l.lineGlyphPositions(w, it, chapter), lo, hi)
}

func runeXFromPos(pos []glyphPos, lo, hi int) (float32, float32) {
	if len(pos) == 0 {
		return 0, 0
	}
	x0 := pos[len(pos)-1].x1
	for _, p := range pos {
		if p.runeIndex >= lo {
			x0 = p.x0
			break
		}
	}
	x1 := x0
	for _, p := range pos {
		if p.runeIndex >= hi {
			break
		}
		if p.x1 > x1 {
			x1 = p.x1
		}
	}
	if x1 < x0 {
		x0, x1 = x1, x0
	}
	return x0, x1
}

func (l *Layout) lineGlyphPositions(w *textWorker, it *flowItem, chapter int) []glyphPos {
	_, pos := l.buildLine(w, it, chapter)
	return pos
}

// HitTest maps a page coordinate to the nearest document location on this
// page.
func (p *Page) HitTest(pt paint.Point) (Location, bool) {
	l := p.layout
	info := &l.pages[p.number]
	cf := &l.chapters[info.chapter]
	x := pt.X - l.opts.Margins.Left
	y := pt.Y - l.opts.Margins.Top + info.top
	w := l.leaseWorker()
	defer l.releaseWorker(w)
	return l.hitTestItems(w, cf, info.chapter, info.first, info.last, x, y)
}

// hitTestItems implements page and layout hit testing in chapter content
// coordinates. first/last bound the items that may be hit (a page range, or
// the whole chapter for scroll layouts).
func (l *Layout) hitTestItems(w *textWorker, cf *chapterFlow, chapter, first, last int, x, y float32) (Location, bool) {
	// An empty flow has no items and no unit to look up: unitOf[0] does not
	// exist. Hit testing is the reader's highest-traffic path (tap to place the
	// caret, press to select, double-tap to pick a word), so an empty text
	// node, an empty block or a style-hidden chapter must be a miss, never a
	// panic (audit §2.2).
	n := cf.count()
	if n == 0 {
		return Location{}, false
	}
	if last > n {
		last = n
	}
	if first < 0 {
		first = 0
	}
	if first >= last {
		return Location{}, false
	}
	// Walk unit by unit: a unit's items are contiguous in both the unit and
	// the line slots, so this touches each line box once instead of looking it
	// up per item.
	best := -1
	bestDist := float32(1e30)
	u := int(cf.unitOf[first])
	for i := first; i < last; {
		lu := &cf.units[u]
		end := int(lu.first) + lu.itemCount()
		if end > last {
			end = last
		}
		for k := i; k < end; k++ {
			it := cf.unitExtent(u, k-int(lu.first), k)
			width := it.width
			if it.kind == itemLine {
				width = it.avail
			}
			dx := axisDistance(x, it.x, it.x+width)
			dy := axisDistance(y, it.y, it.y+it.height)
			if d := dx*dx + dy*dy; d < bestDist {
				best, bestDist = k, d
			}
		}
		i = end
		u++
	}
	if best < 0 {
		return Location{}, false
	}
	bestIt := cf.item(best)
	it := &bestIt
	if it.kind == itemImage {
		return Location{
			Chapter: chapter,
			Node:    it.node,
			Anchor:  cf.nodeAnchorOf(it.node),
		}, true
	}
	pos := l.lineGlyphPositions(w, it, chapter)
	if len(pos) == 0 {
		return Location{}, false
	}
	runeIndex := pos[len(pos)-1].runeIndex
	if x <= pos[0].x0+it.x {
		runeIndex = pos[0].runeIndex
	} else {
		for _, gp := range pos {
			if x < it.x+gp.x1 {
				runeIndex = gp.runeIndex
				break
			}
		}
	}
	seg := it.para.segAt(runeIndex)
	if seg == nil {
		return Location{}, false
	}
	off := runeIndex - int(seg.start)
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

func axisDistance(v, lo, hi float32) float32 {
	if v < lo {
		return lo - v
	}
	if v > hi {
		return v - hi
	}
	return 0
}
