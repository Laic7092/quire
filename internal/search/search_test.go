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
