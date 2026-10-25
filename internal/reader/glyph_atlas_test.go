package reader

import (
	"image"
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx/render"
)

// paintViewport replays the viewport of a scrolling column into a fresh frame,
// as the reader's draw callback does, and returns the pixels.
func paintViewport(l *render.Layout, up *UIPainter, scale, scrollY float32) *image.RGBA {
	return ui.Render(func(c *ui.Context) {
		ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
			counter := &countPainter{UIPainter: up}
			drawViewport(l, p, counter, scrollY)
		})
	}, 620, 700, scale)
}

// glyphDiff is how far two frames are apart, per channel of 255.
type glyphDiff struct {
	max, mean    float64
	over8, over3 float64 // share of pixels differing by more than 8 and 32
}

func diffFrames(a, b *image.RGBA) glyphDiff {
	if a.Bounds() != b.Bounds() {
		return glyphDiff{max: math.Inf(1)}
	}
	var d glyphDiff
	n := 0
	var sum float64
	for i := 0; i < len(a.Pix); i += 4 {
		worst := 0
		for c := 0; c < 4; c++ {
			v := abs(int(a.Pix[i+c]) - int(b.Pix[i+c]))
			worst = max(worst, v)
		}
		sum += float64(worst)
		d.max = max(d.max, float64(worst))
		if worst > 8 {
			d.over8++
		}
		if worst > 32 {
			d.over3++
		}
		n++
	}
	d.mean = sum / float64(n)
	d.over8 /= float64(n)
	d.over3 /= float64(n)
	return d
}

// useAtlas forces the painter's glyph path for the rest of a test, and puts the
// package's own choice back after it: a headless frame is drawn by mygo's
// software rasterizer, where the atlas is off unless it is asked for (see
// (*UIPainter).atlasWanted).
func useAtlas(t *testing.T, mode string) {
	t.Helper()
	old := glyphAtlasMode
	glyphAtlasMode = mode
	t.Cleanup(func() { glyphAtlasMode = old })
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// TestGlyphAtlasMatchesOutlines draws the same viewport from the outlines and
// from the atlas, and holds the two to being the same picture: the atlas may
// move where a pixel's coverage is antialiased, but it may not thin, thicken or
// shift the text. Both device pixel ratios are checked, since the atlas keeps a
// bitmap per ratio.
func TestGlyphAtlasMatchesOutlines(t *testing.T) {
	l, up, _ := benchScrollColumn(t)
	useAtlas(t, "")
	const scrollY float32 = 4000
	for _, scale := range []float32{1, 2} {
		for _, fade := range []float32{1, 0.5} {
			glyphAtlasMode = "0"
			up.SetFade(fade)
			want := paintViewport(l, up, scale, scrollY)
			glyphAtlasMode = "1"
			got := paintViewport(l, up, scale, scrollY)
			up.SetFade(1)
			glyphAtlasMode = ""

			d := diffFrames(want, got)
			t.Logf("scale=%g fade=%g: max=%d mean=%.3f >8=%.3f%% >32=%.3f%%",
				scale, fade, int(d.max), d.mean, d.over8*100, d.over3*100)
			if d.mean > 1.5 {
				t.Errorf("scale=%g fade=%g: mean channel difference %.2f of 255, want antialiasing only",
					scale, fade, d.mean)
			}
			if d.over3 > 0.002 {
				t.Errorf("scale=%g fade=%g: %.2f%% of pixels differ by more than 32 of 255",
					scale, fade, d.over3*100)
			}
		}
	}
}

// TestGlyphAtlasIsStable draws the same viewport twice and holds the atlas to
// drawing the same pixels the second time: the frame after the glyphs were
// rasterized must not differ from it.
func TestGlyphAtlasIsStable(t *testing.T) {
	l, up, _ := benchScrollColumn(t)
	useAtlas(t, "1")
	const scrollY float32 = 4000
	for _, scale := range []float32{1, 2} {
		first := paintViewport(l, up, scale, scrollY)
		second := paintViewport(l, up, scale, scrollY)
		if d := diffFrames(first, second); d.max != 0 {
			t.Fatalf("scale=%g: the second frame differs by %d of 255", scale, int(d.max))
		}
	}
}

// TestGlyphAtlasFrame reports what one frame of the column costs the painter
// with the atlas on and off: the runs and glyphs it is asked for, the outlines
// it flattens or the bitmaps it rasterizes, and how much memory the atlas takes.
// The timing of the same frames is in BenchmarkScrollViewportPaint.
func TestGlyphAtlasFrame(t *testing.T) {
	l, up, _ := benchScrollColumn(t)
	useAtlas(t, "")
	glyphStatsOn = true
	defer func() { glyphStatsOn = false }()
	for _, scale := range []float32{1, 2} {
		for _, mode := range []struct {
			name  string
			atlas string
		}{{"outlines", "0"}, {"atlas", "1"}} {
			glyphAtlasMode = mode.atlas
			paintViewport(l, up, scale, 4000)
			up.ResetGlyphStats()
			paintViewport(l, up, scale, 4000)
			s := up.GlyphStats()
			t.Logf("scale=%g %-9s warm runs=%d glyphs=%d lookups=%d hits=%d (%.1f%%) rasters=%d flattens=%d slots=%d layers=%d bytes=%d",
				scale, mode.name, s.Runs, s.Glyphs, s.Lookups, s.Hits, s.HitRate()*100,
				s.Rasters, s.Flattens, s.Slots, s.Layers, s.Bytes)
		}
	}
}

// TestGlyphAtlasByRenderer holds the default to the renderer: a frame drawn on
// a GPU takes the atlas, one drawn by mygo's own software rasterizer does not,
// and LAYX_GLYPH_ATLAS says otherwise.
func TestGlyphAtlasByRenderer(t *testing.T) {
	l, up, _ := benchScrollColumn(t)
	useAtlas(t, "")
	var painter *ui.Painter
	ui.Render(func(c *ui.Context) {
		ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
			painter = p
		})
	}, 100, 100, 1)
	up.P = painter
	if painter.Accelerated() {
		t.Skip("this host draws on a GPU")
	}
	if up.atlasWanted() {
		t.Error("a headless frame draws from the atlas, want the outlines under a software rasterizer")
	}
	glyphAtlasMode = "1"
	if !up.atlasWanted() {
		t.Error("LAYX_GLYPH_ATLAS=1 did not turn the atlas on")
	}
	glyphAtlasMode = "0"
	if up.atlasWanted() {
		t.Error("LAYX_GLYPH_ATLAS=0 did not turn the atlas off")
	}
	_ = l
}

// TestGlyphAtlasScrolling reports the same counters for a glide, where the
// column moves under the viewport and glyphs the atlas never saw come into it:
// that is the frame a reader sees while scrolling, where the warm frame above
// is one that has nothing to do.
func TestGlyphAtlasScrolling(t *testing.T) {
	l, up, _ := benchScrollColumn(t)
	useAtlas(t, "1")
	glyphStatsOn = true
	defer func() { glyphStatsOn = false }()
	const frames = 20
	for _, scale := range []float32{1, 2} {
		scrollY := float32(4000)
		paintViewport(l, up, scale, scrollY)
		up.ResetGlyphStats()
		for i := 0; i < frames; i++ {
			scrollY += 6.3 // a glide's steps are not whole pixels
			paintViewport(l, up, scale, scrollY)
			up.FlushGlyphs()
		}
		s := up.GlyphStats()
		t.Logf("scale=%g glide glyphs=%d/frame lookups=%d hits=%.1f%% rasters=%.1f/frame uploads=%.1f/frame evictions=%d slots=%d layers=%d bytes=%d",
			scale, s.Glyphs/frames, s.Lookups/frames, s.HitRate()*100, float64(s.Rasters)/frames,
			float64(s.Uploads)/frames, s.Evictions, s.Slots, s.Layers, s.Bytes)
		if s.Uploads == 0 {
			t.Errorf("scale=%g: the atlas added glyphs but uploaded nothing", scale)
		}
		if s.Uploads > frames {
			t.Errorf("scale=%g: %d uploads for %d frames, want one a layer a frame", scale, s.Uploads, frames)
		}
	}
}

// TestGlyphAtlasPaintCost times what the painter does inside the frame's draw
// callback, where the reader's page is replayed: the work the atlas replaces,
// apart from what the renderer spends on the pixels it was handed. It drives a
// window the way the reader does, one glide step a frame, so the caches and the
// display lists are as warm as a reader's.
func TestGlyphAtlasPaintCost(t *testing.T) {
	l, up, _ := benchScrollColumn(t)
	useAtlas(t, "")
	const frames = 40
	for _, scale := range []float32{1, 2} {
		for _, mode := range []struct {
			name  string
			atlas string
		}{{"outlines", "0"}, {"atlas", "1"}} {
			glyphAtlasMode = mode.atlas
			var scrollY float32 = 4000
			var draws []time.Duration
			tt := ui.NewTester(func(c *ui.Context) {
				scrollY += 6.3
				paintFrame(c, l, up, scrollY, &draws)
			}, 620, 700)
			tt.SetScale(scale)
			tt.Frame() // the frame that builds the atlas, if there is one
			draws = draws[:0]
			start := time.Now()
			for i := 0; i < frames; i++ {
				tt.Advance(16 * time.Millisecond)
			}
			total := time.Since(start) / frames
			var sum time.Duration
			for _, d := range draws {
				sum += d
			}
			t.Logf("scale=%g %-9s paint=%.3fms/draw (%d draws) frame=%.3fms",
				scale, mode.name, float64(sum)/float64(max(len(draws), 1))/1e6, len(draws), float64(total)/1e6)
		}
	}
}

// viewport draws one frame of the column and records how long the painter spent
// in the draw callback.
func paintFrame(c *ui.Context, l *render.Layout, up *UIPainter, scrollY float32, draws *[]time.Duration) {
	ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
		start := time.Now()
		counter := &countPainter{UIPainter: up}
		drawViewport(l, p, counter, scrollY)
		*draws = append(*draws, time.Since(start))
		p.AnimationFrame()
	})
}
