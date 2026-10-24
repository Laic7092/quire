package reader

import (
	"strings"
	"unicode/utf8"

	"github.com/laix/layx/render"
)

// Selection is a run of text between two document locations: the anchor where
// a press on the page landed, and the focus the drag has reached. Either order
// works, and the zero value is empty.
//
// Locations are valid for the life of one Layout: a relayout (a new font size,
// another viewport) hands out new node ids, so a host clears the selection when
// it publishes a new layout. Cross-page and cross-chapter ranges are the
// engine's business; this type only keeps the endpoints.
type Selection struct {
	anchor, focus render.Location
	live          bool
}

// Empty reports whether nothing is selected, or the selection is a single
// point.
func (s *Selection) Empty() bool { return s == nil || !s.live }

// Start begins a selection at loc, replacing the previous one.
func (s *Selection) Start(loc render.Location) {
	s.anchor, s.focus, s.live = loc, loc, true
}

// Extend moves the focus of the selection to loc.
func (s *Selection) Extend(loc render.Location) {
	if s == nil || !s.live {
		return
	}
	s.focus = loc
}

// Clear drops the selection.
func (s *Selection) Clear() {
	s.anchor, s.focus, s.live = render.Location{}, render.Location{}, false
}

// Focus returns the location the drag last reached.
func (s *Selection) Focus() render.Location { return s.focus }

// Quads returns the rectangles covering the selection, page by page. It is
// Layout.Selection underneath, which orders the endpoints itself, so a
// backwards drag paints the same rectangle it would forwards.
func (s *Selection) Quads(l *render.Layout) []render.SelectionQuad {
	if s == nil || !s.live || l == nil {
		return nil
	}
	return l.Selection(s.anchor, s.focus)
}

// Text returns the selected runes, read from the document the layout was built
// from. Chapters are joined by a blank line. An endpoint on an image or an
// empty block resolves to where it sits, so dragging over one keeps the text
// around it.
func (s *Selection) Text(doc *render.Document) string {
	if s == nil || !s.live || doc == nil {
		return ""
	}
	from, to := s.anchor, s.focus
	if from.Chapter > to.Chapter {
		from, to = to, from
	}
	var parts []string
	for ci := from.Chapter; ci <= to.Chapter; ci++ {
		ch, ok := doc.Chapter(ci)
		if !ok {
			continue
		}
		ct := chapterTextOf(ch)
		lo, hi := 0, ct.runes
		if ci == from.Chapter {
			if off, ok := ct.offset(from); ok {
				lo = off
			}
		}
		if ci == to.Chapter {
			if off, ok := ct.offset(to); ok {
				hi = off
			}
		}
		if lo > hi {
			lo, hi = hi, lo
		}
		if text := sliceRunes(ct.text, lo, hi); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// textNode is one node of a chapter in reading order, with the rune range it
// covers in the chapter's text. A node that is not a text node covers none and
// only marks where it sits, so a location on an image still resolves.
type textNode struct {
	node       render.NodeID
	start, end int
}

// chapterText is a chapter's text as a reader copies it: its text nodes in
// reading order, every other node at zero width.
type chapterText struct {
	text  string
	runes int
	nodes []textNode
}

// chapterTextOf concatenates the chapter's text nodes in reading order, as the
// search index does, and records where every node falls in it.
func chapterTextOf(ch render.Chapter) chapterText {
	var ct chapterText
	var b strings.Builder
	stack := []render.NodeID{ch.Root}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if id < 0 || int(id) >= len(ch.Nodes) {
			continue
		}
		n := &ch.Nodes[id]
		if n.Kind == render.NodeText {
			r := utf8.RuneCountInString(n.Text)
			ct.nodes = append(ct.nodes, textNode{node: id, start: ct.runes, end: ct.runes + r})
			b.WriteString(n.Text)
			ct.runes += r
			continue
		}
		ct.nodes = append(ct.nodes, textNode{node: id, start: ct.runes, end: ct.runes})
		for i := len(n.Children) - 1; i >= 0; i-- {
			stack = append(stack, n.Children[i])
		}
	}
	ct.text = b.String()
	return ct
}

// offset maps a location in this chapter to a rune offset in its text, clamped
// to the node it names. ok is false when the node is not in the chapter.
func (ct chapterText) offset(loc render.Location) (int, bool) {
	for i := range ct.nodes {
		tn := &ct.nodes[i]
		if tn.node != loc.Node {
			continue
		}
		off := tn.start + loc.Offset
		if off > tn.end {
			off = tn.end
		}
		if off < tn.start {
			off = tn.start
		}
		return off, true
	}
	return 0, false
}

// sliceRunes returns the runes [lo, hi) of s, counting lo and hi as rune
// offsets. An empty range, or one starting past the end, is "".
func sliceRunes(s string, lo, hi int) string {
	if hi <= lo || lo < 0 {
		return ""
	}
	start, end := -1, len(s)
	i := 0
	for pos := range s {
		if i == lo {
			start = pos
		}
		if i == hi {
			end = pos
			break
		}
		i++
	}
	if start < 0 {
		return ""
	}
	return s[start:end]
}
