// Package search indexes the rendered text of a book and maps matches back to
// render locations, so the reader can search the whole book and jump to a page.
package search

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/laix/layx-reader/internal/book"
	"github.com/laix/layx/render"
)

// Hit is one occurrence of the query.
type Hit struct {
	Chapter int    // chapter index
	Title   string // chapter title, for the UI
	Offset  int    // rune offset of the match in the chapter's concatenated text
	Snippet string // about 24 runes on each side, the match in 「」
	Loc     render.Location
	// End is the location just past the match, in the same text node, so a host
	// can paint the hit with Layout.Selection(Loc, End).
	End render.Location
}

// textSpan records where one text node lives in the concatenated chapter text.
type textSpan struct {
	node               render.NodeID
	byteStart, byteEnd int // byte range in chapter text
	start, end         int // rune range in chapter text
}

type chapterIndex struct {
	title  string
	text   string // NodeText text in document order
	folded string // ASCII-folded copy of text, same byte length
	spans  []textSpan
}

// Index is an immutable full-text index of one render.Document.
//
// The index stores the document it was built from so a host can notice that a
// relayout (after a font-size change, say) produced a different document. See
// Document and Stale.
type Index struct {
	doc      *render.Document
	chapters []chapterIndex
}

// NewIndex builds a full-text index from doc. Text comes from render.NodeText
// nodes only; the original XHTML is never consulted, so offsets agree with the
// locations PageOf understands.
func NewIndex(b *book.Book, doc *render.Document) (*Index, error) {
	if doc == nil {
		return nil, errors.New("search: nil document")
	}
	ix := &Index{doc: doc, chapters: make([]chapterIndex, 0, doc.ChapterCount())}
	for i := 0; i < doc.ChapterCount(); i++ {
		ch, ok := doc.Chapter(i)
		if !ok {
			return nil, fmt.Errorf("search: chapter %d is missing", i)
		}
		text, spans := chapterText(ch)
		title := ch.ID
		if b != nil && i < len(b.Chapters) && strings.TrimSpace(b.Chapters[i].Title) != "" {
			title = b.Chapters[i].Title
		}
		ix.chapters = append(ix.chapters, chapterIndex{
			title:  title,
			text:   text,
			folded: foldASCII(text),
			spans:  spans,
		})
	}
	return ix, nil
}

// Document returns the document this index was built from.
func (ix *Index) Document() *render.Document {
	if ix == nil {
		return nil
	}
	return ix.doc
}

// Stale reports whether doc is not the document this index was built from.
func (ix *Index) Stale(doc *render.Document) bool {
	return ix == nil || ix.doc == nil || doc != ix.doc
}

// chapterText concatenates the chapter's text nodes in reading order and
// records each node's rune range. Empty text nodes are skipped.
func chapterText(ch render.Chapter) (string, []textSpan) {
	var text strings.Builder
	var spans []textSpan
	offset, byteOffset := 0, 0

	stack := []render.NodeID{ch.Root}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if id < 0 || int(id) >= len(ch.Nodes) {
			continue
		}
		n := &ch.Nodes[id]
		if n.Kind == render.NodeText {
			if n.Text == "" {
				continue
			}
			end := offset + utf8.RuneCountInString(n.Text)
			byteEnd := byteOffset + len(n.Text)
			spans = append(spans, textSpan{
				node:      id,
				byteStart: byteOffset,
				byteEnd:   byteEnd,
				start:     offset,
				end:       end,
			})
			text.WriteString(n.Text)
			offset, byteOffset = end, byteEnd
			continue
		}
		for i := len(n.Children) - 1; i >= 0; i-- {
			stack = append(stack, n.Children[i])
		}
	}
	return text.String(), spans
}

// Search returns at most limit hits in reading order. Matching is
// case-insensitive for ASCII; limit <= 0 means 100. Matches that only exist
// because two text nodes were concatenated are skipped.
func (ix *Index) Search(query string, limit int) []Hit {
	if ix == nil || query == "" {
		return nil
	}
	if limit <= 0 {
		limit = 100
	}
	foldedQuery := foldASCII(query)
	if foldedQuery == "" {
		return nil
	}
	var hits []Hit
	for ci := range ix.chapters {
		ch := &ix.chapters[ci]
		for pos := 0; pos < len(ch.folded); {
			rel := strings.Index(ch.folded[pos:], foldedQuery)
			if rel < 0 {
				break
			}
			byteStart := pos + rel
			byteEnd := byteStart + len(foldedQuery)
			if byteEnd > len(ch.text) {
				break
			}
			// Move past an accepted match; after a boundary rejection move
			// only one rune so a real match inside the false one is not lost.
			next := byteEnd

			k := sort.Search(len(ch.spans), func(i int) bool {
				return ch.spans[i].byteEnd > byteStart
			})
			if k >= len(ch.spans) {
				break
			}
			span := ch.spans[k]
			if byteStart < span.byteStart || byteEnd > span.byteEnd {
				// The match crosses a node boundary, so it is not a real
				// occurrence in the chapter's text. Keep looking one rune in.
				_, size := utf8.DecodeRuneInString(ch.text[byteStart:])
				next = byteStart + size
				pos = next
				continue
			}

			start := span.start + utf8.RuneCountInString(ch.text[span.byteStart:byteStart])
			pos = next
			hits = append(hits, Hit{
				Chapter: ci,
				Title:   ch.title,
				Offset:  start,
				Snippet: snippet(ch.text, byteStart, byteEnd),
				Loc: render.Location{
					Chapter: ci,
					Node:    span.node,
					Offset:  start - span.start,
				},
				End: render.Location{
					Chapter: ci,
					Node:    span.node,
					Offset:  start - span.start + utf8.RuneCountInString(foldedQuery),
				},
			})
			if len(hits) >= limit {
				return hits
			}
		}
	}
	return hits
}

// foldASCII lowercases A-Z only. It keeps byte and rune lengths unchanged, so
// byte offsets found in the folded text are valid in the original.
func foldASCII(s string) string {
	i := 0
	for i < len(s) {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			break
		}
		i++
	}
	if i == len(s) {
		return s
	}
	b := []byte(s)
	for ; i < len(b); i++ {
		if c := b[i]; c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

const snippetContext = 24

// snippet returns about snippetContext runes on each side of text[start:end],
// with the match in 「」 and an ellipsis where the chapter was cut.
func snippet(text string, start, end int) string {
	lo := start
	for n := 0; n < snippetContext && lo > 0; n++ {
		_, size := utf8.DecodeLastRuneInString(text[:lo])
		if size <= 0 {
			break
		}
		lo -= size
	}
	hi := end
	for n := 0; n < snippetContext && hi < len(text); n++ {
		_, size := utf8.DecodeRuneInString(text[hi:])
		if size <= 0 {
			break
		}
		hi += size
	}

	var b strings.Builder
	b.Grow(hi - lo + 4)
	if lo > 0 {
		b.WriteRune('…')
	}
	b.WriteString(text[lo:start])
	b.WriteRune('「')
	b.WriteString(text[start:end])
	b.WriteRune('」')
	b.WriteString(text[end:hi])
	if hi < len(text) {
		b.WriteRune('…')
	}
	return b.String()
}

// Page resolves a hit to a global page number. It returns false when layout is
// nil, when the hit does not belong to this index's document, or when the
// layout's chapter structure no longer matches the index.
//
// render.Layout does not expose its *Document, so Page cannot compare document
// identity directly. A host that still has the document it laid out should
// check ix.Stale(doc) before calling Page.
func Page(layout *render.Layout, ix *Index, h Hit) (int, bool) {
	if layout == nil || ix == nil || ix.doc == nil {
		return 0, false
	}
	if h.Chapter < 0 || h.Chapter >= len(ix.chapters) || h.Loc.Chapter != h.Chapter {
		return 0, false
	}
	ch, ok := ix.doc.Chapter(h.Chapter)
	if !ok {
		return 0, false
	}
	if h.Loc.Node < 0 || int(h.Loc.Node) >= len(ch.Nodes) {
		return 0, false
	}
	node := ch.Nodes[h.Loc.Node]
	if node.Kind != render.NodeText {
		return 0, false
	}
	n := utf8.RuneCountInString(node.Text)
	if h.Loc.Offset < 0 || h.Loc.Offset >= n {
		return 0, false
	}

	pages := layout.ChapterPages()
	if pages != nil && len(pages) != ix.doc.ChapterCount() {
		return 0, false
	}
	if _, _, ok := layout.PageRange(h.Chapter); !ok {
		return 0, false
	}
	page, ok := layout.PageOf(h.Loc)
	if !ok || page < 0 || page >= layout.PageCount() {
		return 0, false
	}
	return page, true
}
