package book

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"unicode"

	"github.com/laix/layx/render"
	"github.com/laix/layx/render/paint"
	"golang.org/x/net/html"
)

// Style IDs in every document this package builds.
const (
	styleBody render.StyleID = 1 + iota
	styleParagraph
	styleH1
	styleH2
	styleH3
	styleH4
	styleH5
	styleH6
	styleQuote
	styleListItem
	styleEmphasis
	styleStrong
)

func documentStyles(base float32) []render.Style {
	ink := paint.Color{R: 0x1a, G: 0x1a, B: 0x1a, A: 0xff}
	styles := make([]render.Style, int(styleStrong)+1)
	styles[styleBody] = render.Style{
		FontFamily: "book",
		FontSize:   base,
		LineHeight: 1.6,
		Color:      ink,
	}
	styles[styleParagraph] = render.Style{
		FirstLineIndent: 2 * base,
		MarginBottom:    0.6 * base,
	}
	headingSizes := [...]float32{1.6, 1.4, 1.25, 1.1, 1.0, 0.95}
	for i, scale := range headingSizes {
		styles[int(styleH1)+i] = render.Style{
			FontSize:     base * scale,
			FontWeight:   700,
			MarginTop:    0.6 * base,
			MarginBottom: 0.6 * base,
			KeepTogether: true,
		}
	}
	styles[styleQuote] = render.Style{
		MarginLeft:   2 * base,
		MarginBottom: 0.6 * base,
	}
	styles[styleListItem] = render.Style{
		MarginLeft:   1.5 * base,
		MarginBottom: 0.3 * base,
	}
	styles[styleEmphasis] = render.Style{FontSlant: render.SlantItalic}
	styles[styleStrong] = render.Style{FontWeight: 700}
	return styles
}

type hnode struct {
	tag      string // "" for text
	text     string
	children []*hnode
}

var voidTags = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true,
	"hr": true, "img": true, "input": true, "link": true, "meta": true,
	"param": true, "source": true, "track": true, "wbr": true,
}

var skippedTags = map[string]bool{
	"head": true, "title": true, "style": true, "script": true,
	"meta": true, "link": true, "base": true, "img": true, "svg": true,
	"math": true, "hr": true,
}

var blockTags = map[string]bool{
	"body": true, "p": true, "div": true, "section": true, "article": true,
	"main": true, "header": true, "footer": true, "aside": true, "nav": true,
	"blockquote": true, "ul": true, "ol": true, "li": true, "dl": true,
	"dt": true, "dd": true, "figure": true, "figcaption": true, "pre": true,
	"table": true, "thead": true, "tbody": true, "tfoot": true, "tr": true,
	"td": true, "th": true, "h1": true, "h2": true, "h3": true, "h4": true,
	"h5": true, "h6": true,
}

var headingStyles = map[string]render.StyleID{
	"h1": styleH1, "h2": styleH2, "h3": styleH3,
	"h4": styleH4, "h5": styleH5, "h6": styleH6,
}

// parseTree tokenizes XHTML into a small element tree. The tokenizer decodes
// entities such as &#160; for us.
func parseTree(data []byte) (*hnode, error) {
	z := html.NewTokenizer(bytes.NewReader(data))
	root := &hnode{tag: "#root"}
	stack := []*hnode{root}
	for {
		switch tt := z.Next(); tt {
		case html.ErrorToken:
			if err := z.Err(); err != nil && !errors.Is(err, io.EOF) {
				return nil, err
			}
			return root, nil
		case html.TextToken:
			text := string(z.Text())
			if text != "" {
				top := stack[len(stack)-1]
				top.children = append(top.children, &hnode{text: text})
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			tag := strings.ToLower(string(name))
			n := &hnode{tag: tag}
			top := stack[len(stack)-1]
			top.children = append(top.children, n)
			if tt == html.StartTagToken && !voidTags[tag] {
				stack = append(stack, n)
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			tag := strings.ToLower(string(name))
			for i := len(stack) - 1; i > 0; i-- {
				if stack[i].tag == tag {
					stack = stack[:i]
					break
				}
			}
		}
	}
}

// convertChapter maps one spine item's XHTML to a render chapter.
func convertChapter(data []byte, id string, anchor render.AnchorID) (render.Chapter, error) {
	tree, err := parseTree(data)
	if err != nil {
		return render.Chapter{}, err
	}
	c := &converter{}
	root := c.add(render.Node{Kind: render.NodeBlock, Style: styleBody, Anchor: anchor})
	c.emit(root, tree.children, false)
	if len(c.nodes) == 1 {
		// Keep an empty chapter valid and visible instead of failing the book.
		c.addText(root, "（本章没有可显示的文本）")
	}
	return render.Chapter{
		ID:      id,
		Root:    root,
		Nodes:   c.nodes,
		Anchors: map[render.AnchorID]render.NodeID{anchor: root},
	}, nil
}

// headingTitle returns the text of the first h1-h3 in the chapter.
func headingTitle(data []byte) string {
	tree, err := parseTree(data)
	if err != nil {
		return ""
	}
	var found *hnode
	var walk func(*hnode) bool
	walk = func(n *hnode) bool {
		if n.tag == "h1" || n.tag == "h2" || n.tag == "h3" {
			found = n
			return true
		}
		for _, child := range n.children {
			if child.tag != "" && walk(child) {
				return true
			}
		}
		return false
	}
	walk(tree)
	if found == nil {
		return ""
	}
	return collapseText(textContent(found))
}

func textContent(n *hnode) string {
	if n.tag == "" {
		return n.text
	}
	var b strings.Builder
	for _, child := range n.children {
		b.WriteString(textContent(child))
	}
	return b.String()
}

func collapseText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

type converter struct {
	nodes []render.Node
}

func (c *converter) add(n render.Node) render.NodeID {
	id := render.NodeID(len(c.nodes))
	c.nodes = append(c.nodes, n)
	return id
}

func (c *converter) addChild(parent render.NodeID, n render.Node) render.NodeID {
	id := c.add(n)
	c.nodes[parent].Children = append(c.nodes[parent].Children, id)
	return id
}

func (c *converter) addText(parent render.NodeID, text string) {
	if text == "" {
		return
	}
	c.addChild(parent, render.Node{Kind: render.NodeText, Text: text})
}

// emit converts children into parent. pending reports a pending word space
// from the previous text run, which is carried across inline boundaries (but
// not across a block boundary).
func (c *converter) emit(parent render.NodeID, kids []*hnode, pending bool) bool {
	for _, n := range kids {
		if n.tag == "" {
			collapsed, leading, trailing := collapseSpace(n.text)
			if collapsed == "" {
				pending = pending || leading || trailing
				continue
			}
			text := collapsed
			if pending {
				text = " " + text
			}
			c.addText(parent, text)
			pending = trailing
			continue
		}
		if skippedTags[n.tag] {
			pending = false
			continue
		}
		if n.tag == "br" {
			c.addChild(parent, render.Node{Kind: render.NodeBreak})
			pending = false
			continue
		}
		if style, ok := headingStyles[n.tag]; ok {
			block := c.addChild(parent, render.Node{Kind: render.NodeBlock, Style: style})
			c.emit(block, n.children, false)
			pending = false
			continue
		}
		if n.tag == "em" || n.tag == "i" || n.tag == "strong" || n.tag == "b" {
			style := styleEmphasis
			if n.tag == "strong" || n.tag == "b" {
				style = styleStrong
			}
			if containsBlock(n) {
				pending = c.emit(parent, n.children, pending)
				continue
			}
			inline := c.addChild(parent, render.Node{Kind: render.NodeInline, Style: style})
			pending = c.emit(inline, n.children, pending)
			continue
		}
		if n.tag == "li" {
			block := c.addChild(parent, render.Node{Kind: render.NodeBlock, Style: styleListItem})
			c.addText(block, "· ")
			c.emit(block, n.children, false)
			pending = false
			continue
		}
		if blockTags[n.tag] {
			style := styleBody
			switch n.tag {
			case "p":
				style = styleParagraph
			case "blockquote":
				style = styleQuote
			}
			block := c.addChild(parent, render.Node{Kind: render.NodeBlock, Style: style})
			c.emit(block, n.children, false)
			pending = false
			continue
		}
		// Everything else is a transparent container.
		pending = c.emit(parent, n.children, pending)
	}
	return pending
}

func containsBlock(n *hnode) bool {
	for _, child := range n.children {
		if child.tag == "" {
			continue
		}
		if blockTags[child.tag] || headingStyles[child.tag] != 0 || containsBlock(child) {
			return true
		}
	}
	return false
}

// collapseSpace collapses runs of white space and reports whether the run had
// a leading or trailing space.
func collapseSpace(s string) (out string, leading, trailing bool) {
	var b strings.Builder
	inSpace := false
	started := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if started {
				inSpace = true
			} else {
				leading = true
			}
			continue
		}
		if inSpace {
			b.WriteByte(' ')
			inSpace = false
		}
		b.WriteRune(r)
		started = true
	}
	return b.String(), leading, inSpace
}
