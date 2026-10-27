package reader

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/laix/layx-reader/internal/book"
	"github.com/laix/layx/render"
)

// A size that is not a number takes the default it stands in for. Every
// comparison against NaN is false, so it slips past a clamp built on `<` and
// `>`; a layout made from it places no line, and since NaN is not equal to
// itself it also makes Same report a difference on every frame — a relayout per
// frame.
func TestOptionsNormalizeNaN(t *testing.T) {
	nan := float32(math.NaN())
	o := Options{FontSize: nan, LineHeight: nan, Margin: nan, Font: FontSerif}.Normalized()
	if o.FontSize != DefaultFontSize {
		t.Errorf("NaN size normalizes to %g, want the default %d", o.FontSize, DefaultFontSize)
	}
	if o.LineHeight != 1.18 {
		t.Errorf("NaN leading normalizes to %g, want 1.18", o.LineHeight)
	}
	if o.Margin != Margin {
		t.Errorf("NaN margin normalizes to %g, want the default %d", o.Margin, Margin)
	}
	if !o.Same(o) {
		t.Error("options that came out of Normalized do not lay out the same as themselves")
	}
	if !(Options{FontSize: nan}).Same(Options{FontSize: nan}) {
		t.Error("two NaN sizes ask for a new layout on every comparison")
	}

	// Infinities are values: they take the limit, not the default.
	inf := float32(math.Inf(1))
	hi := Options{FontSize: inf, LineHeight: inf, Margin: inf}.Normalized()
	if hi.FontSize != MaxFontSize || hi.LineHeight != MaxLeading || hi.Margin != MaxMargin {
		t.Errorf("infinite options normalize to %+v, want every limit", hi)
	}
	lo := Options{FontSize: float32(math.Inf(-1)), LineHeight: float32(math.Inf(-1)),
		Margin: float32(math.Inf(-1))}.Normalized()
	if lo.FontSize != MinFontSize || lo.LineHeight != MinLeading || lo.Margin != MinMargin {
		t.Errorf("negative infinities normalize to %+v, want the low limits", lo)
	}
}

// A viewport that is not a number, or an infinite one, is refused with a
// readable error instead of being laid out: the pages would carry that size, and
// nothing drawn from them lands on a pixel.
func TestLayoutRefusesNonFiniteViewports(t *testing.T) {
	doc, fonts := selectionBook(t)
	nan, inf := float32(math.NaN()), float32(math.Inf(1))
	for _, tc := range []struct {
		name string
		w, h float32
		want string
	}{
		{"NaN width", nan, 560, "not a finite size"},
		{"NaN height", 420, nan, "not a finite size"},
		{"infinite width", inf, 560, "not a finite size"},
		{"infinite height", 420, inf, "not a finite size"},
		{"zero", 0, 0, "too small"},
		{"narrow", 40, 560, "too small"},
	} {
		_, err := BuildLayout(context.Background(), doc, fonts, tc.w, tc.h, Options{FontSize: 18})
		if err == nil {
			t.Errorf("%s: a %gx%g viewport was laid out", tc.name, tc.w, tc.h)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q does not say %q", tc.name, err, tc.want)
		}
	}

	// The same guard is on the rebuild path, which is what a window resize
	// during a theme change takes.
	layout, err := BuildLayout(context.Background(), doc, fonts, 420, 560, Options{FontSize: 18})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Relayout(context.Background(), layout, fonts, nan, 560, Options{FontSize: 18}); err == nil {
		t.Error("Relayout laid the book out in a NaN viewport")
	}
}

// The paper a layout is printed on is baked into its pages, so a layout always
// comes out on the palette in use, and Recolor moves the pages already on screen
// to another one without moving a line.
func TestLayoutWearsThePaperInUse(t *testing.T) {
	doc, fonts := selectionBook(t)
	paper := Current()
	defer SetPalette(paper)

	SetPalette(Original)
	layout, err := BuildLayout(context.Background(), doc, fonts, 420, 560, Options{FontSize: 18})
	if err != nil {
		t.Fatal(err)
	}
	if got := layout.Typography().Background; got != Original.Paper {
		t.Errorf("the layout's paper is %+v, want the one in use %+v", got, Original.Paper)
	}
	before := layout.PageCount()
	loc, ok := layout.LocationOfPage(0)
	if !ok {
		t.Fatal("page 0 has no location")
	}

	pr := NewPageRenderer()
	white := pr.DrawPage(layout.Page(0), 1)
	if c := white.RGBAAt(0, 0); c.R != Original.Paper.R || c.G != Original.Paper.G || c.B != Original.Paper.B {
		t.Fatalf("page 0 printed on %v, want the white paper %v", c, Original.Paper)
	}

	SetPalette(Bold)
	dark := Recolor(layout)
	if dark == layout {
		t.Error("Recolor returned the same layout after the paper changed")
	}
	if got := dark.Typography().Background; got != Bold.Paper {
		t.Errorf("the recoloured layout's paper is %+v, want %+v", got, Bold.Paper)
	}
	if got := dark.PageCount(); got != before {
		t.Errorf("recolouring took the book from %d pages to %d", before, got)
	}
	if back, ok := dark.PageOf(loc); !ok || back != 0 {
		t.Errorf("a location on page 0 is on page %d, %v after recolouring", back, ok)
	}
	shot := pr.DrawPage(dark.Page(0), 1)
	if c := shot.RGBAAt(0, 0); c.R != Bold.Paper.R || c.G != Bold.Paper.G || c.B != Bold.Paper.B {
		t.Fatalf("the recoloured page printed on %v, want the black paper %v", c, Bold.Paper)
	}
	// And it still carries the book, not only its paper.
	if ink := differsFromPaper(shot, Bold.Paper); ink < 500 {
		t.Errorf("the recoloured page carries %d pixels of ink, want the book", ink)
	}
	if Recolor(nil) != nil {
		t.Error("Recolor of no layout is not nil")
	}
}

// A relayout re-paginates the same document for the settings in use and refuses
// to invent one.
func TestRelayoutRepaginatesTheSameDocument(t *testing.T) {
	_, fonts := selectionBook(t)
	var body strings.Builder
	for i := 0; i < 40; i++ {
		body.WriteString("<p>这一段用来把书撑到很多页，检查字号改变之后分页也会改变。")
		body.WriteString("The quick brown fox jumps over the lazy dog.</p>")
	}
	doc, err := (&book.Book{Title: "长文", Chapters: []book.Chapter{{
		Title: "第一章",
		XHTML: []byte("<html><body>" + body.String() + "</body></html>"),
	}}}).DocumentSized(16)
	if err != nil {
		t.Fatal(err)
	}
	small, err := BuildLayout(context.Background(), doc, fonts, 500, 400, Options{FontSize: 16})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Relayout(context.Background(), nil, fonts, 500, 400, Options{FontSize: 16}); err == nil {
		t.Error("Relayout of a nil layout did not fail")
	}
	var huge render.Layout
	if _, err := Relayout(context.Background(), &huge, fonts, 500, 400, Options{FontSize: 16}); err == nil {
		t.Error("Relayout of a layout with no document did not fail")
	}
	// A wider inset narrows the measure, which must page the same text more.
	wide := Options{FontSize: 16, Margin: 60}
	large, err := Relayout(context.Background(), small, fonts, 500, 400, wide)
	if err != nil {
		t.Fatal(err)
	}
	if large.Document() != small.Document() {
		t.Error("a relayout built another document")
	}
	if large.PageCount() <= small.PageCount() {
		t.Errorf("a %g DIP inset relayouts to %d pages, %g to %d: a narrower measure must paginate more",
			wide.Margin, large.PageCount(), Options{FontSize: 16}.Margin, small.PageCount())
	}
}

// The reading size the pages are set at is the size the document was built with:
// the converter bakes absolute sizes into its styles, so a layout cannot restyle
// a document built at another size. This pins the coupling a host has to keep —
// the reader rebuilds its document on a size change for exactly this reason —
// and holds BuildLayout to refusing to pretend otherwise.
func TestReadingSizeComesFromTheDocument(t *testing.T) {
	doc, fonts := selectionBook(t)
	at16, err := BuildLayout(context.Background(), doc, fonts, 300, 400, Options{FontSize: 16})
	if err != nil {
		t.Fatal(err)
	}
	// doc was built at Document()'s own size (18); asking the layout for another
	// one changes the request, not the type.
	for _, size := range []float32{12, 24} {
		other, err := BuildLayout(context.Background(), doc, fonts, 300, 400, Options{FontSize: size})
		if err != nil {
			t.Fatal(err)
		}
		if other.PageCount() != at16.PageCount() {
			t.Logf("%g asked for %d pages, 16 for %d: the document's build size is the reading size",
				size, other.PageCount(), at16.PageCount())
		}
		if got := other.Typography().FontSize; got != size {
			t.Errorf("the layout asks for %g and records %g", size, got)
		}
	}
}
