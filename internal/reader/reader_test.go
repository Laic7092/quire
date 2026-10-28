package reader

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laix/quire/internal/book"
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
	layout, err := BuildLayout(context.Background(), doc, fonts, 480, 640, Options{FontSize: 18})
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
	small, err := BuildLayout(context.Background(), docSmall, fonts, 420, 560, Options{FontSize: 16})
	if err != nil {
		t.Fatal(err)
	}
	large, err := BuildLayout(context.Background(), docLarge, fonts, 420, 560, Options{FontSize: 28})
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
	layout, err := BuildLayout(context.Background(), doc, fonts, 400, 560, Options{FontSize: 18})
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
	layout, err := BuildLayout(context.Background(), doc, fonts, 400, 400, Options{FontSize: 18})
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

// The painter's fade multiplies the alpha of what it draws, which is how one
// page dissolves into the next: half a fade of black over white is half black.
func TestUIPainterFade(t *testing.T) {
	const paper = 0xff
	shot := func(fade float32) uint8 {
		t.Helper()
		up := NewUIPainter(nil, nil)
		tt := ui.NewTester(func(c *ui.Context) {
			ui.Box(c).Fill().Background(ui.RGB(paper, paper, paper)).Children(func() {
				ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
					up.P = p
					up.SetFade(fade)
					up.FillRect(paint.Rect{X: r.X, Y: r.Y, Width: r.W, Height: r.H},
						paint.Color{A: 0xff})
				})
			})
		}, 20, 20)
		tt.Frame()
		cr, _, _, _ := tt.Image().At(10, 10).RGBA()
		return uint8(cr >> 8)
	}
	for _, tc := range []struct {
		fade float32
		want uint8
	}{{1, 0x00}, {0.5, 0x7f}, {0, paper}} {
		got := shot(tc.fade)
		if got != tc.want && got != tc.want+1 {
			t.Errorf("fade %g drew %#02x, want %#02x", tc.fade, got, tc.want)
		}
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
	layout, err := BuildLayout(context.Background(), doc, fonts, 420, 560, Options{FontSize: 18})
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

// The reading options are clamped to what the settings offer, and a change of
// any of them means the text has to be laid out again.
func TestOptionsNormalizeAndCompare(t *testing.T) {
	o := Options{FontSize: 200, LineHeight: 9, Font: "papyrus", Margin: 1000}.Normalized()
	if o.FontSize != MaxFontSize || o.LineHeight != MaxLeading || o.Margin != MaxMargin {
		t.Errorf("options not clamped: %+v", o)
	}
	if o.Font != FontSerif {
		t.Errorf("unknown face %q kept", o.Font)
	}
	if zero := (Options{}).Normalized(); zero.FontSize != DefaultFontSize ||
		zero.LineHeight != 1.18 || zero.Margin != Margin || zero.Font != FontSerif {
		t.Errorf("zero options = %+v, want the sizes of the defaults", zero)
	}

	base := DefaultOptions()
	for _, change := range []func(*Options){
		func(o *Options) { o.FontSize = 20 },
		func(o *Options) { o.LineHeight = 1.8 },
		func(o *Options) { o.Font = FontSans },
		func(o *Options) { o.Justify = false },
		func(o *Options) { o.Margin = 80 },
	} {
		other := base
		change(&other)
		if base.Same(other) {
			t.Errorf("%+v and %+v lay out the same", base, other)
		}
	}
	if !base.Same(DefaultOptions()) {
		t.Error("the defaults do not lay out the same as themselves")
	}

	// The turn changes nothing the layout does: the pages on screen already
	// hold both spreads the transition draws, so it never costs a rebuild.
	base.Turn = TurnFade
	if !base.Same(DefaultOptions()) {
		t.Error("the turn asks for a new layout")
	}
	if got := (Options{Turn: "swirl"}).Normalized().Turn; got != TurnSlide {
		t.Errorf("unknown turn %q kept, want the slide", got)
	}
}

// A system without a serif face sets both choices in the sans one instead of
// failing, and the theme names round-trip through their palettes.
func TestFontsAndThemes(t *testing.T) {
	path, err := FindFont("")
	if err != nil {
		t.Skipf("no system font: %v", err)
	}
	fonts, err := OpenFonts(path, "")
	if err != nil {
		t.Fatal(err)
	}
	// The bundled book face is always there, so the serif choice never falls
	// back to the sans one.
	if got := fonts.Family(FontSerif); got != "book-serif" {
		t.Errorf("serif family = %q, want the bundled book face", got)
	}
	if got := fonts.Family(FontSans); got != "book" {
		t.Errorf("sans family = %q", got)
	}
	if fonts.HasSerif() {
		t.Error("a set made without a serif path holds a system serif face")
	}
	// HasSerif reports the system's face alone: it is what tells the app to make
	// the set again when the reader picks the serif face.
	serif, err := OpenFonts(path, FindSerif(""))
	if err != nil {
		t.Fatal(err)
	}
	if got := serif.Family(FontSerif); got != "book-serif" {
		t.Errorf("serif family = %q, want the bundled book face", got)
	}
	if FindSerif("") == "" && serif.HasSerif() {
		t.Error("a system with no serif face reports one")
	}

	for _, theme := range ThemeOrder {
		p := ForTheme(theme)
		if p.Theme != theme {
			t.Errorf("%q palette says %q", theme, p.Theme)
		}
		if p.Paper.A == 0 || p.Ink.A == 0 || p.Desk.A == 0 || p.Chrome.A == 0 || p.Link.A == 0 {
			t.Errorf("%q palette has holes: %+v", theme, p)
		}
		if theme.Dark() != (theme == ThemeQuiet || theme == ThemeBold) {
			t.Errorf("%q darkness is wrong", theme)
		}
	}
	if System(false) != ThemeOriginal || System(true) != ThemeQuiet {
		t.Errorf("system papers: %q %q", System(false), System(true))
	}
}

// A face is parsed once per set: the sans font tagged serif as well would put a
// second parsed copy of it in the set, which for a system CJK face is the
// difference between sixty and a hundred megabytes of a freshly opened book.
func TestFontsHoldEachFaceOnce(t *testing.T) {
	path, err := FindFont("")
	if err != nil {
		t.Skipf("no system font: %v", err)
	}
	fonts, err := OpenFont(path)
	if err != nil {
		t.Fatal(err)
	}
	// Inter, the system's sans face and the bundled book face.
	if got := fonts.Set.FontCount(); got != 3 {
		t.Errorf("a set without a serif face holds %d faces, want 3", got)
	}
	if serif := FindSerif(""); serif != "" {
		both, err := OpenFonts(path, serif)
		if err != nil {
			t.Fatal(err)
		}
		if got := both.Set.FontCount(); got != 4 {
			t.Errorf("a set with a serif face holds %d faces, want 4", got)
		}
	}
}

// A fade that never arrived draws the page rather than nothing: an animation
// whose progress is not a number must not leave the book blank.
func TestUIPainterFadeNaNIsOpaque(t *testing.T) {
	const paper = 0xff
	testerShot := func(fade float32) uint8 {
		t.Helper()
		up := NewUIPainter(nil, nil)
		tt := ui.NewTester(func(c *ui.Context) {
			ui.Box(c).Fill().Background(ui.RGB(paper, paper, paper)).Children(func() {
				ui.Box(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
					up.P = p
					up.SetFade(fade)
					up.FillRect(paint.Rect{X: r.X, Y: r.Y, Width: r.W, Height: r.H}, paint.Color{A: 0xff})
				})
			})
		}, 20, 20)
		tt.Frame()
		cr, _, _, _ := tt.Image().At(10, 10).RGBA()
		return uint8(cr >> 8)
	}
	if got := testerShot(float32(math.NaN())); got != 0 {
		t.Errorf("a NaN fade drew %#02x, want the opaque ink: the page went blank", got)
	}
	if got := testerShot(2); got != 0 {
		t.Errorf("a fade past 1 drew %#02x, want the opaque ink", got)
	}
}

// An image id that is already decoded stays available: a host may hand it again,
// and it may have no bytes to hand by then (a rebuild that published no new
// image for it). Reporting it missing would make a page lose a picture it has.
func TestUIPainterSetImageKeepsADecodedID(t *testing.T) {
	var pic bytes.Buffer
	src := image.NewRGBA(image.Rect(0, 0, 2, 2))
	if err := png.Encode(&pic, src); err != nil {
		t.Fatal(err)
	}
	const id, other = render.ImageID(1), render.ImageID(2)
	up := NewUIPainter(nil, nil)
	if !up.SetImage(id, pic.Bytes()) {
		t.Fatal("the PNG did not decode")
	}
	if up.Bitmap(id) == nil {
		t.Fatal("the decoded bitmap is not there")
	}
	if !up.SetImage(id, nil) {
		t.Error("a decoded image reported missing when handed no bytes again")
	}
	if up.SetImage(other, nil) {
		t.Error("an image with no bytes decoded")
	}
	up.ClearImages()
	if up.Bitmap(id) != nil {
		t.Error("ClearImages kept a bitmap")
	}
	if up.SetImage(id, nil) {
		t.Error("a cleared image reports itself decoded without bytes")
	}
	if up.SetImage(id, pic.Bytes()) == false || up.Bitmap(id) == nil {
		t.Error("an image does not decode again after ClearImages")
	}
	var nilPainter *UIPainter
	if nilPainter.SetImage(id, pic.Bytes()) || nilPainter.Bitmap(id) != nil {
		t.Error("a nil painter holds an image")
	}
}
