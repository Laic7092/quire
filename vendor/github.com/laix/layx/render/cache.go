package render

import (
	"container/list"
	"hash/maphash"
	"sync"
)

// breakChapter is the reusable result of shaping and line breaking a chapter.
// It contains no vertical positions: those depend on PageKey and are applied
// by stackBreakUnits.
type breakChapter struct {
	key   keyHash
	units []breakUnit

	// Placements (node -> paragraph -> item) depend only on the break result,
	// not on vertical typography, so they are computed once per BreakKey.
	placeOnce sync.Once
	place     *chapterPlacements
}

// chapterPlacements is the BreakKey-scoped half of a chapterFlow.
type chapterPlacements struct {
	placements []paraPlacement
	nodePlace  []int32
	nodeFirst  []int32
	nodeAnchor []AnchorID
	anchors    map[AnchorID]anchorPos
}

// cacheKey identifies one chapter variant. The cache itself is scoped to one
// document (it is created by Layout and carried through Relayout), so the
// document pointer is not part of the key. Fonts are, because Relayout may be
// called with a different FontSet for the same document.
type cacheKey struct {
	chapter int
	fonts   *FontSet
	key     keyHash
}

// lineKey identifies one shaped line. The cache is scoped to a single Layout,
// so a paragraph pointer is a stable identity for the lifetime of every entry.
// The chapter and rune range are included so relayout variants never mix lines
// whose shaping inputs could differ.
type lineKey struct {
	chapter int
	para    *paragraph
	start   int32
	end     int32
}

type lineEntry struct {
	key  lineKey
	runs []shapedRun
	// weights are the justification weights of the line's glyphs, computed when
	// the line is shaped. They depend only on the text and the punctuation
	// rules, so drawing never has to materialize the paragraph's runes again.
	weights []float32
}

// lineCache is a bounded LRU of shaped, not-yet-justified lines. Justification
// depends on the line's available width and is applied by buildLine on every
// use, so a shape result can be shared by every consumer of the same Layout.
//
// The value stores shaped runs, not flow items and not positioned glyph runs:
// callers must treat the returned slice as read-only.
//
// The cache is sharded so that building several pages at once (or a tile while
// a page is being built) does not serialise on one mutex: every line lookup
// takes its stripe by hash. Total capacity is unchanged — each stripe keeps
// limit/stripes entries.
type lineCache struct {
	stripes []lineCacheStripe
}

type lineCacheStripe struct {
	mu      sync.Mutex
	limit   int
	order   *list.List
	entries map[lineKey]*list.Element
}

// lineCacheStripes is the number of shards. Sixteen is enough to keep lock
// contention invisible on a 16-thread machine while still leaving each stripe
// a useful share of a 256-entry default cache.
const lineCacheStripes = 16

func newLineCache(limit int) *lineCache {
	n := lineCacheStripes
	if limit < n {
		n = limit
	}
	if n <= 0 {
		return nil
	}
	per := limit / n
	if per < 1 {
		per = 1
	}
	c := &lineCache{stripes: make([]lineCacheStripe, n)}
	for i := range c.stripes {
		c.stripes[i] = lineCacheStripe{
			limit:   per,
			order:   list.New(),
			entries: make(map[lineKey]*list.Element, per),
		}
	}
	return c
}

// stripeFor picks the shard for a key. Any deterministic function of the key
// works; the rune range spreads a paragraph's lines across shards, which is
// what makes parallel page building scale.
func (c *lineCache) stripeFor(key lineKey) *lineCacheStripe {
	h := uint32(key.start)*2654435761 + uint32(key.chapter)*40503
	return &c.stripes[int(h>>7)%len(c.stripes)]
}

func (c *lineCache) get(key lineKey) (*lineEntry, bool) {
	if c == nil || len(c.stripes) == 0 {
		return nil, false
	}
	s := c.stripeFor(key)
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	if !ok {
		return nil, false
	}
	s.order.MoveToFront(e)
	return e.Value.(*lineEntry), true
}

func (c *lineCache) put(e *lineEntry) {
	if c == nil || len(c.stripes) == 0 {
		return
	}
	s := c.stripeFor(e.key)
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.entries[e.key]; ok {
		v := existing.Value.(*lineEntry)
		v.runs, v.weights = e.runs, e.weights
		s.order.MoveToFront(existing)
		return
	}
	for s.order.Len() >= s.limit {
		back := s.order.Back()
		if back == nil {
			break
		}
		delete(s.entries, back.Value.(*lineEntry).key)
		s.order.Remove(back)
	}
	s.entries[e.key] = s.order.PushFront(e)
}

// size reports the number of cached lines across all stripes. It is used by
// tests and diagnostics.
func (c *lineCache) size() int {
	if c == nil {
		return 0
	}
	n := 0
	for i := range c.stripes {
		s := &c.stripes[i]
		s.mu.Lock()
		n += s.order.Len()
		s.mu.Unlock()
	}
	return n
}

// variantCache keeps the most recent whole-document variants. Capacity is
// derived from the chapter count so that one full layout always fits in two
// variants; the contract is "the last 2-4 variants", not an unbounded cache.
type variantCache struct {
	mu sync.Mutex

	// Seed the digest per document chain so BreakKey/PageKey values stay
	// comparable across Relayout calls without any global state.
	seedA maphash.Seed
	seedB maphash.Seed

	breakLimit int
	breakMap   map[cacheKey]*breakChapter
	breakOrder []cacheKey

	pageLimit int
	pageMap   map[cacheKey]*chapterFlow
	pageOrder []cacheKey

	// A Document is immutable and an Engine's Limits are fixed, so the
	// O(runes) limit scan only has to run once per document chain.
	limitsDone bool
}

// variantsPerChapter is the number of recent variants retained per chapter.
// Two is enough for the common A/B toggle (e.g. line height 1.5 <-> 1.8)
// without letting a large book's retained line boxes grow without bound.
const variantsPerChapter = 2

func newVariantCache(chapters int) *variantCache {
	limit := chapters * variantsPerChapter
	if limit < variantsPerChapter {
		limit = variantsPerChapter
	}
	return &variantCache{
		seedA:      maphash.MakeSeed(),
		seedB:      maphash.MakeSeed(),
		breakLimit: limit,
		breakMap:   make(map[cacheKey]*breakChapter, limit),
		pageLimit:  limit,
		pageMap:    make(map[cacheKey]*chapterFlow, limit),
	}
}

func (c *variantCache) limitsVerified() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.limitsDone
}

func (c *variantCache) markLimitsVerified() {
	c.mu.Lock()
	c.limitsDone = true
	c.mu.Unlock()
}

func (c *variantCache) newKeyer() *keyer {
	return &keyer{seedA: c.seedA, seedB: c.seedB}
}

// seeds returns the cache's hash seeds. Parallel key passes build one keyer per
// task from the same seeds, so digests are identical to a sequential pass and
// stay comparable across Relayout calls. The seeds are immutable once the
// cache exists, so reading them needs no lock.
func (c *variantCache) seeds() (maphash.Seed, maphash.Seed) {
	return c.seedA, c.seedB
}

func (c *variantCache) getBreak(key cacheKey) *breakChapter {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.breakMap[key]
	if !ok {
		return nil
	}
	c.breakOrder = touchKey(c.breakOrder, key)
	return v
}

func (c *variantCache) putBreak(key cacheKey, v *breakChapter) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.breakMap[key]; exists {
		c.breakMap[key] = v
		return
	}
	for len(c.breakMap) >= c.breakLimit && len(c.breakOrder) > 0 {
		old := c.breakOrder[0]
		c.breakOrder = c.breakOrder[1:]
		delete(c.breakMap, old)
	}
	c.breakMap[key] = v
	c.breakOrder = append(c.breakOrder, key)
}

func (c *variantCache) getPage(key cacheKey) *chapterFlow {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.pageMap[key]
	if !ok {
		return nil
	}
	c.pageOrder = touchKey(c.pageOrder, key)
	return v
}

func (c *variantCache) putPage(key cacheKey, v *chapterFlow) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.pageMap[key]; exists {
		c.pageMap[key] = v
		return
	}
	for len(c.pageMap) >= c.pageLimit && len(c.pageOrder) > 0 {
		old := c.pageOrder[0]
		c.pageOrder = c.pageOrder[1:]
		delete(c.pageMap, old)
	}
	c.pageMap[key] = v
	c.pageOrder = append(c.pageOrder, key)
}

// touchKey moves key to the most-recent end of a small LRU order slice.
func touchKey(order []cacheKey, key cacheKey) []cacheKey {
	for i := range order {
		if order[i] == key {
			copy(order[i:], order[i+1:])
			order[len(order)-1] = key
			return order
		}
	}
	return append(order, key)
}
