package reader

import (
	"context"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laix/layx-reader/internal/book"
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
