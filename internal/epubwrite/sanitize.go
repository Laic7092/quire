package epubwrite

import (
	"fmt"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// allowedElements are the HTML elements that survive sanitization. They all
// have an unambiguous XHTML spelling and are rendered by layx/epub.
var allowedElements = map[string]bool{
	"p": true, "div": true, "br": true, "a": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"blockquote": true,
	"ul":         true, "ol": true, "li": true,
	"em": true, "strong": true, "i": true, "b": true, "u": true, "s": true,
	"code": true, "pre": true,
	"ruby": true, "rt": true, "rp": true, "span": true,
}

// droppedElements are removed together with their content. Scripts and styles
// are executable or global; frames, plugins and form controls would either
// need the network or invite input inside a book.
var droppedElements = map[string]bool{
	"script": true, "style": true, "iframe": true, "frame": true, "frameset": true,
	"object": true, "embed": true, "applet": true, "param": true,
	"form": true, "input": true, "button": true, "select": true, "option": true,
	"optgroup": true, "textarea": true, "label": true, "fieldset": true,
	"legend": true, "datalist": true, "output": true, "progress": true,
	"meter": true, "base": true, "link": true, "meta": true,
}

// sanitizeFragment turns a source's chapter HTML fragment into well-formed
// XHTML body content: unknown wrappers are unwrapped, media is replaced by its
// alternate text, and every attribute except a safe link href is dropped.
func sanitizeFragment(fragment string) (string, error) {
	context := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(strings.NewReader(fragment), context)
	if err != nil {
		return "", fmt.Errorf("parse html: %w", err)
	}
	var b strings.Builder
	for _, n := range nodes {
		renderNode(&b, n)
	}
	return b.String(), nil
}

// renderNode appends one parsed node to the output, escaping text as it goes.
func renderNode(b *strings.Builder, n *html.Node) {
	switch n.Type {
	case html.TextNode:
		escapeInto(b, n.Data)
	case html.ElementNode:
		tag := strings.ToLower(n.Data)
		if droppedElements[tag] {
			return
		}
		switch tag {
		case "img", "image":
			// Keep what a reader would otherwise lose: the alternate text.
			escapeInto(b, imageAlt(n))
			return
		case "br":
			b.WriteString("<br/>")
			return
		}
		if !allowedElements[tag] {
			// Unwrap anything we do not know, so an odd wrapper around real
			// paragraphs does not cost the paragraphs.
			renderChildren(b, n)
			return
		}
		if tag == "a" {
			renderLink(b, n)
			return
		}
		b.WriteString("<" + tag + ">")
		renderChildren(b, n)
		b.WriteString("</" + tag + ">")
	}
}

// renderLink keeps an anchor's text and only a harmless href. Anything that
// could execute script (including entity-obfuscated forms, which the HTML
// parser has already decoded) becomes plain text.
func renderLink(b *strings.Builder, n *html.Node) {
	href := safeHref(attrValue(n, "href"))
	if href == "" {
		renderChildren(b, n)
		return
	}
	b.WriteString(`<a href="`)
	escapeInto(b, href)
	b.WriteString(`">`)
	renderChildren(b, n)
	b.WriteString("</a>")
}

func renderChildren(b *strings.Builder, n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		renderNode(b, c)
	}
}

func attrValue(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

// imageAlt returns the text that replaces a dropped image.
func imageAlt(n *html.Node) string {
	for _, key := range []string{"alt", "title"} {
		if v := strings.TrimSpace(attrValue(n, key)); v != "" {
			return v
		}
	}
	return ""
}

// safeHref rejects executable and embedded-document URLs; everything else,
// including relative links, is kept.
func safeHref(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	var scheme strings.Builder
	for _, r := range strings.ToLower(s) {
		if r == ':' {
			break
		}
		if r <= ' ' {
			// Browsers ignore whitespace and control characters inside a
			// scheme, so "java\tscript:" must be treated as "javascript:".
			continue
		}
		scheme.WriteRune(r)
	}
	switch scheme.String() {
	case "javascript", "vbscript", "data":
		return ""
	}
	return s
}

// escape escapes XML text and attribute values, normalizes non-breaking
// spaces, and drops code points XML 1.0 cannot represent.
func escape(s string) string {
	var b strings.Builder
	escapeInto(&b, s)
	return b.String()
}

func escapeInto(b *strings.Builder, s string) {
	for _, r := range s {
		switch {
		case r == '&':
			b.WriteString("&amp;")
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r == '"':
			b.WriteString("&quot;")
		case r == '\'':
			b.WriteString("&#39;")
		case r == '\u00a0':
			b.WriteString("&#160;")
		case validXMLRune(r):
			b.WriteRune(r)
		}
	}
}

// validXMLRune reports whether r is allowed in an XML 1.0 document.
func validXMLRune(r rune) bool {
	switch {
	case r == '\t' || r == '\n' || r == '\r':
		return true
	case r >= 0x20 && r <= 0xd7ff:
		return true
	case r >= 0xe000 && r <= 0xfffd:
		return true
	case r >= 0x10000 && r <= 0x10ffff:
		return true
	}
	return false
}
