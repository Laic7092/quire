package store

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/laix/layx-reader/internal/book"
	"github.com/laix/layx-reader/internal/reader"
	"github.com/laix/layx/render"
)

// testFonts loads the system font the reader would use, or skips the test.
func testFonts(t *testing.T) *reader.Fonts {
	t.Helper()
	path, err := reader.FindFont("")
	if err != nil {
		t.Skipf("no font available: %v", err)
	}
	fonts, err := reader.OpenFont(path)
	if err != nil {
		t.Fatalf("OpenFont: %v", err)
	}
	return fonts
}

// testBook builds a synthetic multi-chapter book. Each chapter is long enough
// to span several pages at the test viewport. base is the font size the book is
// converted for: the converter bakes absolute font sizes into the node styles,
// so a font size change means rebuilding the Document, not relayouting it.
func testBook(t *testing.T, chapters int, base float32) *render.Document {
	t.Helper()
	list := make([]book.Chapter, chapters)
	for ci := 0; ci < chapters; ci++ {
		var b strings.Builder
		fmt.Fprintf(&b, "<html><body><h1>Chapter %d</h1>", ci+1)
		for i := 0; i < 10; i++ {
			fmt.Fprintf(&b, "<p>Chapter %d paragraph %d. "+
				"This sentence exists only to make the chapter long enough to paginate. "+
				"The quick brown fox jumps over the lazy dog.</p>", ci+1, i+1)
		}
		b.WriteString("</body></html>")
		list[ci] = book.Chapter{Title: fmt.Sprintf("Chapter %d", ci+1), XHTML: []byte(b.String())}
	}
	doc, _, err := (&book.Book{Title: "probe", Chapters: list}).Build(base)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return doc
}

func testLayout(t *testing.T, doc *render.Document, fonts *reader.Fonts, size float32) *render.Layout {
	t.Helper()
	l, err := reader.BuildLayout(context.Background(), doc, fonts, 480, 640, reader.Options{FontSize: size})
	if err != nil {
		t.Fatalf("BuildLayout: %v", err)
	}
	return l
}

// TestPageForNodeRoundTrip checks the exact path: a position recorded from a
// page resolves back to that page while the layout is unchanged.
func TestPageForNodeRoundTrip(t *testing.T) {
	fonts := testFonts(t)
	l := testLayout(t, testBook(t, 3, 18), fonts, 18)
	if l.PageCount() < 6 {
		t.Fatalf("page count = %d, the synthetic book is too short", l.PageCount())
	}
	for _, page := range []int{0, 1, l.PageCount() / 2, l.PageCount() - 1} {
		p, ok := PositionFor(l, page)
		if !ok {
			t.Fatalf("PositionFor(page %d) failed", page)
		}
		if p.Node == 0 {
			t.Errorf("page %d position has no node: %+v", page, p)
		}
		got, how := PageFor(l, p)
		if got != page || how != "node" {
			t.Errorf("PageFor(page %d) = (%d, %q), want (%d, node)", page, got, how, page)
		}
	}
}

// TestPageForNodeSurvivesReflow records a position at one font size and
// restores it after a full rebuild at another. The node path keeps working
// because this builder numbers nodes from the source, not from the layout.
func TestPageForNodeSurvivesReflow(t *testing.T) {
	fonts := testFonts(t)
	small := testLayout(t, testBook(t, 3, 18), fonts, 18)
	large := testLayout(t, testBook(t, 3, 30), fonts, 30)

	page := small.PageCount() / 2
	p, ok := PositionFor(small, page)
	if !ok {
		t.Fatalf("PositionFor(%d) failed", page)
	}
	got, how := PageFor(large, p)
	if how != "node" {
		t.Fatalf("after reflow the node path did not fire: (%d, %q)", got, how)
	}
	want := int(p.Progress * float32(large.PageCount()))
	if diff := abs(got - want); diff > 1 {
		t.Errorf("restored page %d, proportional estimate %d (off by %d)", got, want, diff)
	}

	// The recorded position still points at the same line: the layout pages it
	// to the same page it claims, and the top of that page is at or before it.
	loc := render.Location{Chapter: p.Chapter, Node: render.NodeID(p.Node), Offset: p.Offset}
	if back, ok := large.PageOf(loc); !ok || back != got {
		t.Errorf("PageOf = (%d, %v), PageFor = %d", back, ok, got)
	}
	if top, ok := PositionFor(large, got); ok {
		if top.Chapter != p.Chapter || top.Node > p.Node {
			t.Errorf("page top %+v is past the recorded position %+v", top, p)
		}
	}
}

// TestPageForDegrades covers every fallback. Node 0 is a valid render.NodeID,
// so a zero Node must be treated as "not recorded" rather than resolving to the
// chapter root.
func TestPageForDegrades(t *testing.T) {
	fonts := testFonts(t)
	l := testLayout(t, testBook(t, 3, 18), fonts, 18)
	pages := l.PageCount()

	// A node that does not exist in the new document falls through to progress.
	p := Position{Chapter: 0, Node: 999979, Offset: 3, Progress: 0.5}
	got, how := PageFor(l, p)
	if how != "progress" {
		t.Fatalf("missing node: got (%d, %q), want the progress path", got, how)
	}
	if want := int(0.5 * float64(pages)); got != want {
		t.Errorf("progress half = %d, want %d", got, want)
	}

	// Anchor-only position: the chapter root anchor is the only anchor this
	// builder emits, so this lands on the chapter start.
	loc, ok := l.Locate(render.AnchorID(2))
	if !ok {
		t.Fatal("Locate(2) failed")
	}
	first, ok := l.PageOf(loc)
	if !ok {
		t.Fatal("PageOf(chapter 2 root) failed")
	}
	if got, how := PageFor(l, Position{Chapter: 1, Anchor: 2}); got != first || how != "anchor" {
		t.Errorf("anchor path = (%d, %q), want (%d, anchor)", got, how, first)
	}

	// Zero node + zero anchor + zero progress: the chapter start is the floor.
	if first, _, ok := l.PageRange(2); ok {
		if got, how := PageFor(l, Position{Chapter: 2}); got != first || how != "chapter" {
			t.Errorf("chapter path = (%d, %q), want (%d, chapter)", got, how, first)
		}
	}
	if starts := l.ChapterPages(); len(starts) > 0 {
		last := starts[len(starts)-1]
		if got, how := PageFor(l, Position{Chapter: 99}); got != last || how != "chapter" {
			t.Errorf("out-of-range chapter = (%d, %q), want (%d, chapter)", got, how, last)
		}
		if got, how := PageFor(l, Position{Chapter: -3}); got != starts[0] || how != "chapter" {
			t.Errorf("negative chapter = (%d, %q), want (%d, chapter)", got, how, starts[0])
		}
	}

	// Degenerate layouts report that they cannot help instead of page 0.
	if got, how := PageFor(nil, Position{Chapter: 1}); got != -1 || how != "none" {
		t.Errorf("nil layout = (%d, %q), want (-1, none)", got, how)
	}
	if got, how := PageFor(scrollLayout(t, testBook(t, 1, 18), fonts), Position{Progress: 0.5}); got != -1 || how != "none" {
		t.Errorf("scroll layout = (%d, %q), want (-1, none)", got, how)
	}
}

func scrollLayout(t *testing.T, doc *render.Document, fonts *reader.Fonts) *render.Layout {
	t.Helper()
	eng, err := render.New(render.Options{})
	if err != nil {
		t.Fatal(err)
	}
	l, err := eng.Layout(context.Background(), doc, render.LayoutOptions{
		Viewport:   render.Viewport{Width: 480, Height: 640},
		Typography: render.Typography{FontFamily: "book", FontSize: 18, LineHeight: 1.6},
		Fonts:      fonts.Set,
		Margins:    reader.Margins(),
		Mode:       render.ModeScroll,
	})
	if err != nil {
		t.Fatalf("scroll layout: %v", err)
	}
	return l
}

// TestProgressCost measures what a progress-only restore costs when the font
// size changes. The numbers land in docs/explore/friction-storage.md.
func TestProgressCost(t *testing.T) {
	fonts := testFonts(t)
	small := testLayout(t, testBook(t, 3, 18), fonts, 18)
	type size struct {
		px  float32
		lay *render.Layout
	}
	sizes := []size{{18, small}}
	for _, px := range []float32{24, 36, 48} {
		sizes = append(sizes, size{px, testLayout(t, testBook(t, 3, px), fonts, px)})
	}

	for _, s := range sizes[1:] {
		var maxDelta float64
		var maxPageErr, sumPageErr int
		for page := 0; page < small.PageCount(); page++ {
			p, ok := PositionFor(small, page)
			if !ok {
				t.Fatalf("PositionFor(%d) failed", page)
			}
			loc := render.Location{Chapter: p.Chapter, Node: render.NodeID(p.Node), Offset: p.Offset}
			after, ok := s.lay.Progress(loc)
			if !ok {
				t.Fatalf("Progress of %+v at %vpx failed", loc, s.px)
			}
			if d := math.Abs(float64(after - p.Progress)); d > maxDelta {
				maxDelta = d
			}
			truth, ok := s.lay.PageOf(loc)
			if !ok {
				t.Fatalf("PageOf of %+v at %vpx failed", loc, s.px)
			}
			restored, how := PageFor(s.lay, Position{Progress: p.Progress, Chapter: p.Chapter})
			if how != "progress" {
				t.Fatalf("PageFor took the %q path, want progress", how)
			}
			if err := abs(restored - truth); err > maxPageErr {
				maxPageErr = err
			}
			if err := abs(restored - truth); err > 0 {
				sumPageErr += err
			}
		}
		t.Logf("18px -> %vpx: %d -> %d pages; max progress delta %.4f (%.2f%% of the book); "+
			"progress restore max error %d page(s) = %.1f%% of the book, %d/%d pages off by one or more",
			s.px, small.PageCount(), s.lay.PageCount(), maxDelta, maxDelta*100,
			maxPageErr, 100*float64(maxPageErr)/float64(s.lay.PageCount()),
			sumPageErr, small.PageCount())
		// The fallback must stay within a small fraction of the book even when
		// the font size more than doubles the page count.
		if limit := max(1, s.lay.PageCount()/10); maxPageErr > limit {
			t.Errorf("18px -> %vpx: a progress restore is off by %d pages (limit %d)",
				s.px, maxPageErr, limit)
		}
	}
}

// TestAnchorIsChapterGranular measures the anchor path: this builder anchors
// only chapter roots, so an anchor restore lands on the chapter start.
func TestAnchorIsChapterGranular(t *testing.T) {
	fonts := testFonts(t)
	l := testLayout(t, testBook(t, 3, 18), fonts, 18)
	starts := l.ChapterPages()
	if len(starts) < 2 {
		t.Fatalf("chapters = %d, want at least 2", len(starts))
	}
	lost := 0
	for ch := 1; ch < len(starts); ch++ {
		first := starts[ch]
		last := l.PageCount() - 1
		if ch+1 < len(starts) {
			last = starts[ch+1] - 1
		}
		p, ok := PositionFor(l, (first+last)/2)
		if !ok {
			t.Fatalf("PositionFor(%d) failed", (first+last)/2)
		}
		if p.Anchor != 0 {
			t.Fatalf("a mid-chapter position carries anchor %d; the builder should not emit one", p.Anchor)
		}
		got, how := PageFor(l, Position{Chapter: ch, Anchor: uint32(render.AnchorID(ch + 1))})
		if how != "anchor" || got != first {
			t.Errorf("chapter %d: anchor restore = (%d, %q), want (%d, anchor)", ch, got, how, first)
		}
		lost += (first+last)/2 - got
	}
	t.Logf("%d mid-chapter pages restored from a chapter anchor lose %d pages in total "+
		"(%d pages per chapter on average, %d pages in the book)", len(starts)-1, lost,
		lost/max(1, len(starts)-1), l.PageCount())
}

// TestExploreNodeStability asks the question the storage design depends on:
// does rebuilding the same book at another font size renumber nodes? For this
// builder the answer is no, because ids come from the source order.
func TestExploreNodeStability(t *testing.T) {
	b := &book.Book{Title: "probe", Chapters: []book.Chapter{
		{Title: "one", XHTML: []byte("<html><body><h1>A</h1><p>hello <b>world</b></p></body></html>")},
		{Title: "two", XHTML: []byte("<html><body><p>second</p><img src='x.png'/></body></html>")},
	}}
	d18, _, err := b.Build(18)
	if err != nil {
		t.Fatal(err)
	}
	d30, _, err := b.Build(30)
	if err != nil {
		t.Fatal(err)
	}
	if d18.ChapterCount() != d30.ChapterCount() {
		t.Fatalf("chapter counts differ")
	}
	for ci := 0; ci < d18.ChapterCount(); ci++ {
		a, _ := d18.Chapter(ci)
		c, _ := d30.Chapter(ci)
		if len(a.Nodes) != len(c.Nodes) {
			t.Fatalf("chapter %d: %d nodes at 18px, %d at 30px", ci, len(a.Nodes), len(c.Nodes))
		}
		for i := range a.Nodes {
			if a.Nodes[i].Kind != c.Nodes[i].Kind ||
				a.Nodes[i].Text != c.Nodes[i].Text ||
				a.Nodes[i].Anchor != c.Nodes[i].Anchor {
				t.Fatalf("chapter %d node %d changed with the base size:\n18px: %+v\n30px: %+v",
					ci, i, a.Nodes[i], c.Nodes[i])
			}
		}
	}
	// Anchors, on the other hand, only exist on chapter roots: the whole
	// document has exactly one per chapter.
	anchors := 0
	for ci := 0; ci < d18.ChapterCount(); ci++ {
		ch, _ := d18.Chapter(ci)
		anchors += len(ch.Anchors)
	}
	if anchors != d18.ChapterCount() {
		t.Errorf("anchors = %d for %d chapters, want one per chapter", anchors, d18.ChapterCount())
	}
}

// TestExploreStalePosition simulates the book being replaced between launches:
// chapter 0 is new, so every node id in the recorded position now belongs to
// another chapter. PageFor cannot notice and silently returns a wrong page.
func TestExploreStalePosition(t *testing.T) {
	fonts := testFonts(t)
	old := testLayout(t, testBook(t, 3, 18), fonts, 18)

	// The "updated EPUB": one extra chapter inserted at the front, and the text
	// of every paragraph changed.
	var b strings.Builder
	b.WriteString("<html><body><h1>Inserted</h1>")
	for i := 0; i < 10; i++ {
		fmt.Fprintf(&b, "<p>Inserted paragraph %d, also long enough to paginate with "+
			"a full sentence of filler text for the measurement.</p>", i)
	}
	b.WriteString("</body></html>")
	newChapters := []book.Chapter{{Title: "Inserted", XHTML: []byte(b.String())}}
	for ci := 0; ci < 3; ci++ {
		var cb strings.Builder
		fmt.Fprintf(&cb, "<html><body><h1>Chapter %d</h1>", ci+1)
		for i := 0; i < 10; i++ {
			fmt.Fprintf(&cb, "<p>Rewritten chapter %d paragraph %d with completely "+
				"different wording than the previous revision of this file.</p>", ci+1, i+1)
		}
		cb.WriteString("</body></html>")
		newChapters = append(newChapters, book.Chapter{
			Title: fmt.Sprintf("Chapter %d", ci+1), XHTML: []byte(cb.String()),
		})
	}
	newDoc, _, err := (&book.Book{Title: "probe", Chapters: newChapters}).Build(18)
	if err != nil {
		t.Fatal(err)
	}
	fresh := testLayout(t, newDoc, fonts, 18)

	// A position from the middle of the old chapter 2.
	p, ok := PositionFor(old, old.PageCount()*2/3)
	if !ok {
		t.Fatal("PositionFor failed")
	}
	got, how := PageFor(fresh, p)
	t.Logf("stale position chapter %d node %d offset %d: PageFor = (%d, %q) in the new book "+
		"(%d pages), old page was %d of %d",
		p.Chapter, p.Node, p.Offset, got, how, fresh.PageCount(), old.PageCount()*2/3, old.PageCount())
	if how != "node" {
		t.Fatalf("expected the node path to fire blindly, got (%d, %q)", got, how)
	}
	if got < 0 || got >= fresh.PageCount() {
		t.Fatalf("stale restore produced an out-of-range page %d", got)
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// testScrollLayout builds the same synthetic book as a continuous column.
func testScrollLayout(t *testing.T, doc *render.Document, fonts *reader.Fonts, size float32) *render.Layout {
	t.Helper()
	l, err := reader.BuildLayout(context.Background(), doc, fonts, 480, 640, reader.Options{FontSize: size, Scroll: true})
	if err != nil {
		t.Fatalf("BuildLayout(scroll): %v", err)
	}
	if l.PageCount() != 0 || l.ContentHeight() <= 0 {
		t.Fatalf("not a scroll layout: %d pages, %.0f tall", l.PageCount(), l.ContentHeight())
	}
	return l
}

// TestLocateScrollRoundTrip records the place at several offsets of a scroll
// layout and finds them again from the persisted Position.
func TestLocateScrollRoundTrip(t *testing.T) {
	fonts := testFonts(t)
	l := testScrollLayout(t, testBook(t, 3, 18), fonts, 18)
	height := l.ContentHeight()

	for _, y := range []float32{1, height / 4, height / 2, height - 2} {
		loc, ok := l.LocationAt(y)
		if !ok {
			t.Fatalf("LocationAt(%.0f) failed", y)
		}
		want, ok := l.OffsetOf(loc)
		if !ok {
			t.Fatalf("OffsetOf(%+v) failed", loc)
		}
		p, ok := PositionAt(l, loc)
		if !ok {
			t.Fatalf("PositionAt(%+v) failed", loc)
		}
		if p.Node == 0 || p.Progress <= 0 {
			t.Errorf("position at %.0f is incomplete: %+v", y, p)
		}
		got, how := Locate(l, p)
		if how != "node" && how != "anchor" {
			t.Errorf("Locate at %.0f went through %q, want the exact path", y, how)
		}
		if back, ok := l.OffsetOf(got); !ok || back != want {
			t.Errorf("position at %.0f came back at %.0f, want %.0f", y, back, want)
		}
	}

	// The progress fallback still lands in the flow without node or anchor.
	loc, how := Locate(l, Position{Chapter: 1, Progress: 0.5})
	if how != "progress" {
		t.Errorf("progress-only position resolved via %q", how)
	}
	if off, ok := l.OffsetOf(loc); !ok || off < height*0.4 || off > height*0.6 {
		t.Errorf("progress half landed at %.0f of %.0f", off, height)
	}

	// PageFor keeps saying there are no pages, so a scroll host cannot mistake
	// it for a page number.
	if page, how := PageFor(l, Position{Progress: 0.5}); page != -1 || how != "none" {
		t.Errorf("PageFor on a scroll layout = %d, %q", page, how)
	}
}

// TestPositionForSingleChapterBook covers the book that has one chapter, which
// is what a short TXT is. Every page of it belongs to chapter 0, so the old
// page-slot probe — derived from "the first page of a chapter that does not
// start on page 0" — could measure nothing and answered false for every page:
// no page turn of such a book was ever recorded, so it neither resumed where it
// was left nor showed any progress on the shelf.
func TestPositionForSingleChapterBook(t *testing.T) {
	fonts := testFonts(t)
	l := testLayout(t, testBook(t, 1, 18), fonts, 18)
	if starts := l.ChapterPages(); len(starts) != 1 || starts[0] != 0 {
		t.Fatalf("chapter pages = %v, want one chapter starting on page 0", starts)
	}
	pages := l.PageCount()
	if pages < 2 {
		t.Fatalf("page count = %d, the probe needs a multi-page chapter", pages)
	}
	for page := 0; page < pages; page++ {
		p, ok := PositionFor(l, page)
		if !ok {
			t.Fatalf("PositionFor(%d) reported the page as unreachable", page)
		}
		if got, how := PageFor(l, p); got != page || how != "node" {
			t.Errorf("page %d round-trips to (%d, %q)", page, got, how)
		}
		if p.Progress < 0 || p.Progress > 1 {
			t.Errorf("page %d recorded progress %v", page, p.Progress)
		}
		if page > 0 && p.Progress <= 0 {
			t.Errorf("page %d is past the start of the book but recorded no progress", page)
		}
	}

	// The page slot is the whole viewport minus its margins: a probe into a page
	// must not land in its neighbour.
	if h, ok := pageSlotHeight(l); !ok || h != l.PageContentHeight() {
		t.Errorf("pageSlotHeight = %v, %v", h, ok)
	}
	if _, ok := PositionFor(l, pages); ok {
		t.Errorf("PositionFor accepted page %d of %d", pages, pages)
	}
	if _, ok := PositionFor(l, -1); ok {
		t.Error("PositionFor accepted page -1")
	}
}

// The last page is the end of the book even though Progress names its top and
// is therefore below 1. Every other page is not finished.
func TestPositionForMarksTheLastPageFinished(t *testing.T) {
	fonts := testFonts(t)
	l := testLayout(t, testBook(t, 3, 18), fonts, 18)
	if l.PageCount() < 2 {
		t.Skip("the synthetic book fits on one page")
	}
	for page := 0; page < l.PageCount(); page++ {
		p, ok := PositionFor(l, page)
		if !ok {
			t.Fatalf("PositionFor(%d) failed", page)
		}
		want := page == l.PageCount()-1
		if p.Finished != want {
			t.Errorf("page %d Finished = %v, want %v", page, p.Finished, want)
		}
		if p.Completed() != want {
			t.Errorf("page %d Completed = %v, want %v", page, p.Completed(), want)
		}
	}
}

// Positions written before Finished existed are read from Progress; a book on
// its last page left it just under 1.
func TestCompletedReadsOldPositions(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    Position
		want bool
	}{
		{"zero", Position{}, false},
		{"start", Position{Progress: 0.001}, false},
		{"half", Position{Progress: 0.5}, false},
		{"just below", Position{Progress: 0.994}, false},
		{"threshold", Position{Progress: 0.995}, true},
		{"one", Position{Progress: 1}, true},
		{"marked", Position{Progress: 0.5, Finished: true}, true},
	} {
		if got := tc.p.Completed(); got != tc.want {
			t.Errorf("%s: Completed() = %v, want %v", tc.name, got, tc.want)
		}
	}
}
