package book

import (
	"bytes"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode"

	"github.com/laix/layx/render"
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
	styles := make([]render.Style, int(styleStrong)+1)
	// No colour, family or leading here: the body inherits the layout's
	// typography, so a host can turn the page to another paper, another face or
	// another leading without rewriting the document. Only the size is baked in,
	// because the headings are scaled from it.
	styles[styleBody] = render.Style{
		FontSize: base,
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

type hattr struct {
	key, val string
}

type hnode struct {
	tag      string // "" for text
	text     string
	attrs    []hattr
	children []*hnode
}

// attr returns the value of the lower-cased attribute key, or "".
func attr(n *hnode, key string) string {
	for _, a := range n.attrs {
		if a.key == key {
			return a.val
		}
	}
	return ""
}

var voidTags = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true,
	"hr": true, "img": true, "image": true, "input": true, "link": true,
	"meta": true, "param": true, "source": true, "track": true, "wbr": true,
}

// svg is not skipped: emit turns its first image into a NodeImage and drops
// the rest of the vector content, which the render engine does not support.
var skippedTags = map[string]bool{
	"head": true, "title": true, "style": true, "script": true,
	"meta": true, "link": true, "base": true, "math": true, "hr": true,
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
			for {
				key, val, more := z.TagAttr()
				if len(key) > 0 {
					n.attrs = append(n.attrs, hattr{key: strings.ToLower(string(key)), val: string(val)})
				}
				if !more {
					break
				}
			}
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

// ImageResolver maps an href found in the markup, relative to the document
// that contains it, to a host image handle and its intrinsic size. It reports
// false when the image is unavailable.
type ImageResolver func(href string, widthHint, heightHint float32) (render.ImageID, float32, float32, bool)

// convertChapter maps one spine item's XHTML to a render chapter.
func convertChapter(data []byte, id string, anchor render.AnchorID, resolve ImageResolver) (render.Chapter, error) {
	tree, err := parseTree(data)
	if err != nil {
		return render.Chapter{}, err
	}
	c := &converter{resolve: resolve}
	root := c.add(render.Node{Kind: render.NodeBlock, Style: styleBody, Anchor: anchor})
	c.emit(root, tree.children, false)
	if !c.hasContent() {
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
	nodes   []render.Node
	resolve ImageResolver
}

// hasContent reports whether the chapter produced anything visible. Empty
// wrapper elements alone (a body and a div around a dropped svg, say) do not
// count, or the page would be silent blank.
func (c *converter) hasContent() bool {
	for i := range c.nodes {
		n := &c.nodes[i]
		switch n.Kind {
		case render.NodeImage:
			return true
		case render.NodeText:
			if strings.TrimSpace(n.Text) != "" {
				return true
			}
		}
	}
	return false
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
		if n.tag == "img" {
			c.addImage(parent, attr(n, "src"), parsePixels(attr(n, "width")), parsePixels(attr(n, "height")))
			pending = false
			continue
		}
		if n.tag == "svg" {
			href, w, h := svgImage(n)
			c.addImage(parent, href, w, h)
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

func (c *converter) addImage(parent render.NodeID, href string, widthHint, heightHint float32) {
	if href == "" || c.resolve == nil {
		return
	}
	id, w, h, ok := c.resolve(href, widthHint, heightHint)
	if !ok {
		return
	}
	c.addChild(parent, render.Node{Kind: render.NodeImage, Image: id, Width: w, Height: h})
}

// svgImage returns the first image an inline SVG shows, which is how calibre
// and most converters wrap a cover. Vector content itself is not rendered.
func svgImage(n *hnode) (href string, width, height float32) {
	img := findTag(n, "image")
	if img == nil {
		img = findTag(n, "img")
	}
	if img != nil {
		href = firstAttr(img, "xlink:href", "href", "src")
		width = parsePixels(attr(img, "width"))
		height = parsePixels(attr(img, "height"))
	}
	if width <= 0 || height <= 0 {
		if vw, vh := viewBoxSize(attr(n, "viewbox")); vw > 0 && vh > 0 {
			if width <= 0 {
				width = vw
			}
			if height <= 0 {
				height = vh
			}
		}
	}
	return href, width, height
}

func findTag(n *hnode, tag string) *hnode {
	for _, child := range n.children {
		if child.tag == tag {
			return child
		}
		if found := findTag(child, tag); found != nil {
			return found
		}
	}
	return nil
}

func firstAttr(n *hnode, keys ...string) string {
	for _, key := range keys {
		if v := attr(n, key); v != "" {
			return v
		}
	}
	return ""
}

// parsePixels reads a CSS length in pixels. Percentages and other units are
// unknown here, so the intrinsic image size wins.
func parsePixels(s string) float32 {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.TrimSuffix(s, "px")
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 32)
	if err != nil || v <= 0 {
		return 0
	}
	return float32(v)
}

func viewBoxSize(s string) (width, height float32) {
	fields := strings.Fields(strings.ReplaceAll(s, ",", " "))
	if len(fields) != 4 {
		return 0, 0
	}
	w, err1 := strconv.ParseFloat(fields[2], 32)
	h, err2 := strconv.ParseFloat(fields[3], 32)
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return 0, 0
	}
	return float32(w), float32(h)
}

// firstImageHref returns the first image href the markup shows, or "".
func firstImageHref(data []byte) string {
	tree, err := parseTree(data)
	if err != nil {
		return ""
	}
	var found string
	var walk func(*hnode) bool
	walk = func(n *hnode) bool {
		switch n.tag {
		case "img":
			if src := attr(n, "src"); src != "" {
				found = src
				return true
			}
			return false
		case "svg":
			if href, _, _ := svgImage(n); href != "" {
				found = href
				return true
			}
			return false
		case "head", "title", "style", "script", "meta", "link", "base":
			return false
		}
		for _, child := range n.children {
			if child.tag != "" && walk(child) {
				return true
			}
		}
		return false
	}
	walk(tree)
	return found
}

// imageOnly reports whether the markup shows images and no text at all, the
// shape of a converted cover page.
func imageOnly(data []byte) bool {
	tree, err := parseTree(data)
	if err != nil {
		return false
	}
	text, images := 0, 0
	var walk func(*hnode)
	walk = func(n *hnode) {
		if n.tag == "" {
			if strings.TrimSpace(n.text) != "" {
				text++
			}
			return
		}
		switch n.tag {
		case "head", "title", "style", "script", "meta", "link", "base":
			return
		case "img":
			images++
			return
		case "svg":
			if href, _, _ := svgImage(n); href != "" {
				images++
			}
			return
		}
		for _, child := range n.children {
			walk(child)
		}
	}
	walk(tree)
	return text == 0 && images > 0
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
