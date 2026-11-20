package reader

import (
	"image"
	"math"
	"os"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laic7092/layx/render"
	"github.com/laic7092/layx/render/paint"
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

// BenchmarkGlyphRasterizer measures the coverage rasterizer on the glyph
// outlines the atlas draws, at the place it would rasterize them (17 DIP at DPR
// 2). It is the cost a cold atlas pays; a warm frame is a lookup instead.
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

// The atlas keeps its memory under its budget by letting go of whole layers, the
// least recently used first, and it must let go of the keys too: an index entry
// whose layer is gone is a slot pointing at a bitmap nothing owns any more.
func TestGlyphAtlasEvictsTheLeastRecentlyUsed(t *testing.T) {
	perLayer := glyphLayerW * glyphLayerH * 4
	want := glyphAtlasBudget / perLayer // the layers the budget holds
	if want < 2 {
		t.Fatalf("the budget holds %d layers, too few to evict", want)
	}
	a := &glyphAtlas{index: make(map[glyphKey]*glyphSlot)}
	opened := 0
	add := func() *glyphLayer {
		l := a.newLayer()
		opened++
		key := glyphKey{gid: uint32(opened)}
		l.keys = append(l.keys, key)
		a.index[key] = &glyphSlot{layer: l}
		return l
	}
	var layers []*glyphLayer
	for i := 0; i < want; i++ {
		layers = append(layers, add())
	}
	if len(a.layers) != want || a.bytes != want*perLayer {
		t.Fatalf("%d layers, %d bytes; want %d and %d", len(a.layers), a.bytes, want, want*perLayer)
	}
	// Use the oldest layer, as a frame that draws a glyph from it would: it is
	// no longer the next to go.
	a.touch(layers[0])
	for i := 0; i < 2; i++ {
		add()
	}
	if len(a.layers) != want {
		t.Fatalf("%d layers after opening two more, want the budget's %d", len(a.layers), want)
	}
	if a.bytes != want*perLayer {
		t.Errorf("%d bytes held, want %d", a.bytes, want*perLayer)
	}
	if !slices.Contains(a.layers, layers[0]) {
		t.Error("the layer used last was let go while older ones stayed")
	}
	if slices.Contains(a.layers, layers[1]) || slices.Contains(a.layers, layers[2]) {
		t.Error("the two least recently used layers were kept")
	}
	if got := len(a.index); got != want {
		t.Errorf("the index holds %d glyphs for %d layers, want one each: an evicted layer must drop its keys", got, want)
	}
	for key, slot := range a.index {
		if slot == nil {
			t.Errorf("%+v is indexed without a slot", key)
			continue
		}
		if !slices.Contains(a.layers, slot.layer) {
			t.Errorf("%+v points at a layer the atlas no longer holds", key)
		}
	}
	// The layer being filled is never the one let go, so a glyph can never evict
	// the neighbour it was packed with.
	if n := len(a.layers); n > 0 && a.layers[n-1] == nil {
		t.Error("the layer being filled was let go")
	}
}

// A glyph looked at and found to have no ink is a hit of the atlas like any
// other: it needed no rasterizing the second time, which is what HitRate says.
func TestGlyphAtlasCountsBlankHits(t *testing.T) {
	path, err := FindFont("")
	if err != nil {
		t.Skipf("no font available: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	up := NewUIPainter(nil, nil)
	gf := up.glyphFace(&paint.Font{Data: data})
	if gf == nil {
		t.Fatal("the face did not parse")
	}
	upem := float32(gf.face.Upem())
	a := up.atlas()
	var stats GlyphStats
	a.stats = &stats
	// A space is a glyph with nothing to draw: the atlas keeps that answer as a
	// blank slot, one per phase, so it never asks the outline again.
	gid, ok := gf.face.NominalGlyph(' ')
	if !ok {
		t.Skip("the face has no space")
	}
	for i := 0; i < 3; i++ {
		slot, _, _ := a.slot(gf, uint32(gid), 17, 2, 10.25, 20.5, upem, nil, false)
		if slot != nil {
			t.Skipf("a space has ink in this face: %+v", slot)
		}
	}
	if stats.Lookups != 3 || stats.Hits != 2 || stats.Rasters != 0 {
		t.Errorf("lookups %d hits %d rasters %d, want 3, 2 and 0", stats.Lookups, stats.Hits, stats.Rasters)
	}
	if got := stats.HitRate(); math.Abs(got-2.0/3) > 1e-9 {
		t.Errorf("hit rate %.3f, want %.3f", got, 2.0/3)
	}
}
