package reader

import "sync"

// Rasterizing a glyph is the one part of drawing text a frame cannot afford: at
// a phone's ratio a character costs several microseconds of coverage, and the
// first frames of a screen's new text cost thousands of them (see
// glyph_atlas.go). Copying a rasterized one into the atlas is a memcpy.
//
// So the coverage is kept apart from the atlas, in a cache the painters of a
// window share: the frame's, which needs a glyph now, and the worker's, which
// walks the tiles the column is heading for and rasterizes the glyphs they will
// draw (see WarmTile). Whichever gets there first pays for the coverage; the
// other copies it.

// glyphCover is one glyph's coverage, trimmed to the pixels its ink reaches: w
// by h bytes, and where they go from the glyph's device pixel origin. A cover
// with no width is a glyph with no ink, which is an answer worth keeping.
//
// A cover is written once and never changed, so a painter may read the pixels
// while another thread holds the cache.
type glyphCover struct {
	w, h   int
	kx, ky int
	pix    []uint8
}

// GlyphCovers is a cache of rasterized glyphs, shared by the painters of one
// window. It is safe for concurrent use, and the covers it hands out are read
// only from the moment they come back.
type GlyphCovers struct {
	mu    sync.Mutex
	index map[glyphKey]*glyphCover
	// order is the keys in the order they came, the oldest first: a glyph is
	// drawn where it is read, so the ones that came longest ago are the ones
	// furthest behind the viewport.
	order []glyphKey
	bytes int
}

// coversOf returns the cache, made on first use.
func (u *UIPainter) coversOf() *GlyphCovers {
	if u.covers == nil {
		u.covers = &GlyphCovers{index: make(map[glyphKey]*glyphCover)}
	}
	return u.covers
}

// Covers is how many glyphs the shared cache holds now: a reader that measures
// a scroll reads it to say whether the frame still rasterizes.
func (u *UIPainter) Covers() int {
	if u == nil || u.covers == nil {
		return 0
	}
	return u.covers.Len()
}

// take returns the cover cached for key, and whether the cache had one.
func (c *GlyphCovers) take(key glyphKey) (*glyphCover, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cv, ok := c.index[key]
	return cv, ok
}

// put keeps a cover for key, letting go of the oldest ones while the cache is
// over half the atlas's budget. Half, because a cover is a byte a pixel and the
// atlas four: the same number of screens takes a quarter of the room.
func (c *GlyphCovers) put(key glyphKey, cv *glyphCover) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.index[key]; ok {
		return
	}
	c.index[key] = cv
	c.order = append(c.order, key)
	c.bytes += cv.bytes()
	budget := GlyphAtlasBudget() / 2
	for c.bytes > budget && len(c.order) > 1 {
		oldest := c.order[0]
		c.order = c.order[1:]
		gone, ok := c.index[oldest]
		if !ok {
			continue
		}
		delete(c.index, oldest)
		c.bytes -= gone.bytes()
	}
}

// Bytes is what the cache holds now, for a reader that measures it.
func (c *GlyphCovers) Bytes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bytes
}

// Len is how many covers the cache holds, blanks included.
func (c *GlyphCovers) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.index)
}

// bytes is the room a cover takes, its box and nothing else.
func (cv *glyphCover) bytes() int {
	if cv == nil {
		return 0
	}
	return cv.w * cv.h
}
