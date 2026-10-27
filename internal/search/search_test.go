package search

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/laix/layx-reader/internal/book"
	"github.com/laix/layx-reader/internal/reader"
	"github.com/laix/layx/render"
)

func sampleIndex(t *testing.T) (*book.Book, *render.Document, *Index) {
	t.Helper()
	b, err := book.Open(filepath.Join("..", "book", "testdata", "samples", "fusheng-liuji.txt"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := b.Document()
	if err != nil {
		t.Fatal(err)
	}
	ix, err := NewIndex(b, doc)
	if err != nil {
		t.Fatal(err)
	}
	return b, doc, ix
}

func TestSearchFindsKnownText(t *testing.T) {
	b, doc, ix := sampleIndex(t)
	const query = "滄浪亭"

	hits := ix.Search(query, 0)
	if len(hits) == 0 {
		t.Fatalf("no hits for %q", query)
	}
	if len(hits) > 100 {
		t.Fatalf("default limit returned %d hits", len(hits))
	}

	h := hits[0]
	if h.Chapter < 0 || h.Chapter >= len(b.Chapters) {
		t.Fatalf("chapter = %d", h.Chapter)
	}
	if h.Title != b.Chapters[h.Chapter].Title {
		t.Errorf("title = %q, want %q", h.Title, b.Chapters[h.Chapter].Title)
	}
	if !strings.Contains(h.Snippet, query) {
		t.Errorf("snippet %q does not contain %q", h.Snippet, query)
	}

	ch, ok := doc.Chapter(h.Chapter)
	if !ok {
		t.Fatal("chapter missing from document")
	}
	if h.Loc.Node < 0 || int(h.Loc.Node) >= len(ch.Nodes) {
		t.Fatalf("node = %d, chapter has %d nodes", h.Loc.Node, len(ch.Nodes))
	}
	node := ch.Nodes[h.Loc.Node]
	if node.Kind != render.NodeText {
		t.Fatalf("node kind = %d, want text", node.Kind)
	}
	runes := []rune(node.Text)
	q := []rune(query)
	if h.Loc.Offset < 0 || h.Loc.Offset+len(q) > len(runes) {
		t.Fatalf("offset %d out of range for node text of %d runes", h.Loc.Offset, len(runes))
	}
	if got := string(runes[h.Loc.Offset : h.Loc.Offset+len(q)]); got != query {
		t.Errorf("text at location = %q, want %q", got, query)
	}
	if h.End.Chapter != h.Chapter || h.End.Node != h.Loc.Node {
		t.Errorf("end %+v is not in the match's node", h.End)
	}
	if h.End.Offset != h.Loc.Offset+len(q) {
		t.Errorf("end offset = %d, want %d", h.End.Offset, h.Loc.Offset+len(q))
	}

	// Offset is a rune offset in the chapter text, not in the node.
	ci := &ix.chapters[h.Chapter]
	all := []rune(ci.text)
	if h.Offset < 0 || h.Offset+len(q) > len(all) {
		t.Fatalf("chapter offset %d out of range for %d runes", h.Offset, len(all))
	}
	if got := string(all[h.Offset : h.Offset+len(q)]); got != query {
		t.Errorf("chapter text at offset = %q, want %q", got, query)
	}
}

func TestSearchIsASCIIFold(t *testing.T) {
	b := &book.Book{
		Title: "case",
		Chapters: []book.Chapter{{
			Title: "one",
			XHTML: []byte("<html><body><p>Hello Layx Search</p><p>hello WORLD</p></body></html>"),
		}},
	}
	doc, err := b.Document()
	if err != nil {
		t.Fatal(err)
	}
	ix, err := NewIndex(b, doc)
	if err != nil {
		t.Fatal(err)
	}
	hits := ix.Search("hello", 0)
	if len(hits) != 2 {
		t.Fatalf("hits = %d, want 2", len(hits))
	}
	if !strings.Contains(hits[0].Snippet, "Hello") {
		t.Errorf("snippet = %q, want original casing", hits[0].Snippet)
	}
	if hits[0].Offset >= hits[1].Offset {
		t.Errorf("hits are not in reading order: %d, %d", hits[0].Offset, hits[1].Offset)
	}
}

func TestSearchSkipsCrossNodeMatches(t *testing.T) {
	b := &book.Book{
		Title: "boundary",
		Chapters: []book.Chapter{{
			Title: "one",
			XHTML: []byte("<html><body><p>foo</p><p>bar</p></body></html>"),
		}},
	}
	doc, err := b.Document()
	if err != nil {
		t.Fatal(err)
	}
	ix, err := NewIndex(b, doc)
	if err != nil {
		t.Fatal(err)
	}
	if hits := ix.Search("oob", 0); len(hits) != 0 {
		t.Fatalf("cross-node hits = %#v, want none", hits)
	}
	if hits := ix.Search("foo", 0); len(hits) != 1 {
		t.Fatalf("in-node hits = %d, want 1", len(hits))
	}

	// A fake cross-node match at "aa"|"aa" must not hide the real "aa" that
	// starts one rune later inside the second node.
	b = &book.Book{
		Title: "boundary-overlap",
		Chapters: []book.Chapter{{
			Title: "one",
			XHTML: []byte("<html><body><p>xa</p><p>aa</p></body></html>"),
		}},
	}
	doc, err = b.Document()
	if err != nil {
		t.Fatal(err)
	}
	ix, err = NewIndex(b, doc)
	if err != nil {
		t.Fatal(err)
	}
	hits := ix.Search("aa", 0)
	if len(hits) != 1 {
		t.Fatalf("overlap hits = %d, want 1", len(hits))
	}
	if hits[0].Offset != 2 || hits[0].Loc.Offset != 0 {
		t.Errorf("overlap hit = offset %d node offset %d, want 2 and 0",
			hits[0].Offset, hits[0].Loc.Offset)
	}
}

func TestSearchLimit(t *testing.T) {
	_, _, ix := sampleIndex(t)
	hits := ix.Search("之", 3)
	if len(hits) > 3 {
		t.Fatalf("limit 3 returned %d hits", len(hits))
	}
	if len(hits) == 0 {
		t.Fatal("no hits for a common character")
	}
	for i := 1; i < len(hits); i++ {
		if hits[i].Chapter < hits[i-1].Chapter ||
			(hits[i].Chapter == hits[i-1].Chapter && hits[i].Offset < hits[i-1].Offset) {
			t.Fatalf("hits are not in reading order at %d", i)
		}
	}
}

func TestPageOfHit(t *testing.T) {
	b, doc, ix := sampleIndex(t)
	path, err := reader.FindFont("")
	if err != nil {
		t.Skipf("no font available: %v", err)
	}
	fonts, err := reader.OpenFont(path)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := reader.BuildLayout(context.Background(), doc, fonts, 480, 640, reader.Options{FontSize: 18})
	if err != nil {
		t.Fatal(err)
	}

	hits := ix.Search("滄浪亭", 0)
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	h := hits[len(hits)-1]
	page, ok := Page(layout, ix, h)
	if !ok {
		t.Fatalf("Page(%+v) failed", h.Loc)
	}
	if page < 0 || page >= layout.PageCount() {
		t.Fatalf("page = %d, page count = %d", page, layout.PageCount())
	}

	if _, ok := Page(nil, ix, h); ok {
		t.Error("Page with nil layout succeeded")
	}
	if _, ok := Page(layout, nil, h); ok {
		t.Error("Page with nil index succeeded")
	}

	// The index still refers to the document it was built from.
	if ix.Stale(doc) {
		t.Error("index is stale for the document it was built from")
	}
	if other, err := b.DocumentSized(24); err == nil && !ix.Stale(other) {
		t.Error("index is not stale for a different document")
	}

	if utf8.RuneCountInString(h.Snippet) == 0 {
		t.Error("empty snippet")
	}
}

// A query pasted or typed with space around it names the same text: only the
// query itself is matched, and a query of nothing but whitespace finds nothing.
func TestSearchIgnoresSurroundingSpace(t *testing.T) {
	_, _, ix := sampleIndex(t)
	want := len(ix.Search("滄浪亭", 0))
	if want == 0 {
		t.Fatal("the sample no longer contains the query")
	}
	for _, query := range []string{" 滄浪亭", "滄浪亭 ", "\n滄浪亭\t", "　滄浪亭　"} {
		if got := len(ix.Search(query, 0)); got != want {
			t.Errorf("Search(%q) = %d hits, want %d", query, got, want)
		}
	}
	for _, query := range []string{"", " ", "\n\t", "　"} {
		if hits := ix.Search(query, 0); len(hits) != 0 {
			t.Errorf("Search(%q) = %d hits, want none", query, len(hits))
		}
	}
}

// Hits do not overlap: the scan moves past an accepted match, so "aa" has one
// hit in "aaa", and repeated text in one node yields one hit per position
// without duplicates.
func TestSearchHitsDoNotOverlap(t *testing.T) {
	b := &book.Book{Title: "overlap", Chapters: []book.Chapter{{
		Title: "one",
		XHTML: []byte("<html><body><p>aaababab</p></body></html>"),
	}}}
	doc, err := b.Document()
	if err != nil {
		t.Fatal(err)
	}
	ix, err := NewIndex(b, doc)
	if err != nil {
		t.Fatal(err)
	}

	// "aa" matches at 0 and 1; only the first is a hit, and the scan resumes
	// after it instead of reporting the overlap.
	hits := ix.Search("aa", 0)
	if len(hits) != 1 || hits[0].Offset != 0 {
		t.Fatalf("Search(\"aa\") = %+v, want one hit at 0", hits)
	}

	hits = ix.Search("ab", 0)
	if len(hits) != 3 {
		t.Fatalf("Search(\"ab\") = %d hits, want 3: %+v", len(hits), hits)
	}
	ch, ok := doc.Chapter(0)
	if !ok {
		t.Fatal("chapter missing from document")
	}
	for i, h := range hits {
		// "ab" sits at 2, 4 and 6 of "aaababab".
		if h.Offset != 2+2*i {
			t.Errorf("hit %d offset = %d, want %d", i, h.Offset, 2+2*i)
		}
		runes := []rune(ch.Nodes[h.Loc.Node].Text)
		if h.Loc.Offset+2 > len(runes) || string(runes[h.Loc.Offset:h.Loc.Offset+2]) != "ab" {
			t.Errorf("hit %d node offset %d does not point at the match in %q",
				i, h.Loc.Offset, string(runes))
		}
	}
}

// A layout built from another document must not answer for the index: its node
// ids mean different text, so the page it reports is not the hit's page.
func TestPageRejectsLayoutOfAnotherDocument(t *testing.T) {
	b, doc, ix := sampleIndex(t)
	path, err := reader.FindFont("")
	if err != nil {
		t.Skipf("no font available: %v", err)
	}
	fonts, err := reader.OpenFont(path)
	if err != nil {
		t.Fatal(err)
	}
	hits := ix.Search("滄浪亭", 0)
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	h := hits[len(hits)-1]

	other, err := b.DocumentSized(24)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		doc  *render.Document
		want bool
	}{
		{"same document", doc, true},
		{"other document", other, false},
	} {
		layout, err := reader.BuildLayout(context.Background(), tc.doc, fonts, 480, 640,
			reader.Options{FontSize: 18})
		if err != nil {
			t.Fatal(err)
		}
		page, ok := Page(layout, ix, h)
		if ok != tc.want {
			t.Errorf("%s: Page = %d, %v; want ok %v", tc.name, page, ok, tc.want)
		}
	}
}

// The query is literal text, never a pattern: regex metacharacters match
// themselves and find nothing when the chapter has no such characters.
func TestSearchIsLiteralText(t *testing.T) {
	b := &book.Book{Title: "literal", Chapters: []book.Chapter{{
		Title: "one",
		XHTML: []byte("<html><body><p>abc a.c a*c</p></body></html>"),
	}}}
	doc, err := b.Document()
	if err != nil {
		t.Fatal(err)
	}
	ix, err := NewIndex(b, doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query string
		want  int
	}{
		{"a.c", 1}, // not the "abc"
		{"a*c", 1},
		{".*", 0},
		{"[abc]", 0},
		{"^abc", 0},
		{"abc$", 0},
	} {
		if hits := ix.Search(tc.query, 0); len(hits) != tc.want {
			t.Errorf("Search(%q) = %d hits, want %d: %+v", tc.query, len(hits), tc.want, hits)
		}
	}
}
