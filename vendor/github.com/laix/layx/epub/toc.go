package epub

import (
	"encoding/xml"
	"strings"
)

// tocNode is one entry of a navigation document before it is matched to the
// spine. Path and Frag are already resolved against the document that contains
// the link, so a fragment-only href names the navigation document itself.
type tocNode struct {
	Title    string
	Path     string
	Frag     string
	Children []tocNode
}

// parseNavTOC reads an EPUB 3 navigation document: the first <nav> whose type
// says "toc". It keeps document order, nesting and fragments; matching the
// targets to the spine happens in Book.attachTOC.
func parseNavTOC(data []byte, navPath string) []tocNode {
	tree, err := parseTree(data)
	if err != nil {
		return nil
	}
	nav := findTOCNav(tree)
	if nav == nil {
		return nil
	}
	return navEntries(nav, navPath)
}

func findTOCNav(n *hnode) *hnode {
	if n.tag == "nav" {
		typ := strings.ToLower(attr(n, "epub:type"))
		role := strings.ToLower(attr(n, "role"))
		if hasProperty(typ, "toc") || hasProperty(role, "doc-toc") {
			return n
		}
	}
	for _, child := range n.children {
		if child.tag == "" {
			continue
		}
		if found := findTOCNav(child); found != nil {
			return found
		}
	}
	return nil
}

// navEntries turns the first <ol>/<ul> inside a nav into entries. A nav without
// a list has no usable contents.
func navEntries(nav *hnode, base string) []tocNode {
	var list *hnode
	var find func(*hnode)
	find = func(n *hnode) {
		if list != nil {
			return
		}
		if n.tag == "ol" || n.tag == "ul" {
			list = n
			return
		}
		for _, child := range n.children {
			if child.tag != "" {
				find(child)
			}
		}
	}
	find(nav)
	if list == nil {
		return nil
	}
	return navList(list, base)
}

func navList(list *hnode, base string) []tocNode {
	var out []tocNode
	for _, li := range list.children {
		if li.tag != "li" {
			continue
		}
		title, href := navLabel(li)
		node := tocNode{Title: title}
		if href != "" {
			node.Path, node.Frag = splitHref(base, href)
		}
		for _, child := range li.children {
			if child.tag == "ol" || child.tag == "ul" {
				node.Children = append(node.Children, navList(child, base)...)
			}
		}
		if node.Title == "" && node.Path == "" {
			// A <li> that only groups children has no row of its own.
			out = append(out, node.Children...)
			continue
		}
		out = append(out, node)
	}
	return out
}

// navLabel returns the first link under li and, when there is no link, the text
// of a plain label such as <span>第一卷</span>. Nested lists are not labels.
func navLabel(li *hnode) (title, href string) {
	var label string
	var walk func(*hnode) bool
	walk = func(n *hnode) bool {
		for _, child := range n.children {
			if child.tag == "ol" || child.tag == "ul" {
				continue
			}
			if child.tag == "a" {
				title = collapseText(textContent(child))
				href = attr(child, "href")
				if title != "" || href != "" {
					return true
				}
			}
			if child.tag == "span" && label == "" {
				label = collapseText(textContent(child))
			}
			if child.tag != "" && walk(child) {
				return true
			}
		}
		return false
	}
	walk(li)
	if title == "" {
		title = label
	}
	return title, href
}

// parseNCX reads an EPUB 2 NCX navigation map with the same fidelity: order,
// nested navPoints and fragments survive.
func parseNCX(data []byte, ncxPath string) []tocNode {
	var ncx struct {
		NavMap struct {
			Points []ncxPoint `xml:"navPoint"`
		} `xml:"navMap"`
	}
	if err := xml.Unmarshal(data, &ncx); err != nil {
		return nil
	}
	return ncxEntries(ncx.NavMap.Points, ncxPath)
}

func ncxEntries(points []ncxPoint, base string) []tocNode {
	var out []tocNode
	for _, p := range points {
		node := tocNode{Title: collapseText(p.Label.Text)}
		if p.Content.Src != "" {
			node.Path, node.Frag = splitHref(base, p.Content.Src)
		}
		node.Children = ncxEntries(p.Points, base)
		if node.Title == "" && node.Path == "" {
			out = append(out, node.Children...)
			continue
		}
		out = append(out, node)
	}
	return out
}

type ncxPoint struct {
	Label struct {
		Text string `xml:"text"`
	} `xml:"navLabel"`
	Content struct {
		Src string `xml:"src,attr"`
	} `xml:"content"`
	Points []ncxPoint `xml:"navPoint"`
}
