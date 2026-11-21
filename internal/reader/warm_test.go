package reader

import (
	"testing"

	"github.com/laic7092/layx/render"
)

// A tile warmed by a painter with no surface is a copy a glyph for the painter
// that draws it: the coverage lands in the cache the two share, so the frame
// that bakes the tile rasterizes nothing (see WarmTile and glyphcover.go).
func TestWarmedTilesAreCopiedByTheFrame(t *testing.T) {
	l, frame, _ := benchScrollColumn(t)
	useAtlas(t, "1")
	CountGlyphs(true)
	t.Cleanup(func() { CountGlyphs(false) })
	const scrollY float32 = 900
	// A painter with no surface, as the worker's is. It shares the window
	// painter's faces, corrections and coverage, and walks the tiles the
	// viewport covers for their glyphs — the same boxes the frame will draw.
	warm := NewUIPainter(nil, nil)
	warm.ShareGlyphCaches(frame)
	tiles := viewportTiles(l, scrollY)
	if len(tiles) == 0 {
		t.Skip("the sample laid out no tiles")
	}
	for _, placed := range tiles {
		warm.WarmTile(placed.tile, placed.boxY, 1)
	}
	if warm.Covers() == 0 {
		t.Fatal("warming rasterized no glyph")
	}
	// The frame draws the same viewport: what it finds in its atlas is a copy.
	frame.ResetGlyphStats()
	paintViewport(l, frame, 1, scrollY)
	st := frame.GlyphStats()
	if st.Hits == 0 {
		t.Fatal("the frame found nothing in its atlas for a warmed viewport")
	}
	if st.CoverHits == 0 {
		t.Fatal("the frame did not use the coverage the warmer rasterized")
	}
	if st.Rasters != 0 {
		t.Fatalf("the frame rasterized %d glyphs a warmer had already done", st.Rasters)
	}
}

// A frame that warmed nothing pays for its glyphs, which is what the test above
// is about: the same viewport, without a warmer.
func TestAFrameWithoutAWarmedTileRasterizes(t *testing.T) {
	l, frame, _ := benchScrollColumn(t)
	useAtlas(t, "1")
	CountGlyphs(true)
	t.Cleanup(func() { CountGlyphs(false) })
	frame.ResetGlyphStats()
	paintViewport(l, frame, 1, 0)
	if st := frame.GlyphStats(); st.Rasters == 0 {
		t.Fatal("the frame rasterized nothing, so the test above proves nothing")
	}
}

// placedTile is a tile and where its top edge lands in the viewport.
type placedTile struct {
	tile *render.Tile
	boxY float32
}

// viewportTiles is the tiles a viewport at scrollY covers and where each one
// lands, as the reader's draw callback places them.
func viewportTiles(l *render.Layout, scrollY float32) []placedTile {
	const viewH float32 = 700
	tileH := viewH
	if tile := l.Tile(0); tile != nil {
		tileH = tile.Height()
	}
	first := max(int(scrollY/tileH)-1, 0)
	last := min(int((scrollY+viewH)/tileH), l.TileCount()-1)
	var out []placedTile
	for i := last; i >= first; i-- {
		tile := l.Tile(i)
		if tile == nil {
			continue
		}
		out = append(out, placedTile{tile: tile, boxY: tile.Top() - scrollY})
	}
	return out
}
