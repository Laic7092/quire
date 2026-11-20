package epub

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/laix/layx/render"
	"github.com/laix/layx/render/paint"
	"golang.org/x/net/html"
)

// DefaultLinkColor is the colour of an in-book or external link when the host
// does not choose one: the blue browsers and reading apps give links.
var DefaultLinkColor = render.DefaultLinkColor

// Options are the choices a host makes when it turns a book into a document.
type Options struct {
	// LinkColor paints link text; the underline is drawn in the same colour.
	// A transparent or zero colour uses DefaultLinkColor. Author CSS can
	// override it.
	LinkColor paint.Color

	// ThemeLinks leaves link text in the reader's colour: the user-agent sheet
	// underlines links but names no colour, so render.Typography.LinkColor
	// paints them. A reader that changes its paper repaints links without
	// rebuilding the document, which is why a reader with themes wants this.
	// LinkColor is then unused, and author CSS that names a link colour still
	// wins.
	ThemeLinks bool

	// RuleColor paints rules and borders that name no colour of their own,
	// such as an <hr> in a book whose CSS never mentions it. A transparent
	// value uses DefaultRuleColor.
	RuleColor paint.Color

	// IgnoreColor drops author `color` declarations, so ink stays whatever
	// the host's Typography and theme decide. Hosts with a night or sepia
	// palette should set this: PG's boilerplate says `color: black` on body.
	IgnoreColor bool

	// ContainerWidth is the text column width in CSS pixels, used to resolve
	// percentage margins, padding and text-indent. Zero means "unknown":
	// percentages then resolve to zero instead of a guess.
	ContainerWidth float32

	// OnIssue receives one diagnostic for every piece of content the converter
	// or the CSS cascade could not represent: a linearized <table>, an image
	// whose bytes are missing, a flattened <ruby> annotation, an ignored
	// @font-face rule, a stylesheet that could not be read. The alternative is
	// the silent degradation the library used to have; a host that wants to
	// explain "this page lost something" should set this.
	//
	// OnIssue is called once per issue from Build/BuildWith, after the
	// conversion has finished and before the call returns, on the calling
	// goroutine, so the callback never runs concurrently with itself. A book
	// with nothing to report never calls it. The same list, including issues
	// found while the container was opened, is available afterwards from
	// Book.Issues; OnIssue is only the push form of that list.
	OnIssue func(render.Issue)
}

func (o Options) linkColor() paint.Color {
	if o.ThemeLinks {
		return paint.Color{}
	}
	if o.LinkColor.Transparent() {
		return DefaultLinkColor
	}
	return o.LinkColor
}

func (o Options) ruleColor() paint.Color {
	if o.RuleColor.Transparent() {
		return DefaultRuleColor
	}
	return o.RuleColor
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
	"meta": true, "link": true, "base": true, "math": true,
}

// unsupportedContentTags are elements whose media or control content the IR
// cannot express. They are reported as dropped content; any fallback children
// (the text inside <video> or <iframe>) are still emitted as plain text.
var unsupportedContentTags = map[string]bool{
	"audio": true, "video": true, "canvas": true, "iframe": true,
	"object": true, "embed": true, "input": true, "select": true,
	"textarea": true,
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

// convertChapter maps one spine item's XHTML to a render chapter. anchors maps
// element ids (navigation targets and in-book link targets) to render anchors;
// base is the chapter's archive path, used to resolve relative hrefs. style is
// the chapter's cascade and styles the document-wide dedupe table, shared by
// every chapter of one Build. issues, when not nil, receives the content the
// converter had to drop or flatten; the resulting Issue.Chapter is id and the
// detail starts with base. The converter installs each anchor on the first node
// the element produces, or falls back to the chapter root when the element has
// no visible content.
func convertChapter(data []byte, id string, anchor render.AnchorID, resolve ImageResolver, anchors map[string]render.AnchorID, base string, style *cascade, styles *styleRegistry, issues *issueCollector) (render.Chapter, error) {
	tree, err := parseTree(data)
	if err != nil {
		return render.Chapter{}, err
	}
	return convertTree(tree, id, anchor, resolve, anchors, base, style, styles, issues)
}

// convertTree is convertChapter for callers that already parsed the markup
// (Build does, to find stylesheet links).
func convertTree(tree *hnode, id string, anchor render.AnchorID, resolve ImageResolver, anchors map[string]render.AnchorID, base string, style *cascade, styles *styleRegistry, issues *issueCollector) (render.Chapter, error) {
	if style == nil {
		style = newCascade(18, Options{}, nil)
	}
	if styles == nil {
		styles = newStyleRegistry()
	}
	c := &converter{
		resolve:  resolve,
		anchors:  anchors,
		base:     base,
		chapter:  id,
		cascade:  style,
		styles:   styles,
		issues:   issues,
		usedAnch: map[render.AnchorID]bool{},
	}
	rootComputed := c.cascade.root()
	root := c.add(render.Node{Kind: render.NodeBlock, Style: c.styleOf(rootComputed), Anchor: anchor})

	body, path := findBody(tree)
	if body == nil {
		c.emit(root, tree.children, false, rootComputed)
	} else {
		// Replay the ancestor chain so descendant selectors such as
		// `html body p` match, and body declarations land on the body block.
		cur := rootComputed
		for i := 1; i < len(path); i++ {
			cur = c.cascade.compute(path[i].node, cur, path[:i+1])
		}
		c.path = append(matchPath(nil), path...)
		if cur.display != displayNone {
			block := c.container(root, render.NodeBlock, c.styleOf(cur), body)
			c.emit(block, body.children, false, cur)
		}
	}

	if !c.hasContent() {
		// Keep an empty chapter valid and visible instead of failing the book.
		c.addText(root, "（本章没有可显示的文本）")
	}
	locations := map[render.AnchorID]render.NodeID{anchor: root}
	used := make(map[render.AnchorID]bool, len(anchors))
	for i := range c.nodes {
		if a := c.nodes[i].Anchor; a != 0 {
			used[a] = true
		}
	}
	for _, a := range anchors {
		if a != 0 && !used[a] {
			// The target element is empty or vanished during conversion: keep
			// the row clickable by landing at the chapter start.
			locations[a] = root
		}
	}
	return render.Chapter{
		ID:      id,
		Root:    root,
		Nodes:   c.nodes,
		Anchors: locations,
	}, nil
}

// findBody returns the <body> element and the ancestor path that ends at it.
func findBody(root *hnode) (*hnode, matchPath) {
	var found *hnode
	var foundPath matchPath
	var walk func(n *hnode, path matchPath)
	walk = func(n *hnode, path matchPath) {
		if found != nil {
			return
		}
		if n.tag == "body" {
			found, foundPath = n, path
			return
		}
		for i, child := range n.children {
			if child.tag == "" {
				continue
			}
			next := make(matchPath, len(path)+1)
			copy(next, path)
			next[len(path)] = matchFrame{node: child, index: i}
			walk(child, next)
		}
	}
	walk(root, matchPath{{node: root}})
	return found, foundPath
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
	nodes     []render.Node
	resolve   ImageResolver
	anchors   map[string]render.AnchorID
	base      string                   // archive path of this chapter, for hrefs
	chapter   string                   // render chapter ID, used in diagnostics
	cascade   *cascade                 // user-agent + author styles for this chapter
	styles    *styleRegistry           // document-wide style table
	issues    *issueCollector          // dropped-content diagnostics, nil-safe
	path      matchPath                // ancestor chain ending at the element being emitted
	lists     []listState              // open <ol>/<ul> counters
	carry     render.AnchorID          // anchor waiting for the first node of a wrapper
	carryLink string                   // link waiting for the first node of an <a>
	usedAnch  map[render.AnchorID]bool // anchors already installed on a node
}

// dropf reports content the converter could not represent. The detail starts
// with the chapter's archive path so a host can point the reader at the page
// that lost something; node is the render node the dropped content maps to, or
// InvalidNode when nothing was produced for it. It is nil-safe and formats
// nothing when no collector is attached (the unit-test path).
func (c *converter) dropf(node render.NodeID, format string, args ...any) {
	if c.issues == nil {
		return
	}
	detail := fmt.Sprintf(format, args...)
	if c.base != "" {
		detail = c.base + ": " + detail
	}
	c.issues.add(render.IssueDroppedContent, c.chapter, node, "%s", detail)
}

type listState struct {
	item int
}

// anchorFor returns the render anchor installed on the element's id (or, for
// legacy <a name>, its name), or 0 when it is not a navigation target. Only the
// first element with a given id takes the anchor: books do contain duplicate
// ids, and installing one anchor twice would make the document invalid.
func (c *converter) anchorFor(n *hnode) render.AnchorID {
	if len(c.anchors) == 0 || n == nil {
		return 0
	}
	if id := attr(n, "id"); id != "" {
		if a, ok := c.anchors[id]; ok {
			return c.takeAnchor(a)
		}
	}
	if n.tag == "a" {
		if name := attr(n, "name"); name != "" {
			if a, ok := c.anchors[name]; ok {
				return c.takeAnchor(a)
			}
		}
	}
	return 0
}

// takeAnchor marks a as used and returns it, or 0 when another node already
// took it.
func (c *converter) takeAnchor(a render.AnchorID) render.AnchorID {
	if a == 0 {
		return 0
	}
	if c.usedAnch == nil {
		c.usedAnch = map[render.AnchorID]bool{}
	}
	if c.usedAnch[a] {
		return 0
	}
	c.usedAnch[a] = true
	return a
}

// linkFor returns the Node.Link value of an <a href>: external URLs keep their
// exact spelling, in-book targets are resolved to an archive path plus
// fragment. A link is attached to the first node the anchor wraps.
func (c *converter) linkFor(n *hnode) string {
	if n == nil || n.tag != "a" {
		return ""
	}
	href := strings.TrimSpace(attr(n, "href"))
	if href == "" {
		return ""
	}
	if isExternalHref(href) {
		return href
	}
	p, frag := splitHref(c.base, href)
	if frag != "" {
		return p + "#" + frag
	}
	return p
}

// withTarget installs an anchor and/or a link on the first node fn creates,
// unless that node names its own. It restores the previous pending values so an
// empty wrapper cannot leak them onto a following sibling.
func (c *converter) withTarget(anchor render.AnchorID, link string, fn func()) {
	if anchor == 0 && link == "" {
		fn()
		return
	}
	oldAnchor, oldLink := c.carry, c.carryLink
	if anchor != 0 {
		c.carry = anchor
	}
	if link != "" {
		c.carryLink = link
	}
	fn()
	c.carry, c.carryLink = oldAnchor, oldLink
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

// styleOf registers a computed style and returns its document-wide ID.
func (c *converter) styleOf(cs computed) render.StyleID {
	return c.styles.add(c.cascade.renderStyle(cs))
}

func (c *converter) add(n render.Node) render.NodeID {
	if n.Anchor == 0 && c.carry != 0 {
		n.Anchor, c.carry = c.carry, 0
	} else if n.Anchor != 0 {
		c.carry = 0
	}
	// A link belongs to the run of text it paints: render turns Node.Link into
	// hit rectangles from text and image tokens, not from containers. An
	// inline <a> still gets its own style node; the URL lands on the first leaf
	// inside it.
	if n.Link == "" && c.carryLink != "" && (n.Kind == render.NodeText || n.Kind == render.NodeImage || n.Kind == render.NodeBreak) {
		n.Link, c.carryLink = c.carryLink, ""
	}
	id := render.NodeID(len(c.nodes))
	c.nodes = append(c.nodes, n)
	return id
}

func (c *converter) addChild(parent render.NodeID, n render.Node) render.NodeID {
	id := c.add(n)
	c.nodes[parent].Children = append(c.nodes[parent].Children, id)
	return id
}

// container adds a block or inline node for an element, carrying its anchor.
// Links stay pending for the first leaf inside: render only derives hit
// rectangles from text and image tokens.
func (c *converter) container(parent render.NodeID, kind render.NodeKind, style render.StyleID, n *hnode) render.NodeID {
	var id render.NodeID
	c.withTarget(c.anchorFor(n), "", func() {
		id = c.addChild(parent, render.Node{Kind: kind, Style: style})
	})
	return id
}

// withLink makes n's href pending while fn emits its subtree, so the first
// text or image leaf inside receives it.
func (c *converter) withLink(n *hnode, fn func()) {
	link := c.linkFor(n)
	if link == "" {
		fn()
		return
	}
	old := c.carryLink
	c.carryLink = link
	fn()
	c.carryLink = old
}

func (c *converter) addText(parent render.NodeID, text string) {
	if text == "" {
		return
	}
	c.addChild(parent, render.Node{Kind: render.NodeText, Text: text})
}

// emit converts children into parent. pending reports a pending word space
// from the previous text run, which is carried across inline boundaries (but
// not across a block boundary). cur is the computed style of the element that
// owns kids.
func (c *converter) emit(parent render.NodeID, kids []*hnode, pending bool, cur computed) bool {
	for i, n := range kids {
		if n.tag == "" {
			if cur.pre {
				pending = false
				c.addPreformatted(parent, n.text)
				continue
			}
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
			if n.tag == "math" {
				c.dropf(render.InvalidNode, "<math> MathML content is not supported by the render IR; the formula is dropped")
			}
			pending = false
			continue
		}
		depth := len(c.path)
		c.path = append(c.path, matchFrame{node: n, index: i})
		cs := c.cascade.compute(n, cur, c.path)
		pending = c.emitElement(parent, n, cs, pending)
		c.path = c.path[:depth]
	}
	return pending
}

// emitElement adds one element (and its children) to parent.
func (c *converter) emitElement(parent render.NodeID, n *hnode, cs computed, pending bool) bool {
	if cs.display == displayNone {
		return false
	}
	switch n.tag {
	case "br":
		c.addChild(parent, render.Node{Kind: render.NodeBreak, Anchor: c.anchorFor(n)})
		return false
	case "img":
		c.addImage(parent, "img", attr(n, "src"), parsePixels(attr(n, "width")), parsePixels(attr(n, "height")), c.anchorFor(n))
		return false
	case "image":
		// A bare SVG <image> that is not inside an <svg> element: the vector
		// wrapper is gone, but the raster reference still names an image.
		c.addImage(parent, "image", firstAttr(n, "xlink:href", "href", "src"), parsePixels(attr(n, "width")), parsePixels(attr(n, "height")), c.anchorFor(n))
		return false
	case "svg":
		href, w, h := svgImage(n)
		if href == "" {
			c.dropf(render.InvalidNode, "<svg> has no <image> raster and vector drawing is not supported; the svg is dropped")
			return false
		}
		c.addImage(parent, "svg", href, w, h, c.anchorFor(n))
		return false
	case "ruby":
		// The children are still emitted, so no text is lost, but the reading
		// annotation stops being ruby: it becomes plain inline text.
		c.dropf(render.InvalidNode, "<ruby> annotation is flattened into the running text; ruby layout is not represented")
	case "hr":
		// An empty block with a bottom border: render draws it as a rule, and
		// the margins come from the UA sheet or the book's own CSS.
		c.container(parent, render.NodeBlock, c.styleOf(cs), n)
		return false
	case "table":
		block := c.container(parent, render.NodeBlock, c.styleOf(cs), n)
		c.dropf(block, "<table> linearized into one block per row; columns, column widths and cell spans are not represented")
		c.withLink(n, func() { c.emitTable(block, n, cs) })
		return false
	}
	if unsupportedContentTags[n.tag] {
		c.dropf(render.InvalidNode, "<%s> media or control content is not rendered; any fallback text is kept", n.tag)
		return c.emit(parent, n.children, pending, cs)
	}
	if n.tag == "pre" {
		stripPreNewline(n)
	}
	if n.tag == "ol" || n.tag == "ul" {
		c.lists = append(c.lists, listState{})
		defer func() { c.lists = c.lists[:len(c.lists)-1] }()
	}
	if n.tag == "li" {
		block := c.container(parent, render.NodeBlock, c.styleOf(cs), n)
		marker := c.listMarker(cs)
		c.withLink(n, func() {
			c.addText(block, marker)
			c.emit(block, n.children, false, cs)
		})
		return false
	}
	if cs.display == displayBlock || cs.display == displayListItem || cs.display == displayTable {
		block := c.container(parent, render.NodeBlock, c.styleOf(cs), n)
		c.withLink(n, func() { c.emit(block, n.children, false, cs) })
		return false
	}
	// Inline or unknown: a wrapper only needs its own node when the cascade
	// changed something. Otherwise its children keep inheriting the parent
	// run, which is what makes plain <span> and <a> cheap.
	if !cs.changed || containsBlock(n) {
		c.withTarget(c.anchorFor(n), c.linkFor(n), func() {
			pending = c.emit(parent, n.children, pending, cs)
		})
		return pending
	}
	inline := c.container(parent, render.NodeInline, c.styleOf(cs), n)
	c.withLink(n, func() { pending = c.emit(inline, n.children, pending, cs) })
	return pending
}

// addPreformatted writes text with its whitespace intact, turning newlines
// into forced breaks: render has no white-space property of its own.
func (c *converter) addPreformatted(parent render.NodeID, text string) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, "\t", "    ")
	for i, part := range strings.Split(text, "\n") {
		if i > 0 {
			c.addChild(parent, render.Node{Kind: render.NodeBreak})
		}
		if part != "" {
			c.addText(parent, part)
		}
	}
}

// stripPreNewline drops the single newline HTML ignores right after <pre>.
func stripPreNewline(n *hnode) {
	for _, child := range n.children {
		if child.tag != "" {
			return
		}
		switch {
		case strings.HasPrefix(child.text, "\r\n"):
			child.text = child.text[2:]
		case strings.HasPrefix(child.text, "\n"):
			child.text = child.text[1:]
		}
		return
	}
}

// listMarker returns the marker text of one <li>, advancing the enclosing
// list's counter. Ordered styles count; bullets do not.
func (c *converter) listMarker(cs computed) string {
	index := 1
	if len(c.lists) > 0 {
		// Count every item, including one whose own list-style-type hides its
		// marker: CSS counters do not skip it either.
		c.lists[len(c.lists)-1].item++
		index = c.lists[len(c.lists)-1].item
	}
	switch cs.listStyle {
	case listNone:
		return ""
	case listDisc:
		return "• "
	case listCircle:
		return "◦ "
	case listSquare:
		return "▪ "
	}
	return listLabel(cs.listStyle, index) + ". "
}

func listLabel(style listStyle, n int) string {
	switch style {
	case listLowerAlpha:
		return alphaLabel(n, false)
	case listUpperAlpha:
		return alphaLabel(n, true)
	case listLowerRoman:
		return strings.ToLower(romanLabel(n))
	case listUpperRoman:
		return romanLabel(n)
	}
	return strconv.Itoa(n)
}

func alphaLabel(n int, upper bool) string {
	if n <= 0 {
		return ""
	}
	base := 'a'
	if upper {
		base = 'A'
	}
	var out []byte
	for n > 0 {
		n--
		out = append([]byte{byte(base + rune(n%26))}, out...)
		n /= 26
	}
	return string(out)
}

func romanLabel(n int) string {
	if n <= 0 || n > 3999 {
		return strconv.Itoa(n)
	}
	values := []struct {
		value int
		text  string
	}{
		{1000, "M"}, {900, "CM"}, {500, "D"}, {400, "CD"},
		{100, "C"}, {90, "XC"}, {50, "L"}, {40, "XL"},
		{10, "X"}, {9, "IX"}, {5, "V"}, {4, "IV"}, {1, "I"},
	}
	var b strings.Builder
	for _, v := range values {
		for n >= v.value {
			b.WriteString(v.text)
			n -= v.value
		}
	}
	return b.String()
}

// emitTable linearizes a table into blocks that render's IR can express:
// one block per row, cells joined by a visible bar, header cells bold. Column
// widths and row/col spans are not represented; real grid layout needs a new
// node model in render (see the design notes in docs/design.md).
func (c *converter) emitTable(parent render.NodeID, n *hnode, cur computed) {
	for i, child := range n.children {
		if child.tag == "" {
			// Stray text between rows is usually indentation, which carries no
			// meaning. Text that a reader would see is content, and is reported.
			if strings.TrimSpace(child.text) != "" {
				c.dropf(render.InvalidNode, "text directly inside <%s> is not in a cell; dropped", n.tag)
			}
			continue
		}
		depth := len(c.path)
		c.path = append(c.path, matchFrame{node: child, index: i})
		cs := c.cascade.compute(child, cur, c.path)
		if cs.display == displayNone {
			c.path = c.path[:depth]
			continue
		}
		switch child.tag {
		case "caption":
			block := c.container(parent, render.NodeBlock, c.styleOf(cs), child)
			c.emit(block, child.children, false, cs)
		case "thead", "tbody", "tfoot":
			c.emitTable(parent, child, cs)
		case "tr":
			row := c.container(parent, render.NodeBlock, c.styleOf(cs), child)
			c.emitRow(row, child, cs)
		case "colgroup", "col":
			// Column hints cannot be honoured without a grid.
			c.dropf(render.InvalidNode, "<%s> column hints are not represented without a table grid; widths and spans are ignored", child.tag)
		default:
			// A wrapper such as <div> inside <table> is transparent.
			c.emitTable(parent, child, cs)
		}
		c.path = c.path[:depth]
	}
}

func (c *converter) emitRow(row render.NodeID, tr *hnode, cur computed) {
	wrote := false
	for i, cell := range tr.children {
		if cell.tag != "td" && cell.tag != "th" {
			if cell.tag == "" {
				if strings.TrimSpace(cell.text) != "" {
					c.dropf(render.InvalidNode, "text directly inside <tr> is not in a cell; dropped")
				}
			} else {
				c.dropf(render.InvalidNode, "<%s> directly inside <tr> is not a table cell; its content is dropped", cell.tag)
			}
			continue
		}
		depth := len(c.path)
		c.path = append(c.path, matchFrame{node: cell, index: i})
		cs := c.cascade.compute(cell, cur, c.path)
		if cs.display == displayNone {
			c.path = c.path[:depth]
			continue
		}
		for _, span := range [...]string{"colspan", "rowspan"} {
			if v := attr(cell, span); v != "" && v != "1" {
				c.dropf(render.InvalidNode, "<%s %s=%q> cell spans are not represented; the cell is linearized in place", cell.tag, span, v)
			}
		}
		if wrote {
			c.addText(row, " ｜ ")
		}
		wrote = true
		if cell.tag == "th" {
			if containsBlock(cell) {
				block := c.container(row, render.NodeBlock, c.styleOf(cs), cell)
				c.emit(block, cell.children, false, cs)
			} else {
				inline := c.container(row, render.NodeInline, c.styleOf(cs), cell)
				c.emit(inline, cell.children, false, cs)
			}
		} else {
			c.withTarget(c.anchorFor(cell), "", func() {
				c.emit(row, cell.children, false, cs)
			})
		}
		c.path = c.path[:depth]
	}
}

func (c *converter) addImage(parent render.NodeID, tag, href string, widthHint, heightHint float32, anchor render.AnchorID) {
	href = strings.TrimSpace(href)
	if href == "" {
		c.dropf(render.InvalidNode, "<%s> names no image source; the image is dropped", tag)
		return
	}
	if c.resolve == nil {
		c.dropf(render.InvalidNode, "<%s src=%q>: this build has no image resolver; the image is dropped", tag, href)
		return
	}
	id, w, h, ok := c.resolve(href, widthHint, heightHint)
	if !ok {
		c.dropf(render.InvalidNode, "<%s src=%q>: image bytes are missing, unreadable, or not a decodable image; the image is dropped", tag, href)
		return
	}
	c.addChild(parent, render.Node{Kind: render.NodeImage, Image: id, Width: w, Height: h, Anchor: anchor})
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
		if blockTags[child.tag] || containsBlock(child) {
			return true
		}
	}
	return false
}

// collapseSpace collapses runs of white space and reports whether the run had
// a leading or trailing space.
//
// Text nodes are most of what a book is made of, and the usual node is already
// collapsed: a paragraph whose white space consists of single spaces, or of the
// indentation and newlines the markup puts around it. Those are answered with a
// slice of s rather than a copy built rune by rune, which is 18 MB of resident
// heap for a 6.7 MB novel and grows with the book.
func collapseSpace(s string) (out string, leading, trailing bool) {
	if lo, hi, ok := collapsedSpan(s); ok {
		return s[lo:hi], lo > 0, hi < len(s)
	}
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

// collapsedSpan reports the byte range of s that collapsing keeps, and whether
// that range is what collapsing produces because it has nothing to rewrite:
// white space may lead, trail, or sit inside as single spaces, and nothing else.
// Invalid UTF-8 is rejected: the rune-by-rune path replaces every stray byte
// with U+FFFD, so the two would not agree.
func collapsedSpan(s string) (lo, hi int, ok bool) {
	hi = len(s)
	for lo < hi {
		r, n := utf8.DecodeRuneInString(s[lo:hi])
		if r == utf8.RuneError && n <= 1 {
			return 0, 0, false
		}
		if !unicode.IsSpace(r) {
			break
		}
		lo += n
	}
	for hi > lo {
		r, n := utf8.DecodeLastRuneInString(s[lo:hi])
		if !unicode.IsSpace(r) {
			break
		}
		hi -= n
	}
	prevSpace := false
	for i := lo; i < hi; {
		r, n := utf8.DecodeRuneInString(s[i:hi])
		if r == utf8.RuneError && n <= 1 {
			return 0, 0, false
		}
		if unicode.IsSpace(r) {
			// A tab, a newline or a second space in a row has to be rewritten.
			if r != ' ' || prevSpace {
				return 0, 0, false
			}
			prevSpace = true
		} else {
			prevSpace = false
		}
		i += n
	}
	return lo, hi, true
}
