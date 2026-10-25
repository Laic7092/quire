package reader

import (
	"image"
	"math"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx/render"
	"github.com/laix/layx/render/paint"
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

// usePlainCoverage turns off what the atlas does to a glyph's coverage beyond
// rasterizing it — the ink's correction and the growth of a synthetic bold —
// for a test that compares the atlas with the outlines. Both are baked into the
// atlas and neither into a path fill, and what those two paths are held to being
// is the same picture of the same outlines.
func usePlainCoverage(t *testing.T) {
	t.Helper()
	oldTone, oldBold := textToneOff, fakeBoldOff
	textToneOff, fakeBoldOff = true, true
	t.Cleanup(func() { textToneOff, fakeBoldOff = oldTone, oldBold })
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
	usePlainCoverage(t)
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

// TestGlyphRasterizersMatch paints the same viewport with the atlas built by
// layx-raster's Coverage and by x/image/vector, and holds them to being the
// same picture: the choice of rasterizer may move where a pixel's coverage is
// antialiased, but it may not thin, thicken or shift the text.
func TestGlyphRasterizersMatch(t *testing.T) {
	l, up, _ := benchScrollColumn(t)
	useAtlas(t, "1")
	usePlainCoverage(t)
	old := glyphRasterMode
	t.Cleanup(func() { glyphRasterMode = old })
	const scrollY float32 = 4000
	for _, scale := range []float32{1, 2} {
		// The atlas key does not name the rasterizer, so switching it has to
		// drop the bitmaps the other one made.
		up.glyphs = nil
		glyphRasterMode = "vector"
		want := paintViewport(l, up, scale, scrollY)
		up.glyphs = nil
		glyphRasterMode = "layx"
		got := paintViewport(l, up, scale, scrollY)

		d := diffFrames(want, got)
		t.Logf("scale=%g: max=%d mean=%.3f >8=%.3f%% >32=%.3f%%",
			scale, int(d.max), d.mean, d.over8*100, d.over3*100)
		if d.mean > 0.5 {
			t.Errorf("scale=%g: mean channel difference %.2f of 255, want the same picture", scale, d.mean)
		}
		if d.over3 > 0.0005 {
			t.Errorf("scale=%g: %.2f%% of pixels differ by more than 32 of 255", scale, d.over3*100)
		}
	}
}

// BenchmarkGlyphRasterizer measures the two coverage rasterizers on the same
// glyph outlines, at the place the atlas would rasterize them (17 DIP at DPR
// 2). It is the cost a cold atlas pays; a warm frame is a lookup either way.
func BenchmarkGlyphRasterizer(b *testing.B) {
	path, err := FindFont("")
	if err != nil {
		b.Skipf("no font available: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	up := NewUIPainter(nil, nil)
	gf := up.glyphFace(&paint.Font{Data: data})
	upem := float64(gf.face.Upem())
	if upem <= 0 {
		b.Skip("the face has no units per em")
	}
	const size, dpr = 17.0, 2.0
	s := size * dpr / upem
	const fx, fy = 0.25, 0.75

	type subject struct {
		out          *glyphOutline
		w, h, kx, ky int
	}
	var subjects []subject
	for _, r := range "天地玄黄宇宙洪荒日月盈昃辰宿列张寒来暑往秋收冬藏AVWgjmy" {
		gid, ok := gf.face.NominalGlyph(r)
		if !ok {
			continue
		}
		out := gf.outline(uint32(gid))
		if out == nil || len(out.segs) == 0 {
			continue
		}
		left := int(math.Floor(fx + float64(out.minX)*s))
		top := int(math.Floor(fy + float64(out.minY)*s))
		right := int(math.Ceil(fx+float64(out.maxX)*s)) + 1
		bottom := int(math.Ceil(fy+float64(out.maxY)*s)) + 1
		if w, h := right-left, bottom-top; w > 0 && h > 0 {
			subjects = append(subjects, subject{out: out, w: w, h: h, kx: left, ky: top})
		}
	}
	if len(subjects) == 0 {
		b.Skip("the face has no drawable glyphs in the sample")
	}

	for _, mode := range []string{"vector", "layx"} {
		b.Run(mode, func(b *testing.B) {
			old := glyphRasterMode
			glyphRasterMode = mode
			b.Cleanup(func() { glyphRasterMode = old })
			a := &glyphAtlas{}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				for _, g := range subjects {
					a.renderCoverage(g.w, g.h, g.out, s, fx, fy, g.kx, g.ky, nil, 0)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(len(subjects)), "ns/glyph")
		})
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

// TestGlyphAtlasByRenderer holds the default to the atlas on every renderer,
// and the switch to the variable: the outlines are the A/B, not the default.
func TestGlyphAtlasByRenderer(t *testing.T) {
	l, up, _ := benchScrollColumn(t)
	useAtlas(t, "")
	if !up.atlasWanted() {
		t.Error("a frame does not draw from the atlas by default")
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
		// A frame uploads every layer it wrote to, so a frame that fills one
		// layer and opens the next costs two: the bound is a layer a frame plus
		// the layers the run opened.
		if s.Uploads > frames+s.Layers {
			t.Errorf("scale=%g: %d uploads for %d frames and %d layers", scale, s.Uploads, frames, s.Layers)
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

// inkMass is how much ink a frame carries: every pixel counted by how far it is
// from the paper, over the whole picture. It is the number that says whether the
// type got heavier, which is what the tone correction and a synthetic bold are
// for.
func inkMass(img *image.RGBA) int64 {
	var sum int64
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			sum += int64(255*3) - (int64(r>>8) + int64(g>>8) + int64(b>>8))
		}
	}
	return sum
}

// TestTextToneMakesTheEdgesInkier holds the ink correction to what it is for:
// the same page drawn with it carries more ink, because every antialiased edge
// of a stroke is drawn heavier than a bare geometric rasterizer draws it. It
// also holds the rollback to being the plain rasterizer's picture.
func TestTextToneMakesTheEdgesInkier(t *testing.T) {
	l, up, _ := benchScrollColumn(t)
	useAtlas(t, "1")
	fakeBoldOff = true
	const scrollY float32 = 4000
	textToneOff = true
	t.Cleanup(func() { textToneOff, fakeBoldOff = false, false })
	plain := inkMass(paintViewport(l, up, 1, scrollY))
	textToneOff = false
	corrected := inkMass(paintViewport(l, up, 1, scrollY))
	if corrected <= plain {
		t.Fatalf("the correction carried %d of the %d ink the plain coverage did", corrected, plain)
	}
	// A page is mostly paper, so a few per cent of its ink is a tenth or more of
	// the ink the type carries. Ten times that would be a mistake in the curve.
	if grew := float64(corrected-plain) / float64(plain); grew > 0.05 {
		t.Errorf("the correction added %.1f%% of the page's ink, want a per cent or two", grew*100)
	}
	// Off means off: the same picture as before it was turned on.
	textToneOff = true
	if again := inkMass(paintViewport(l, up, 1, scrollY)); again != plain {
		t.Errorf("turning the correction off drew %d, want the %d it started with", again, plain)
	}
}

// TestTextToneCurve holds the table itself to the shape the correction has to
// have: the paper and the ink stay where they are, the edges between them grow,
// and a pale ink — a night paper's — gets no contrast on top of the growth,
// since what a dark page needs is the growth and not more weight.
func TestTextToneCurve(t *testing.T) {
	dark := toneLUTFor(paint.Color{R: 0x1c, G: 0x1c, B: 0x1e, A: 255}, 1, false)
	pale := toneLUTFor(paint.Color{R: 0xf2, G: 0xf2, B: 0xf7, A: 255}, 1, false)
	for name, lut := range map[string]*toneLUT{"dark": dark, "pale": pale} {
		if lut[0] != 0 || lut[255] != 255 {
			t.Fatalf("%s ink: the paper is %d and the ink %d, want 0 and 255", name, lut[0], lut[255])
		}
		for i := 1; i < len(lut); i++ {
			if lut[i] < lut[i-1] {
				t.Fatalf("%s ink: coverage %d draws %d under %d's %d", name, i, lut[i], i-1, lut[i-1])
			}
		}
	}
	if d := int(dark[128]) - 128; d < 8 {
		t.Errorf("half coverage carries %d of 255, want at least 136", dark[128])
	}
	if pale[128] >= dark[128] {
		t.Errorf("a pale ink at half coverage is %d and a dark one %d: want the pale one lighter", pale[128], dark[128])
	}
	// A size band takes less of the gain: display type has stems of its own.
	big := toneLUTFor(paint.Color{A: 255}, sizeBand(30), false)
	if big[128] >= dark[128] {
		t.Errorf("display type at half coverage is %d and body type %d: want the display type lighter", big[128], dark[128])
	}
	// A run grown into a synthetic bold takes the contrast without the growth:
	// its strokes already carry more than their outline, and stacking the growth
	// on that closes a bold body's counters.
	grown := toneLUTFor(paint.Color{A: 255}, sizeBand(17), true)
	if grown[128] >= dark[128] {
		t.Errorf("a grown run at half coverage is %d and a plain one %d: want no growth on a grown run",
			grown[128], dark[128])
	}
	if grown[128] <= 128 {
		t.Errorf("a grown run at half coverage is %d: want the contrast kept", grown[128])
	}
}

// TestFakeBoldGrowsTheRun holds a run asking for a weight its face does not have
// to being drawn heavier when it is grown, and to the plain face when the
// growth is turned off: the book asks for bold, and the face it matched is the
// regular.
func TestFakeBoldGrowsTheRun(t *testing.T) {
	l, up, _ := benchScrollColumn(t)
	useAtlas(t, "1")
	const scrollY float32 = 4000
	fakeBoldOff = false
	t.Cleanup(func() { fakeBoldOff = false })
	grown := inkMass(paintViewport(l, up, 1, scrollY))
	fakeBoldOff = true
	plain := inkMass(paintViewport(l, up, 1, scrollY))
	if grown <= plain {
		t.Fatalf("the grown run carried %d of the %d ink the plain one did", grown, plain)
	}
}

// TestGlyphCoverageIgnoresWhatCameBefore holds a grown glyph to being the union
// of its own outline and nothing else. The buffers a glyph is rasterized into
// are reused from one glyph to the next, so a union that starts from the buffer
// rather than from nothing grows a bold glyph by whatever was rasterized last —
// which drew a heading as a smear of its neighbours, and only for the runs that
// asked for a weight their face did not have.
func TestGlyphCoverageIgnoresWhatCameBefore(t *testing.T) {
	l, up, _ := benchScrollColumn(t)
	useAtlas(t, "1")
	fakeBoldOff = false
	t.Cleanup(func() { fakeBoldOff = false })
	paintViewport(l, up, 1, 0)

	var gf *glyphFace
	for _, f := range up.faces {
		gf = f
		break
	}
	if gf == nil {
		t.Fatal("the page drew no face")
	}
	// A large glyph and a small one, both of them the book's own.
	out := gf.outline('g')
	if out == nil || len(out.segs) == 0 {
		t.Skip("the face has no g to draw")
	}
	big := gf.outline('W')
	if big == nil || len(big.segs) == 0 {
		t.Skip("the face has no W to draw")
	}
	const w, h = 32, 32
	const spread = 0.5
	upem := float64(gf.face.Upem())
	if upem <= 0 {
		t.Skip("the face has no units per em")
	}
	bigS, smallS := 34/upem, 17/upem // device pixels per font unit
	// The buffer the grown glyph is rasterized into keeps the large glyph's ink.
	dirty := &glyphAtlas{index: make(map[glyphKey]*glyphSlot)}
	dirty.renderCoverage(w, h, big, bigS, 4, 4, 0, 0, nil, 0)
	got := append([]uint8(nil), dirty.renderCoverage(w, h, out, smallS, 4, 4, 0, 0, nil, spread)...)

	fresh := &glyphAtlas{index: make(map[glyphKey]*glyphSlot)}
	want := fresh.renderCoverage(w, h, out, smallS, 4, 4, 0, 0, nil, spread)
	if !slices.Equal(got, want) {
		sum := 0
		for i := range got {
			if got[i] != want[i] {
				sum++
			}
		}
		t.Fatalf("a glyph rasterized after another differs in %d of %d pixels from one drawn first", sum, len(got))
	}
	_ = l
}
