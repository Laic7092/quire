package reader

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/laix/layx-reader/internal/book"
	"github.com/laix/layx/render"
)

// scrollBook builds a book long enough to scroll: many paragraphs over two
// chapters, anchored per chapter like the converter does.
func scrollBook(t *testing.T, chapters int) *book.Book {
	t.Helper()
	list := make([]book.Chapter, chapters)
	for ci := 0; ci < chapters; ci++ {
		var b strings.Builder
		b.WriteString("<html><body><h1>Chapter ")
		b.WriteString(string(rune('A' + ci)))
		b.WriteString("</h1>")
		for i := 0; i < 24; i++ {
			b.WriteString("<p>The quick brown fox jumps over the lazy dog, twice and again, ")
			b.WriteString("so that the chapter runs well past a single viewport of the column. ")
			b.WriteString("这一段用来把章节撑长，确保滚动模式下有多个 tile。</p>")
		}
		b.WriteString("</body></html>")
		list[ci] = book.Chapter{
			Title: "Chapter " + string(rune('A'+ci)),
			XHTML: []byte(b.String()),
		}
	}
	return &book.Book{Title: "scroll", Chapters: list}
}

// A scroll layout exposes a continuous flow with tiles instead of pages: the
// caller can measure it, draw the tiles and map content y to locations.
func TestBuildLayoutScrolls(t *testing.T) {
	_, fonts := testBookAndFont(t)
	b := scrollBook(t, 2)
	doc, err := b.DocumentSized(18)
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{FontSize: 18, Scroll: true}
	layout, err := BuildLayout(context.Background(), doc, fonts, 420, 560, opts)
	if err != nil {
		t.Fatal(err)
	}
	if n := layout.PageCount(); n != 0 {
		t.Fatalf("a scroll layout has %d pages", n)
	}
	height := layout.ContentHeight()
	if height <= 560 {
		t.Fatalf("content height = %.0f, want more than a viewport", height)
	}
	if n := layout.TileCount(); n < 2 {
		t.Fatalf("tile count = %d, want at least 2", n)
	}
	first := layout.Tile(0)
	if first == nil {
		t.Fatal("tile 0 is nil")
	}
	if first.Height() != 560 {
		t.Errorf("tile height = %.0f, want the layout viewport's 560", first.Height())
	}
	rec := &recorder{}
	first.Draw(rec)
	if rec.glyphs == 0 {
		t.Errorf("tile 0 drew no glyphs (fills=%d)", rec.fills)
	}

	// The two chapters are stacked: the second starts where the first ends,
	// and the flow's y maps to a location and back.
	second, ok := layout.ChapterContentTop(1)
	if !ok || second <= 0 || second >= height {
		t.Fatalf("chapter 1 starts at %.0f of %.0f", second, height)
	}
	for _, y := range []float32{1, second, second + 40, height - 2} {
		loc, ok := layout.LocationAt(y)
		if !ok {
			t.Fatalf("LocationAt(%.0f) failed", y)
		}
		top, ok := layout.OffsetOf(loc)
		if !ok {
			t.Fatalf("OffsetOf(%+v) failed", loc)
		}
		// A y that came from the layout round-trips exactly; a probe takes the
		// containing line, whose top edge is at or above it unless the probe
		// fell before the first line of the book.
		back, ok := layout.LocationAt(top)
		if !ok || back.Chapter != loc.Chapter || back.Node != loc.Node || back.Offset != loc.Offset {
			t.Errorf("location at %.0f does not round-trip: %+v → %.0f → %+v", y, loc, top, back)
		}
		if p, ok := layout.Progress(loc); !ok || p <= 0 || p > 1 {
			t.Errorf("Progress at %.0f = %.2f, %v", y, p, ok)
		}
	}

	// A chapter anchor resolves to a location, which the table of contents and
	// the store rely on.
	if loc, ok := layout.Locate(render.AnchorID(2)); !ok {
		t.Error("chapter 2's anchor did not resolve")
	} else if page, ok := layout.PageOf(loc); ok || page != 0 {
		t.Errorf("PageOf on a scroll layout = %d, %v; want no page", page, ok)
	}

	// Selection rectangles carry content coordinates and no page.
	from, ok1 := layout.LocationAt(second + 10)
	to, ok2 := layout.LocationAt(second + 400)
	if !ok1 || !ok2 {
		t.Fatal("could not probe the second chapter")
	}
	quads := layout.Selection(from, to)
	if len(quads) == 0 {
		t.Fatal("the selection has no rectangles")
	}
	for _, q := range quads {
		if q.Page != -1 {
			t.Errorf("quad page = %d, want -1 in scroll mode", q.Page)
		}
		if q.Content.Empty() {
			t.Error("a quad has no content rectangle")
		}
	}
}

// Two layouts of the same document differ in mode but not in their text: the
// scroll option is part of what a relayout must see.
func TestScrollOptionsAreSameOnlyWhenEqual(t *testing.T) {
	paged := DefaultOptions()
	scroll := paged
	scroll.Scroll = true
	if paged.Same(scroll) {
		t.Error("paged and scroll options claim to need the same layout")
	}
	if !scroll.Same(scroll.Normalized()) {
		t.Error("normalized scroll options are not the same as themselves")
	}
}

// The scrolling column and the paged spread are the same type on the same paper:
// the column keeps the page's head inset (scrollInset in the reader is PageTop,
// and layx's scroll flow starts at content y=0), and from there it sets the same
// lines in the same places as page 0 does inside its own margins. The two part
// only where a page ends, which is the one thing the modes are for.
func TestScrollColumnStartsLikePageZero(t *testing.T) {
	bk, err := book.Open("../book/testdata/samples/aq-zhuan.epub")
	if err != nil {
		t.Skipf("no sample book: %v", err)
	}
	path, err := FindFont("")
	if err != nil {
		t.Skipf("no font: %v", err)
	}
	fonts, err := OpenFont(path)
	if err != nil {
		t.Fatal(err)
	}
	doc, _, err := bk.Build(DefaultFontSize)
	if err != nil {
		t.Fatal(err)
	}
	const w, h float32 = 420, 600
	opts := Options{FontSize: DefaultFontSize}
	paged, err := BuildLayout(context.Background(), doc, fonts, w, h, opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.Scroll = true
	scroll, err := BuildLayout(context.Background(), doc, fonts, w, h, opts)
	if err != nil {
		t.Fatal(err)
	}
	page := paged.Page(0)
	if page == nil || scroll.TileCount() == 0 {
		t.Fatal("the two modes did not both lay the book out")
	}
	// The surface is reused, so each shot is copied before the next draw. A page's
	// display list carries the head inset; a tile's does not — the column is drawn
	// PageTop lower (the reader's scrollInset) — so the two line up with the page
	// shifted down by PageTop.
	pr := NewPageRenderer()
	pageShot := pr.DrawPage(page, 1)
	pagePix := append([]byte(nil), pageShot.Pix...)
	stride := pageShot.Stride
	tilePix := append([]byte(nil), pr.DrawTile(scroll.Tile(0), 1).Pix...)

	paper := Current().Paper
	ink := func(pix []byte, y int) bool {
		row := pix[y*stride : (y+1)*stride]
		for i := 0; i+3 < len(row); i += 4 {
			if row[i] != paper.R || row[i+1] != paper.G || row[i+2] != paper.B {
				return true
			}
		}
		return false
	}
	lastInk := -1
	for y := int(PageTop); y < int(h); y++ {
		if ink(pagePix, y) {
			lastInk = y
		}
	}
	if lastInk < 0 {
		t.Fatal("page 0 carries no ink")
	}
	for y := int(PageTop); y <= lastInk; y++ {
		if !bytes.Equal(pagePix[y*stride:(y+1)*stride], tilePix[(y-PageTop)*stride:(y-PageTop+1)*stride]) {
			t.Fatalf("page 0 row %d does not match the column's row %d: the two modes set the text differently",
				y, y-PageTop)
		}
	}
	// Past the page's last line the column carries on with the lines the page
	// broke away from.
	below := 0
	for y := lastInk - PageTop + 1; y < int(h); y++ {
		if ink(tilePix, y) {
			below++
		}
	}
	if below == 0 && lastInk < int(h-PageBottom)-1 {
		t.Error("the column stops where the page does, so the two modes are one")
	}
}
