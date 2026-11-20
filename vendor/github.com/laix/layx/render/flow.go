package render

type itemKind uint8

const (
	itemLine itemKind = iota
	itemImage
)

// flowItem is one vertically stacked box in a chapter's content flow: a line
// box or an image. Positions are chapter content coordinates.
type flowItem struct {
	kind itemKind

	para       *paragraph
	start, end int32
	x, y       float32
	width      float32
	avail      float32
	height     float32
	baseline   float32
	last       bool
	align      TextAlign

	paraLine  int32
	paraLines int32

	breakBefore  bool
	breakAfter   bool
	keepTogether bool
	keepGroup    int32
	widows       int
	orphans      int

	node  NodeID
	image ImageID
	link  string
}

// resolveStyle fills document defaults into an already-cascaded style.
func resolveStyle(st Style, typo Typography) Style {
	return inheritStyle(Style{}, st, typo)
}

// inheritStyle applies CSS-like inheritance for typographic fields while
// keeping box properties (margins, break flags) local to the node.
func inheritStyle(parent, child Style, typo Typography) Style {
	out := child
	if out.FontFamily == "" {
		out.FontFamily = parent.FontFamily
	}
	if out.FontWeight == 0 {
		out.FontWeight = parent.FontWeight
	}
	if out.FontSlant == SlantNone {
		out.FontSlant = parent.FontSlant
	}
	if out.FontSize <= 0 {
		out.FontSize = parent.FontSize
	}
	if out.LineHeight <= 0 {
		out.LineHeight = parent.LineHeight
	}
	if out.LetterSpacing == 0 {
		out.LetterSpacing = parent.LetterSpacing
	}
	if out.WordSpacing == 0 {
		out.WordSpacing = parent.WordSpacing
	}
	if !out.Hyphenate {
		out.Hyphenate = parent.Hyphenate
	}
	if out.Color.Transparent() {
		out.Color = parent.Color
	}
	if out.TextAlign == AlignDefault {
		out.TextAlign = parent.TextAlign
	}
	// text-decoration propagates in CSS: an ancestor's decoration paints over
	// descendants and cannot be switched off below it.
	if out.TextDecoration == TextDecorationNone {
		out.TextDecoration = parent.TextDecoration
	}
	if out.TextDecorationColor.Transparent() {
		out.TextDecorationColor = parent.TextDecorationColor
	}
	if out.FirstLineIndent == 0 {
		out.FirstLineIndent = parent.FirstLineIndent
	}

	if out.FontFamily == "" {
		out.FontFamily = typo.FontFamily
	}
	if out.FontWeight == 0 {
		out.FontWeight = 400
	}
	if out.FontSize <= 0 {
		out.FontSize = typo.FontSize
	}
	if out.LineHeight <= 0 {
		out.LineHeight = typo.LineHeight
	}
	if out.LetterSpacing == 0 {
		out.LetterSpacing = typo.LetterSpacing
	}
	if out.WordSpacing == 0 {
		out.WordSpacing = typo.WordSpacing
	}
	// Color deliberately stays unresolved when neither the document nor an
	// ancestor named one: Typography.Color is paint-only, so a resolved style
	// that carried it would freeze the ink into the line boxes (BreakKey
	// excludes colour) and a paint-only Relayout would keep the old ink. Page
	// and tile building substitute the layout's Typography.Color instead.
	if out.TextAlign == AlignDefault || out.TextAlign > AlignJustify {
		out.TextAlign = typo.TextAlign
	}
	return out
}

type tokenKind uint8

const (
	tokenText tokenKind = iota
	tokenImage
	tokenBreak
)

type inlineToken struct {
	kind  tokenKind
	node  NodeID
	style Style
	text  string
	image ImageID
	w, h  float32
	link  string
	lang  string
}

// layoutContext is the per-task state shared by layout tasks. Tasks only read
// these fields; their own textWorker is private.
type layoutContext struct {
	doc  *Document
	typo Typography
	// chapter is the ID of the chapter being planned. It is only used to give
	// the errors raised here a structured chapter context.
	chapter string
}

// flattenInline walks an inline subtree and appends its leaf tokens in document
// order. Blocks are rejected: a block inside an inline context has no
// unambiguous line box.
func (lc *layoutContext) flattenInline(nodes []Node, ids []NodeID, parent Style, parentLang string, out *[]inlineToken) error {
	for _, id := range ids {
		if id < 0 || int(id) >= len(nodes) {
			return nodeErrorf(ErrInvalidDocument, lc.chapter, id, "inline child out of range")
		}
		n := &nodes[id]
		st := lc.inlineStyle(n, parent)
		lang := n.Lang
		if lang == "" {
			lang = parentLang
		}
		switch n.Kind {
		case NodeText:
			if n.Text != "" {
				*out = append(*out, inlineToken{
					kind: tokenText, node: id, style: st,
					text: n.Text, link: n.Link, lang: lang,
				})
			}
		case NodeBreak:
			*out = append(*out, inlineToken{kind: tokenBreak})
		case NodeImage:
			*out = append(*out, inlineToken{
				kind: tokenImage, node: id, style: st,
				image: n.Image, w: n.Width, h: n.Height, link: n.Link, lang: lang,
			})
		case NodeInline:
			if err := lc.flattenInline(nodes, n.Children, st, lang, out); err != nil {
				return err
			}
		case NodeBlock:
			return nodeErrorf(ErrInvalidDocument, lc.chapter, id, "block node inside an inline context")
		default:
			return nodeErrorf(ErrInvalidDocument, lc.chapter, id, "unknown node kind %d", n.Kind)
		}
	}
	return nil
}

// styleFor returns the node's own cascaded style, before inheritance.
func (lc *layoutContext) styleFor(n *Node) Style {
	return lc.doc.style(n.Style)
}

func (lc *layoutContext) inlineStyle(n *Node, parent Style) Style {
	return inheritStyle(parent, lc.styleFor(n), lc.typo)
}

func maxF32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}
