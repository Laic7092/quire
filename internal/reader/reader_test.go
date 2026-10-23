package reader

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/book"
	"github.com/laix/layx/render"
	"github.com/laix/layx/render/paint"
)

func testBookAndFont(t *testing.T) (*book.Book, *Fonts) {
	t.Helper()
	b, err := book.Open("../book/testdata/mini.epub")
	if err != nil {
		t.Fatal(err)
	}
	path, err := FindFont("")
	if err != nil {
		t.Skipf("no font available: %v", err)
	}
	fonts, err := OpenFont(path)
	if err != nil {
		t.Fatal(err)
	}
	return b, fonts
}

func TestBuildLayoutAndDrawGlyphs(t *testing.T) {
	b, fonts := testBookAndFont(t)
	doc, err := b.Document()
	if err != nil {
		t.Fatal(err)
	}
	layout, err := BuildLayout(context.Background(), doc, fonts, 480, 640, 18)
	if err != nil {
		t.Fatal(err)
	}
	if layout.PageCount() <= 0 {
		t.Fatalf("page count = %d", layout.PageCount())
	}
	page := layout.Page(0)
	if page == nil {
		t.Fatal("page 0 is nil")
	}
	rec := &recorder{}
	page.Draw(rec)
	if rec.glyphs == 0 {
		t.Errorf("page 0 drew no glyphs (fills=%d)", rec.fills)
	}
	if rec.fills == 0 {
		t.Errorf("page 0 painted no background")
	}

	// The chapter anchors make page-to-chapter mapping exact.
	if got := ChapterOfPage(layout, len(b.Chapters), 0); got != 0 {
		t.Errorf("chapter of page 0 = %d, want 0", got)
	}
	last := layout.PageCount() - 1
	if got := ChapterOfPage(layout, len(b.Chapters), last); got != 1 {
		t.Errorf("chapter of page %d = %d, want 1", last, got)
	}
}

func TestFontSizeChangesPages(t *testing.T) {
	_, fonts := testBookAndFont(t)
	var body strings.Builder
	for i := 0; i < 40; i++ {
		body.WriteString("<p>这一段用来把书撑到很多页，检查字号改变之后分页也会改变。")
		body.WriteString("The quick brown fox jumps over the lazy dog.</p>")
	}
	b := &book.Book{Title: "长文", Chapters: []book.Chapter{{
		Title: "第一章",
		XHTML: []byte("<html><body>" + body.String() + "</body></html>"),
	}}}
	docSmall, err := b.DocumentSized(16)
	if err != nil {
		t.Fatal(err)
	}
	docLarge, err := b.DocumentSized(28)
	if err != nil {
		t.Fatal(err)
	}
	small, err := BuildLayout(context.Background(), docSmall, fonts, 420, 560, 16)
	if err != nil {
		t.Fatal(err)
	}
	large, err := BuildLayout(context.Background(), docLarge, fonts, 420, 560, 28)
	if err != nil {
		t.Fatal(err)
	}
	if large.PageCount() <= small.PageCount() {
		t.Errorf("28px gives %d pages, 16px gives %d; larger type must paginate more",
			large.PageCount(), small.PageCount())
	}
}

func TestUIPainterInTester(t *testing.T) {
	b, fonts := testBookAndFont(t)
	doc, err := b.Document()
	if err != nil {
		t.Fatal(err)
	}
	layout, err := BuildLayout(context.Background(), doc, fonts, 400, 560, 18)
	if err != nil {
		t.Fatal(err)
	}
	page := layout.Page(0)
	if page == nil {
		t.Fatal("no page 0")
	}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
			up := NewUIPainter(nil, fonts)
			up.DrawPage(p, page, r)
		})
	}, 400, 560)
	tt.Frame()
	tt.Frame()
	if tt.FramePending() {
		t.Log("the still page keeps asking for frames")
	}
}

// An image the engine placed must reach the native painter, and a page with
// one image only is enough to see it.
func TestUIPainterDrawsImages(t *testing.T) {
	_, fonts := testBookAndFont(t)

	var pic bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for x := 0; x < 4; x++ {
		for y := 0; y < 4; y++ {
			img.Set(x, y, color.RGBA{R: 0xff, A: 0xff})
		}
	}
	if err := png.Encode(&pic, img); err != nil {
		t.Fatal(err)
	}

	const imageID = render.ImageID(7)
	builder := render.NewDocumentBuilder().
		SetStyles([]render.Style{{}, {FontFamily: "book", FontSize: 18}}).
		SetMetadata(render.Metadata{Reflowable: true})
	err := builder.AddChapter(render.Chapter{
		ID:   "c",
		Root: 0,
		Nodes: []render.Node{
			{Kind: render.NodeBlock, Style: 1, Children: []render.NodeID{1}},
			{Kind: render.NodeImage, Image: imageID, Width: 40, Height: 40},
		},
		Anchors: map[render.AnchorID]render.NodeID{},
	})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	layout, err := BuildLayout(context.Background(), doc, fonts, 400, 400, 18)
	if err != nil {
		t.Fatal(err)
	}
	page := layout.Page(0)
	if page == nil {
		t.Fatal("no page 0")
	}

	up := NewUIPainter(nil, fonts)
	if !up.SetImage(imageID, pic.Bytes()) {
		t.Fatal("PNG did not decode")
	}
	if up.SetImage(imageID+1, []byte("not an image")) {
		t.Error("garbage decoded as an image")
	}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
			up.DrawPage(p, page, r)
		})
	}, 400, 400)
	tt.Frame()
	if !hasRed(tt.Image()) {
		t.Fatal("the image never reached the painter")
	}
}

func hasRed(m *image.RGBA) bool {
	b := m.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := m.At(x, y).RGBA()
			if r > 0x8000 && g < 0x3000 && bl < 0x3000 {
				return true
			}
		}
	}
	return false
}

// recorder implements paint.Painter and counts what a page emits.
type recorder struct {
	fills  int
	glyphs int
	runs   int
}

func (r *recorder) Save()                                                   {}
func (r *recorder) Restore()                                                {}
func (r *recorder) ClipRect(paint.Rect)                                     {}
func (r *recorder) Transform(paint.Affine)                                  {}
func (r *recorder) FillRect(paint.Rect, paint.Color)                        { r.fills++ }
func (r *recorder) StrokeRect(paint.Rect, paint.Color, float32)             {}
func (r *recorder) FillRoundRect(paint.Rect, float32, paint.Color)          { r.fills++ }
func (r *recorder) DrawImage(paint.ImageID, paint.Rect, paint.ImageOptions) {}
func (r *recorder) DrawGlyphs(run paint.GlyphRun) {
	r.runs++
	r.glyphs += len(run.Glyphs)
}

// A plain text book goes through exactly the same Chapter → Document → Layout
// path as an EPUB, which is the point of the host-side Book abstraction.
func TestTextBookPaginates(t *testing.T) {
	_, fonts := testBookAndFont(t)
	b, err := book.Open("../book/testdata/samples/aq-zhuan.txt")
	if err != nil {
		t.Fatal(err)
	}
	doc, images, err := b.Build(18)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 0 {
		t.Errorf("text book reported %d images", len(images))
	}
	layout, err := BuildLayout(context.Background(), doc, fonts, 420, 560, 18)
	if err != nil {
		t.Fatal(err)
	}
	if layout.PageCount() < 5 {
		t.Errorf("page count %d, want a real book", layout.PageCount())
	}
	if got := ChapterOfPage(layout, len(b.Chapters), 0); got != 0 {
		t.Errorf("chapter of page 0 = %d", got)
	}
	if got := ChapterOfPage(layout, len(b.Chapters), layout.PageCount()-1); got < 1 {
		t.Errorf("chapter of the last page = %d, want a later chapter", got)
	}
}
