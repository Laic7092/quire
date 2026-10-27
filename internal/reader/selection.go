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

// Start begins a selection at loc, replacing the previous one. A nil selection
// has nowhere to keep it and starts nothing.
func (s *Selection) Start(loc render.Location) {
	if s == nil {
		return
	}
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
	if s == nil {
		return
	}
	s.anchor, s.focus, s.live = render.Location{}, render.Location{}, false
}

// Focus returns the location the drag last reached.
func (s *Selection) Focus() render.Location {
	if s == nil {
		return render.Location{}
	}
	return s.focus
}

// Anchor returns the location the press began at, which is one end of the
// selection; Focus is the other.
func (s *Selection) Anchor() render.Location {
	if s == nil {
		return render.Location{}
	}
	return s.anchor
}

// Ends returns both ends of the selection: the anchor the drag began at and
// the focus it reached, in gesture order. Either order works for Layout and
// quads; a host that stores the passage keeps them as they are.
func (s *Selection) Ends() (anchor, focus render.Location) {
	if s == nil {
		return render.Location{}, render.Location{}
	}
	return s.anchor, s.focus
}

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
//
// The endpoints are clamped to the document: a location kept from another book,
// or one whose chapter outlived a rebuild, reads the chapters that are there
// rather than walking a chapter count that is not.
func (s *Selection) Text(doc *render.Document) string {
	if s == nil || !s.live || doc == nil {
		return ""
	}
	from, to := s.anchor, s.focus
	if from.Chapter > to.Chapter {
		from, to = to, from
	}
	if from.Chapter < 0 {
		from.Chapter = 0
	}
	if n := doc.ChapterCount(); to.Chapter >= n {
		to.Chapter = n - 1
	}
	if from.Chapter > to.Chapter {
		return ""
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

// chapterTextOf concatenates the chapter's text nodes in reading order and
// records where every node falls in it. Block boundaries become one newline, so
// copying two paragraphs keeps them apart instead of gluing the last word of one
// to the first of the next; chapters themselves are joined by the caller.
func chapterTextOf(ch render.Chapter) chapterText {
	var ct chapterText
	var b strings.Builder
	// A block that held text asks for a newline before the next text run. The
	// break is deferred so an empty block and the end of the chapter do not
	// leave a trailing newline.
	pending := false
	write := func(s string) {
		if s == "" {
			return
		}
		if pending {
			b.WriteByte('\n')
			ct.runes++
			pending = false
		}
		b.WriteString(s)
		ct.runes += utf8.RuneCountInString(s)
	}
	type frame struct {
		node  render.NodeID
		exit  bool
		start int
	}
	stack := []frame{{node: ch.Root}}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if f.node < 0 || int(f.node) >= len(ch.Nodes) {
			continue
		}
		n := &ch.Nodes[f.node]
		if f.exit {
			if n.Kind == render.NodeBlock && ct.runes > f.start {
				pending = true
			}
			continue
		}
		switch n.Kind {
		case render.NodeText:
			r := utf8.RuneCountInString(n.Text)
			write(n.Text)
			ct.nodes = append(ct.nodes, textNode{node: f.node, start: ct.runes - r, end: ct.runes})
		case render.NodeBreak:
			// A forced break is one newline and satisfies a pending block break.
			pending = false
			b.WriteByte('\n')
			ct.runes++
			ct.nodes = append(ct.nodes, textNode{node: f.node, start: ct.runes, end: ct.runes})
		default:
			ct.nodes = append(ct.nodes, textNode{node: f.node, start: ct.runes, end: ct.runes})
			if n.Kind == render.NodeBlock {
				stack = append(stack, frame{node: f.node, exit: true, start: ct.runes})
			}
			for i := len(n.Children) - 1; i >= 0; i-- {
				stack = append(stack, frame{node: n.Children[i]})
			}
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
