// Package render is a reflowable document layout and rendering engine.
//
// It consumes an already parsed and cascaded Document (the IR produced by a
// host or compiler) and produces pages made of drawing commands. It does not
// parse EPUB, XHTML or CSS, and it does not contain a rasterizer.
//
// A Document, FontSet and Layout are immutable after construction except for
// internal caches. They are safe for concurrent use once built.
//
// A host Painter may optionally implement paint.Clipped; Page.Draw and
// Tile.Draw then skip display-list ops outside its visible region. This is
// replay-only culling: it does not lay out, shape or rasterize.
package render

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/laix/layx/render/paint"
)

// NodeID identifies a node inside a Chapter.
type NodeID int32

// InvalidNode is the zero-value-invalid NodeID.
const InvalidNode NodeID = -1

// StyleID identifies an entry in Document.Styles.
type StyleID uint32

// AnchorID is a host-owned identifier for a node. Zero means "no anchor".
//
// An anchor identifies a node, not a rune position: persist Location.Offset
// alongside it. The engine only guarantees that an anchor maps to the same node
// within one Document; stability across a rebuilt Document is the host's
// responsibility (reuse the same ID for the same source position).
type AnchorID uint32

// NoAnchor is the zero AnchorID.
const NoAnchor AnchorID = 0

// ImageID is an opaque host image handle.
type ImageID = paint.ImageID

// NodeKind describes the role of a Node.
type NodeKind uint8

const (
	// NodeBlock is a block container. Its children are laid out vertically.
	NodeBlock NodeKind = iota
	// NodeInline is an inline container. Its children participate in the
	// surrounding paragraph.
	NodeInline
	// NodeText is a run of text.
	NodeText
	// NodeImage is a replaced element.
	NodeImage
	// NodeBreak is a forced line break (like <br>).
	NodeBreak
)

// Node is one entry in the flat node arena of a Chapter.
//
// Nodes are intentionally simple: the host is expected to resolve CSS,
// inheritance and selectors before building the IR.
type Node struct {
	Kind     NodeKind
	Style    StyleID
	Text     string
	Children []NodeID
	Image    ImageID
	// Width and Height are the intrinsic or CSS-specified size of a NodeImage,
	// in CSS pixels. Height may be zero if the host knows only one dimension.
	Width  float32
	Height float32
	// Anchor is a convenience for the common one-anchor-per-node case. The
	// builder merges it into Chapter.Anchors; a conflicting or duplicate ID is
	// rejected. Anchor blocks, paragraphs or headings rather than every inline
	// node: each anchor costs one map entry in the Document and in every Layout
	// variant built from it.
	Anchor AnchorID
	// Lang is an optional BCP 47 tag for this node. It overrides the document
	// language for shaping.
	Lang string
	// Link is an optional host URL. It does not affect layout.
	Link string
}

// Chapter is one independently paginated unit, usually one spine item.
type Chapter struct {
	ID    string
	Root  NodeID
	Nodes []Node
	// Anchors maps host anchors to nodes. Offsets within a node are carried by
	// Location.Offset, not by this table. Node.Anchor is merged into this map
	// by the builder.
	//
	// Anchor blocks, paragraphs or headings rather than every inline node: each
	// anchor costs one map entry in the Document and in every Layout variant,
	// so the cost is O(#anchors), not O(#nodes).
	Anchors map[AnchorID]NodeID
}

// Metadata carries document-level hints.
type Metadata struct {
	// Language is a BCP 47 tag such as "zh-CN" or "en".
	Language string
	// Title is informational and not used by layout.
	Title string
	// Reflowable must be true; the engine only supports reflowable documents.
	Reflowable bool
}

// Document is the immutable input to Engine.Layout.
//
// Build documents with DocumentBuilder rather than constructing the struct
// directly: the builder validates node graphs and copies slices.
type Document struct {
	styles   []Style
	chapters []Chapter
	meta     Metadata
}

// Styles returns a copy of the document style table.
func (d *Document) Styles() []Style {
	out := make([]Style, len(d.styles))
	copy(out, d.styles)
	return out
}

// StyleCount returns the number of entries in the style table.
func (d *Document) StyleCount() int { return len(d.styles) }

// ChapterCount returns the number of chapters.
func (d *Document) ChapterCount() int { return len(d.chapters) }

// Chapter returns chapter i. The returned Chapter shares node storage with the
// document and must be treated as read-only.
func (d *Document) Chapter(i int) (Chapter, bool) {
	if i < 0 || i >= len(d.chapters) {
		return Chapter{}, false
	}
	return d.chapters[i], true
}

// TextRun records where one reachable node sits in Document.Text's string.
//
// RuneStart and RuneEnd are rune offsets in Text's returned string. For a
// NodeText they delimit its text; for every other node they are equal, so a
// Location on an image or an empty block still maps to a position. Rune
// offsets are int32 because Limits.MaxTextRunes is far below their range.
type TextRun struct {
	Node               NodeID
	RuneStart, RuneEnd int32
}

// Text returns one chapter's text and the rune range of every reachable node.
//
// Nodes are visited in reading order from Chapter.Root and their text nodes
// are concatenated with no synthetic separators: block, inline, image and
// break nodes are zero-width runs. The returned string and slice are newly
// allocated; a host that searches or copies on every keystroke should cache
// them. Text is O(chapter text + nodes) and is never called by Layout.
func (d *Document) Text(chapter int) (string, []TextRun, bool) {
	ch, ok := d.Chapter(chapter)
	if !ok {
		return "", nil, false
	}

	// Size the string and the run slice in a first pass over reachable nodes,
	// so the builder below never reallocates what may be megabytes of text.
	var byteSize, nodes int
	stack := []NodeID{ch.Root}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if id < 0 || int(id) >= len(ch.Nodes) {
			continue
		}
		nodes++
		n := &ch.Nodes[id]
		if n.Kind == NodeText && n.Text != "" {
			byteSize += len(n.Text)
		}
		for i := len(n.Children) - 1; i >= 0; i-- {
			stack = append(stack, n.Children[i])
		}
	}

	var b strings.Builder
	b.Grow(byteSize)
	runs := make([]TextRun, 0, nodes)
	var offset int32
	stack = append(stack[:0], ch.Root)
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if id < 0 || int(id) >= len(ch.Nodes) {
			continue
		}
		n := &ch.Nodes[id]
		start := offset
		if n.Kind == NodeText && n.Text != "" {
			offset += int32(utf8.RuneCountInString(n.Text))
			b.WriteString(n.Text)
		}
		runs = append(runs, TextRun{Node: id, RuneStart: start, RuneEnd: offset})
		for i := len(n.Children) - 1; i >= 0; i-- {
			stack = append(stack, n.Children[i])
		}
	}
	return b.String(), runs, true
}

// Metadata returns document metadata.
func (d *Document) Metadata() Metadata { return d.meta }

func (d *Document) style(id StyleID) Style {
	if int(id) < len(d.styles) {
		return d.styles[id]
	}
	return Style{}
}

// DocumentBuilder validates and copies chapters into a Document.
//
// The builder rejects input the engine cannot lay out, returning a
// *DocumentError that wraps ErrInvalidDocument. Both graph shape (cycles,
// out-of-range ids, style references, anchors) and style values are checked;
// style floats must be finite, so NaN or an infinity fails the next AddChapter
// or Build with the offending StyleID instead of reaching layout arithmetic.
type DocumentBuilder struct {
	styles []Style
	// styleErr caches the result of validating the current style table.
	// SetStyles cannot return an error (it is a fluent setter), so the failure
	// is deferred to the next AddChapter or Build, which do return one.
	styleErr error
	chapters []Chapter
	meta     Metadata
	ids      map[string]struct{}
}

// NewDocumentBuilder returns an empty builder.
func NewDocumentBuilder() *DocumentBuilder {
	return &DocumentBuilder{
		styles: []Style{{}},
		ids:    make(map[string]struct{}),
	}
}

// SetStyles replaces the style table. Style ID 0 is always valid and denotes
// the zero style; if styles is empty a single zero style is installed.
//
// SetStyles does not return an error, so a non-finite float in a style is
// reported by the next AddChapter or Build as a *DocumentError wrapping
// ErrInvalidDocument, with the style index in DocumentError.Style.
func (b *DocumentBuilder) SetStyles(styles []Style) *DocumentBuilder {
	b.styles = make([]Style, len(styles))
	copy(b.styles, styles)
	if len(b.styles) == 0 {
		b.styles = []Style{{}}
	}
	b.styleErr = validateStyles(b.styles)
	return b
}

// SetMetadata sets document metadata.
func (b *DocumentBuilder) SetMetadata(m Metadata) *DocumentBuilder {
	b.meta = m
	return b
}

// AddChapter validates ch and appends a copy. Chapters must have unique IDs.
func (b *DocumentBuilder) AddChapter(ch Chapter) error {
	if b.styleErr != nil {
		return b.styleErr
	}
	if err := validateChapter(ch, len(b.styles)); err != nil {
		return err
	}
	if _, exists := b.ids[ch.ID]; exists {
		return chapterErrorf(ErrInvalidDocument, ch.ID, "duplicate chapter id")
	}
	cp := cloneChapter(ch)
	if err := mergeNodeAnchors(&cp); err != nil {
		return err
	}
	b.ids[ch.ID] = struct{}{}
	b.chapters = append(b.chapters, cp)
	return nil
}

// Build freezes the builder's contents into a Document.
func (b *DocumentBuilder) Build() (*Document, error) {
	if b.styleErr != nil {
		return nil, b.styleErr
	}
	styles := append([]Style(nil), b.styles...)
	if len(styles) == 0 {
		styles = []Style{{}}
	}
	chapters := make([]Chapter, len(b.chapters))
	for i, ch := range b.chapters {
		if err := validateChapter(ch, len(styles)); err != nil {
			return nil, err
		}
		chapters[i] = cloneChapter(ch)
	}
	return &Document{styles: styles, chapters: chapters, meta: b.meta}, nil
}

func cloneChapter(ch Chapter) Chapter {
	cp := Chapter{ID: ch.ID, Root: ch.Root, Nodes: make([]Node, len(ch.Nodes))}
	for i, n := range ch.Nodes {
		if n.Children != nil {
			n.Children = append([]NodeID(nil), n.Children...)
		}
		cp.Nodes[i] = n
	}
	if len(ch.Anchors) != 0 {
		cp.Anchors = make(map[AnchorID]NodeID, len(ch.Anchors))
		for k, v := range ch.Anchors {
			cp.Anchors[k] = v
		}
	}
	return cp
}

// mergeNodeAnchors folds Node.Anchor into Chapter.Anchors. A node may carry one
// anchor as a convenience; the map remains the single source of truth so
// placement, validation and Locate see the same table.
func mergeNodeAnchors(ch *Chapter) error {
	for i := range ch.Nodes {
		id := ch.Nodes[i].Anchor
		if id == NoAnchor {
			continue
		}
		if ch.Anchors == nil {
			ch.Anchors = make(map[AnchorID]NodeID)
		}
		if existing, ok := ch.Anchors[id]; ok && existing != NodeID(i) {
			return nodeErrorf(ErrInvalidDocument, ch.ID, NodeID(i),
				"anchor %d is already assigned to node %d", id, existing)
		}
		ch.Anchors[id] = NodeID(i)
	}
	return nil
}

const (
	// maxBuilderDepth prevents malicious or accidentally cyclic IR from
	// exhausting the stack while validating.
	maxBuilderDepth = 4096
)

type visitState uint8

const (
	unvisited visitState = iota
	visiting
	visited
)

// validateChapter checks graph shape, style references and anchor targets.
// It is O(nodes) and reports the first problem it finds. Every failure is a
// *DocumentError wrapping ErrInvalidDocument; chapter, node and style context
// is carried in its structured fields, not only in the message.
func validateChapter(ch Chapter, styleCount int) error {
	if len(ch.Nodes) == 0 {
		return chapterErrorf(ErrInvalidDocument, ch.ID, "chapter has no nodes")
	}
	if ch.Root < 0 || int(ch.Root) >= len(ch.Nodes) {
		return chapterErrorf(ErrInvalidDocument, ch.ID, "root %d out of range (%d nodes)", ch.Root, len(ch.Nodes))
	}
	states := make([]visitState, len(ch.Nodes))
	depth := make([]int32, len(ch.Nodes))

	type frame struct {
		node  NodeID
		child int
	}
	stack := make([]frame, 0, 32)
	stack = append(stack, frame{node: ch.Root})
	states[ch.Root] = visiting

	for len(stack) != 0 {
		f := &stack[len(stack)-1]
		n := &ch.Nodes[f.node]
		if int(n.Style) >= styleCount {
			return styleErrorf(ErrInvalidDocument, ch.ID, f.node, n.Style,
				"style out of range (%d styles in document)", styleCount)
		}
		switch n.Kind {
		case NodeBlock, NodeInline:
			if n.Kind == NodeBlock && len(n.Children) == 0 && n.Text != "" {
				return nodeErrorf(ErrInvalidDocument, ch.ID, f.node, "block has text; use a child text node")
			}
			if f.child >= len(n.Children) {
				states[f.node] = visited
				stack = stack[:len(stack)-1]
				continue
			}
			child := n.Children[f.child]
			f.child++
			if child < 0 || int(child) >= len(ch.Nodes) {
				return nodeErrorf(ErrInvalidDocument, ch.ID, f.node, "child %d out of range", child)
			}
			if n.Kind == NodeInline && ch.Nodes[child].Kind == NodeBlock {
				return nodeErrorf(ErrInvalidDocument, ch.ID, child, "block node inside an inline container")
			}
			switch states[child] {
			case visiting:
				return nodeErrorf(ErrInvalidDocument, ch.ID, child, "node graph contains a cycle")
			case visited:
				// Shared subtrees are allowed by the arena representation, but
				// they have a single layout position. Rejecting them keeps the
				// tree semantics unambiguous.
				return nodeErrorf(ErrInvalidDocument, ch.ID, child, "node is referenced more than once")
			}
			if depth[f.node]+1 > maxBuilderDepth {
				return chapterErrorf(ErrInvalidDocument, ch.ID, "node depth exceeds %d", maxBuilderDepth)
			}
			depth[child] = depth[f.node] + 1
			states[child] = visiting
			stack = append(stack, frame{node: child})
		case NodeText, NodeImage, NodeBreak:
			if len(n.Children) != 0 {
				return nodeErrorf(ErrInvalidDocument, ch.ID, f.node, "leaf node has children")
			}
			states[f.node] = visited
			stack = stack[:len(stack)-1]
		default:
			return nodeErrorf(ErrInvalidDocument, ch.ID, f.node, "unknown node kind %d", n.Kind)
		}
	}

	for i := range states {
		if states[i] == unvisited {
			// Unreachable nodes are almost always a compiler bug. Allow them
			// only if they are structurally valid; they do not affect layout.
			if int(ch.Nodes[i].Style) >= styleCount {
				return styleErrorf(ErrInvalidDocument, ch.ID, NodeID(i), ch.Nodes[i].Style,
					"style out of range (%d styles in document)", styleCount)
			}
		}
	}
	root := ch.Nodes[ch.Root]
	if root.Kind == NodeText || root.Kind == NodeImage || root.Kind == NodeBreak {
		return nodeErrorf(ErrInvalidDocument, ch.ID, ch.Root, "root must be a container")
	}
	for id, node := range ch.Anchors {
		if id == NoAnchor {
			return chapterErrorf(ErrInvalidDocument, ch.ID, "anchor 0 is not allowed")
		}
		if node < 0 || int(node) >= len(ch.Nodes) {
			return chapterErrorf(ErrInvalidDocument, ch.ID, "anchor %d points at node %d out of range", id, node)
		}
	}
	return nil
}

// Errors returned by the render package. Use errors.Is to test them.
var (
	ErrInvalidDocument = errors.New("render: invalid document")
	ErrInvalidOptions  = errors.New("render: invalid options")
	ErrNoFonts         = errors.New("render: no fonts")
	ErrLimitExceeded   = errors.New("render: limit exceeded")
	ErrClosed          = errors.New("render: layout closed")
)
