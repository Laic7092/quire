package reader

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laic7092/layx/render"
	"github.com/laic7092/layx/render/paint"
	"github.com/laix/quire/internal/book"
)

func differsFromPaper(img *image.RGBA, paper paint.Color) int {
	n := 0
	for i := 0; i+3 < len(img.Pix); i += 4 {
		if img.Pix[i] != paper.R || img.Pix[i+1] != paper.G || img.Pix[i+2] != paper.B {
			n++
		}
	}
	return n
}

func TestPageRendererDrawsPage(t *testing.T) {
	b, fonts := testBookAndFont(t)
	doc, err := b.Document()
	if err != nil {
		t.Fatal(err)
	}
	layout, err := BuildLayout(context.Background(), doc, fonts, 480, 640, Options{FontSize: 18})
	if err != nil {
		t.Fatal(err)
	}
	page := layout.Page(0)
	if page == nil {
		t.Fatal("page 0 is nil")
	}

	pr := NewPageRenderer()
	paper := Current().Paper
	img := pr.DrawPage(page, 1)
	if got, want := img.Bounds().Dx(), int(page.Size().Width); got != want {
		t.Fatalf("width = %d, want %d", got, want)
	}
	if got, want := img.Bounds().Dy(), int(page.Size().Height); got != want {
		t.Fatalf("height = %d, want %d", got, want)
	}
	if c := img.RGBAAt(0, 0); c.R != paper.R || c.G != paper.G || c.B != paper.B {
		t.Fatalf("paper = %v, want %v", c, paper)
	}
	ink1 := differsFromPaper(img, paper)
	if ink1 < 500 {
		t.Fatalf("page covered %d pixels of ink, want a readable page", ink1)
	}

	// The surface is reused, so copy the pixels before the next draw: the same
	// page must come out identical.
	first := append([]byte(nil), img.Pix...)
	again := pr.DrawPage(page, 1)
	if !bytes.Equal(first, again.Pix) {
		t.Fatal("drawing the same page twice produced different pixels")
	}

	hi := pr.DrawPage(page, 2)
	if got, want := hi.Bounds().Dx(), 2*int(page.Size().Width); got != want {
		t.Fatalf("2x width = %d, want %d", got, want)
	}
	if got, want := hi.Bounds().Dy(), 2*int(page.Size().Height); got != want {
		t.Fatalf("2x height = %d, want %d", got, want)
	}
	ink2 := differsFromPaper(hi, paper)
	if ink2 <= ink1 {
		t.Fatalf("2x page covered %d pixels, 1x covered %d", ink2, ink1)
	}
}

func TestPageRendererDrawsTile(t *testing.T) {
	b, fonts := testBookAndFont(t)
	doc, err := b.Document()
	if err != nil {
		t.Fatal(err)
	}
	layout, err := BuildLayout(context.Background(), doc, fonts, 480, 640, Options{FontSize: 18, Scroll: true})
	if err != nil {
		t.Fatal(err)
	}
	if layout.TileCount() == 0 {
		t.Fatal("scroll layout has no tiles")
	}
	tile := layout.Tile(0)
	if tile == nil {
		t.Fatal("tile 0 is nil")
	}

	pr := NewPageRenderer()
	img := pr.DrawTile(tile, 1)
	if got, want := img.Bounds().Dx(), int(tile.Size().Width); got != want {
		t.Fatalf("width = %d, want %d", got, want)
	}
	if got, want := img.Bounds().Dy(), int(tile.Size().Height); got != want {
		t.Fatalf("height = %d, want %d", got, want)
	}
	if ink := differsFromPaper(img, Current().Paper); ink < 500 {
		t.Fatalf("tile covered %d pixels of ink, want a readable tile", ink)
	}
}

func TestPageRendererDrawsRegisteredImage(t *testing.T) {
	_, fonts := testBookAndFont(t)

	var pic bytes.Buffer
	src := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for x := 0; x < 4; x++ {
		for y := 0; y < 4; y++ {
			src.Set(x, y, color.RGBA{R: 0xff, A: 0xff})
		}
	}
	if err := png.Encode(&pic, src); err != nil {
		t.Fatal(err)
	}

	const imageID = render.ImageID(7)
	builder := render.NewDocumentBuilder().
		SetStyles([]render.Style{{}, {FontFamily: "book", FontSize: 18}}).
		SetMetadata(render.Metadata{Reflowable: true})
	if err := builder.AddChapter(render.Chapter{
		ID:   "c",
		Root: 0,
		Nodes: []render.Node{
			{Kind: render.NodeBlock, Style: 1, Children: []render.NodeID{1}},
			{Kind: render.NodeImage, Image: imageID, Width: 40, Height: 40},
		},
	}); err != nil {
		t.Fatal(err)
	}
	doc, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	layout, err := BuildLayout(context.Background(), doc, fonts, 200, 200, Options{FontSize: 18})
	if err != nil {
		t.Fatal(err)
	}
	page := layout.Page(0)
	if page == nil {
		t.Fatal("page 0 is nil")
	}

	pr := NewPageRenderer()
	if !pr.SetImage(imageID, pic.Bytes()) {
		t.Fatal("PNG did not decode")
	}
	if pr.SetImage(imageID+1, []byte("not an image")) {
		t.Error("garbage decoded as an image")
	}
	img := pr.DrawPage(page, 1)

	found := false
	for i := 0; i+3 < len(img.Pix); i += 4 {
		if img.Pix[i] > 0xc0 && img.Pix[i+1] < 0x40 && img.Pix[i+2] < 0x40 {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("the registered image never reached the page")
	}
}

func TestDecodeImageRejectsGarbage(t *testing.T) {
	if _, err := DecodeImage([]byte("not an image")); err == nil {
		t.Fatal("garbage decoded as an image")
	}
}

// BenchmarkPageRender measures one page of a real book through the headless
// renderer at DPR 2: the whole display list, glyphs rasterized from their
// outlines. It is the cost of a page turn in an offscreen render, the same work
// the window does once for a page's glyph atlas.
func BenchmarkPageRender(b *testing.B) {
	bk, err := book.Open("../book/testdata/samples/aq-zhuan.epub")
	if err != nil {
		b.Skip(err)
	}
	fontPath, err := FindFont("")
	if err != nil {
		b.Skip(err)
	}
	fonts, err := OpenFont(fontPath)
	if err != nil {
		b.Skip(err)
	}
	doc, _, err := bk.Build(17)
	if err != nil {
		b.Fatal(err)
	}
	l, err := BuildLayout(context.Background(), doc, fonts, 420, 600, Options{FontSize: 17})
	if err != nil {
		b.Fatal(err)
	}
	page := l.Page(2)
	if page == nil {
		b.Skip("no page")
	}

	pr := NewPageRenderer()
	pr.DrawPage(page, 2) // size the surface and warm the glyph faces
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		pr.DrawPage(page, 2)
	}
}

// TestPageRendererPrintsTheSameWeightAsTheScreen holds the two paths to the same
// ink. The window draws through the glyph atlas and the headless renderer through
// DrawGlyphs; both now get their correction from raster.Tone, so the same page
// exported to a PNG must be about as heavy as the page in the window — before the
// correction moved into the rasterizer, the PNG was 12% lighter.
func TestPageRendererPrintsTheSameWeightAsTheScreen(t *testing.T) {
	bk, err := book.Open("../book/testdata/samples/aq-zhuan.epub")
	if err != nil {
		t.Skip(err)
	}
	fontPath, err := FindFont("")
	if err != nil {
		t.Skip(err)
	}
	fonts, err := OpenFont(fontPath)
	if err != nil {
		t.Skip(err)
	}
	doc, _, err := bk.Build(17)
	if err != nil {
		t.Fatal(err)
	}
	const w, h = 420, 600
	l, err := BuildLayout(context.Background(), doc, fonts, w, h, Options{FontSize: 17})
	if err != nil {
		t.Fatal(err)
	}
	page := l.Page(5)
	if page == nil {
		t.Skip("no page")
	}

	useAtlas(t, "1")
	pr := NewPageRenderer()
	img := pr.DrawPage(page, 1)
	paper := paint.Color{R: img.Pix[0], G: img.Pix[1], B: img.Pix[2], A: 0xff}
	headless := inkOver(img, paper)

	up := NewUIPainter(nil, nil)
	shot := ui.Render(func(c *ui.Context) {
		ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
			up.DrawPage(p, page, r)
			up.FlushGlyphs()
		})
	}, w, h, 1)
	screen := inkOver(shot, paper)

	if screen == 0 || headless == 0 {
		t.Fatal("one of the two paths drew no ink")
	}
	if ratio := float64(headless) / float64(screen); ratio < 0.95 || ratio > 1.05 {
		t.Fatalf("headless page carries %d of ink to the screen's %d (%.3fx): the two paths must print the same weight",
			headless, screen, ratio)
	}

	// And the correction is what the weight is: with it off, the same page is
	// lighter by about the growth it removes.
	textToneOff = true
	t.Cleanup(func() { textToneOff = false })
	raw := inkOver(pr.DrawPage(page, 1), paper)
	if ratio := float64(headless) / float64(raw); ratio < 1.05 || ratio > 1.30 {
		t.Fatalf("the correction draws %.3fx the raw ink, want a visible but not a blotchy gain", ratio)
	}
}

// inkOver is how dark an image is over its paper: the sum of the ink the page
// carries, which is what a change of text weight moves.
func inkOver(img *image.RGBA, paper paint.Color) int64 {
	var sum int64
	for i := 0; i+3 < len(img.Pix); i += 4 {
		sum += int64(paper.R) - int64(img.Pix[i])
	}
	return sum
}

// imagePage builds a one-image page, the shape a cover or a figure has.
func imagePage(t *testing.T, fonts *Fonts) (*render.Page, render.ImageID, []byte) {
	t.Helper()
	var pic bytes.Buffer
	src := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for x := 0; x < 4; x++ {
		for y := 0; y < 4; y++ {
			src.Set(x, y, color.RGBA{R: 0xff, A: 0xff})
		}
	}
	if err := png.Encode(&pic, src); err != nil {
		t.Fatal(err)
	}
	const imageID = render.ImageID(11)
	builder := render.NewDocumentBuilder().
		SetStyles([]render.Style{{}, {FontFamily: "book", FontSize: 18}}).
		SetMetadata(render.Metadata{Reflowable: true})
	if err := builder.AddChapter(render.Chapter{
		ID:   "c",
		Root: 0,
		Nodes: []render.Node{
			{Kind: render.NodeBlock, Style: 1, Children: []render.NodeID{1}},
			{Kind: render.NodeImage, Image: imageID, Width: 40, Height: 40},
		},
	}); err != nil {
		t.Fatal(err)
	}
	doc, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	layout, err := BuildLayout(context.Background(), doc, fonts, 200, 200, Options{FontSize: 18})
	if err != nil {
		t.Fatal(err)
	}
	page := layout.Page(0)
	if page == nil {
		t.Fatal("no page 0")
	}
	return page, imageID, pic.Bytes()
}

func hasRedPixels(img *image.RGBA) bool {
	for i := 0; i+3 < len(img.Pix); i += 4 {
		if img.Pix[i] > 0xc0 && img.Pix[i+1] < 0x40 && img.Pix[i+2] < 0x40 {
			return true
		}
	}
	return false
}

// ClearImages drops the decoded images and the surface that held them: the next
// draw of a page whose image is gone paints the page without it, and a
// re-registered image is back.
func TestPageRendererClearImages(t *testing.T) {
	_, fonts := testBookAndFont(t)
	page, imageID, data := imagePage(t, fonts)
	pr := NewPageRenderer()
	if !pr.SetImage(imageID, data) {
		t.Fatal("the PNG did not decode")
	}
	if !hasRedPixels(pr.DrawPage(page, 1)) {
		t.Fatal("the registered image never reached the page")
	}
	pr.ClearImages()
	if hasRedPixels(pr.DrawPage(page, 1)) {
		t.Error("an image survived ClearImages")
	}
	if !pr.SetImage(imageID, data) {
		t.Fatal("the PNG did not decode again")
	}
	if !hasRedPixels(pr.DrawPage(page, 1)) {
		t.Error("a re-registered image never reached the page")
	}
	// A later page of another book reuses the ids: nothing of the first is left.
	pr.ClearImages()
	if pr.SetImage(imageID, []byte("not an image")) {
		t.Error("garbage decoded after ClearImages")
	}
	if hasRedPixels(pr.DrawPage(page, 1)) {
		t.Error("garbage kept the old image")
	}
}

// The device ratio an export asks for is a real number: one that is not, or one
// that is not positive, is drawn at 1 rather than as an empty image.
func TestPageRendererScaleIsSane(t *testing.T) {
	_, fonts := testBookAndFont(t)
	page, _, _ := imagePage(t, fonts)
	pr := NewPageRenderer()
	one := pr.DrawPage(page, 1)
	if one.Bounds().Dx() == 0 || one.Bounds().Dy() == 0 {
		t.Fatal("the 1x page is empty")
	}
	for name, scale := range map[string]float32{
		"zero": 0, "negative": -2, "NaN": float32(math.NaN()), "infinite": float32(math.Inf(1)),
	} {
		img := pr.DrawPage(page, scale)
		if got, want := img.Bounds(), one.Bounds(); got != want {
			t.Errorf("a %s scale drew %v, want the 1x page %v", name, got, want)
		}
	}
}

// A tile keeps a strip below itself for the line that overhangs it, and that
// strip lies over the top of the tile beneath. It must carry the overhanging ink
// and nothing else: cleared to paper it would paint the tile below's first line
// out, and the text would come out cut in half at the boundary.
func TestTilePadDoesNotCoverTheTileBelow(t *testing.T) {
	l, _, _ := benchScrollColumn(t)
	if l.TileCount() < 2 {
		t.Skip("the sample book has one tile")
	}
	upper, lower := l.Tile(0), l.Tile(1)
	if upper == nil || lower == nil {
		t.Fatal("the column has no second tile")
	}
	const pad float32 = 60
	pr := NewPageRenderer()
	copyOf := func(img *image.RGBA) *image.RGBA {
		out := image.NewRGBA(img.Rect)
		copy(out.Pix, img.Pix)
		return out
	}
	low := copyOf(pr.DrawTile(lower, 1))
	up := copyOf(pr.DrawTileSpan(upper, 1, 0, pad))

	paper := Current().Paper
	dst := image.NewRGBA(low.Bounds())
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.RGBA{R: paper.R, G: paper.G, B: paper.B, A: 0xff}),
		image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), low, image.Point{}, draw.Over)

	band := int(pad)
	before := 0
	for y := 2; y < band; y++ {
		for x := 0; x < low.Bounds().Dx(); x++ {
			p := low.RGBAAt(x, y)
			if p.R != paper.R || p.G != paper.G || p.B != paper.B {
				before++
			}
		}
	}
	// The upper tile's strip is the part below its own bottom edge.
	draw.Draw(dst, dst.Bounds(), up, image.Pt(0, int(math.Ceil(float64(upper.Height())))), draw.Over)
	after := 0
	for y := 2; y < band; y++ {
		for x := 0; x < dst.Bounds().Dx(); x++ {
			p := dst.RGBAAt(x, y)
			if p.R != paper.R || p.G != paper.G || p.B != paper.B {
				after++
			}
		}
	}
	if before == 0 {
		t.Skip("no line starts just under the boundary to check")
	}
	if after < before {
		t.Errorf("the strip below the tile covered the tile below: %d of %d ink pixels left under the boundary",
			after, before)
	}
}

// The rasterizer fakes bold by stamping a second copy of the run to the right;
// the offscreen path shifts the run back by half that so the two copies sit
// either side of where the type belongs. A second copy a full step to the right
// is a ghost at 2x and 3x, which is what every glyph looked like before.
func TestRenderPainterCentresBold(t *testing.T) {
	rec := &runRecorder{p: NewPageRenderer()}
	rp := renderPainter{rec, rec.p, 1}

	rp.DrawGlyphs(paint.GlyphRun{X: 10, Size: 24, EmulateBold: true})
	if got, want := rec.run.X, float32(9.5); got != want {
		t.Errorf("bold run starts at %.2f, want %.2f", got, want)
	}
	if !rec.run.EmulateBold {
		t.Error("the bold flag was taken off the run, so it would not be bold")
	}
	if !rec.boldSeen {
		t.Error("the tone was not told the run is bold")
	}
	if rec.p.bold {
		t.Error("the renderer kept the bold flag after the run")
	}

	// A size that would shift by more than the cap is held to it.
	rp.DrawGlyphs(paint.GlyphRun{X: 10, Size: 240, EmulateBold: true})
	if got, want := rec.run.X, float32(9.25); got != want {
		t.Errorf("a large bold run starts at %.2f, want %.2f", got, want)
	}

	// A regular run is drawn where it was.
	rp.DrawGlyphs(paint.GlyphRun{X: 10, Size: 24})
	if got := rec.run.X; got != 10 {
		t.Errorf("a regular run moved to %.2f", got)
	}
	if rec.boldSeen {
		t.Error("a regular run was reported as bold")
	}
}

// runRecorder keeps the last run handed to it and whether the renderer said it
// was bold at the time.
type runRecorder struct {
	paint.Painter
	p        *PageRenderer
	run      paint.GlyphRun
	boldSeen bool
}

func (r *runRecorder) DrawGlyphs(run paint.GlyphRun) {
	r.run = run
	r.boldSeen = r.p.bold
}
