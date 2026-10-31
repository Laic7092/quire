package legado

import (
	"fmt"
	"strings"

	"golang.org/x/net/html"
)

// simpleSel is one compound CSS selector in the supported subset.
type simpleSel struct {
	tag     string
	id      string
	classes []string
	attrs   []attrSel
}

type attrSel struct {
	name     string
	value    string
	hasValue bool
}

// selectorStep is one compound plus its relationship to the previous step.
type selectorStep struct {
	comb combinator
	sel  simpleSel
}

type combinator byte

const (
	combRoot combinator = iota
	combDescendant
	combChild
)

// queryAll selects nodes from root using the supported CSS subset.
// It handles groups separated by commas.
func queryAll(root *html.Node, selector string) ([]*html.Node, error) {
	groups, err := splitGroups(selector)
	if err != nil {
		return nil, err
	}
	seen := make(map[*html.Node]bool)
	var out []*html.Node
	for _, g := range groups {
		seq, err := parseSelectorGroup(g)
		if err != nil {
			return nil, err
		}
		nodes := querySeq([]*html.Node{root}, seq)
		for _, n := range nodes {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out, nil
}

// querySeq evaluates a sequence of combinators against a set of starting nodes.
func querySeq(start []*html.Node, seq []selectorStep) []*html.Node {
	if len(seq) == 0 {
		return nil
	}
	cur := make([]*html.Node, 0)
	for _, n := range start {
		cur = append(cur, matchAllDesc(n, seq[0].sel)...)
	}
	cur = uniqueOrdered(cur)
	for i := 1; i < len(seq); i++ {
		next := make([]*html.Node, 0)
		for _, n := range cur {
			var candidates []*html.Node
			if seq[i].comb == combChild {
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					if c.Type == html.ElementNode {
						candidates = append(candidates, c)
					}
				}
			} else {
				candidates = allDescendants(n)
			}
			for _, c := range candidates {
				if matchSimple(c, seq[i].sel) {
					next = append(next, c)
				}
			}
		}
		cur = uniqueOrdered(next)
	}
	return cur
}

// matchAllDesc returns n and all its descendants that match s.
func matchAllDesc(n *html.Node, s simpleSel) []*html.Node {
	var out []*html.Node
	if matchSimple(n, s) {
		out = append(out, n)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			out = append(out, matchAllDesc(c, s)...)
		}
	}
	return out
}

// allDescendants returns all descendants of n (not including n).
func allDescendants(n *html.Node) []*html.Node {
	var out []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			out = append(out, c)
			out = append(out, allDescendants(c)...)
		}
	}
	return out
}

// matchSimple tests one node against a compound selector.
func matchSimple(n *html.Node, s simpleSel) bool {
	if n.Type != html.ElementNode {
		return false
	}
	if s.tag != "" && !strings.EqualFold(n.Data, s.tag) {
		return false
	}
	if s.id != "" && attrValue(n, "id") != s.id {
		return false
	}
	classes := classSet(n)
	for _, c := range s.classes {
		if !classes[c] {
			return false
		}
	}
	for _, a := range s.attrs {
		v, ok := getAttr(n, a.name)
		if !ok {
			return false
		}
		if a.hasValue && v != a.value {
			return false
		}
	}
	return true
}

func classSet(n *html.Node) map[string]bool {
	set := make(map[string]bool)
	v, ok := getAttr(n, "class")
	if !ok {
		return set
	}
	for _, p := range strings.Fields(v) {
		set[p] = true
	}
	return set
}

func getAttr(n *html.Node, name string) (string, bool) {
	return attrValueOK(n, strings.ToLower(name))
}

func attrValueOK(n *html.Node, name string) (string, bool) {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, name) {
			return a.Val, true
		}
	}
	return "", false
}

func attrValue(n *html.Node, name string) string {
	v, _ := attrValueOK(n, strings.ToLower(name))
	return v
}

// textContent returns the concatenated text content of n and its descendants.
func textContent(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(textContent(c))
	}
	return b.String()
}

// innerHTML serializes the children of n.
func innerHTML(n *html.Node) string {
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&b, c); err != nil {
			return b.String()
		}
	}
	return b.String()
}

// splitGroups splits a selector at top-level commas.
func splitGroups(s string) ([]string, error) {
	var groups []string
	var b strings.Builder
	depth := 0
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch ch {
		case '[':
			depth++
			b.WriteByte(ch)
		case ']':
			if depth > 0 {
				depth--
			}
			b.WriteByte(ch)
		case ',':
			if depth == 0 {
				groups = append(groups, strings.TrimSpace(b.String()))
				b.Reset()
			} else {
				b.WriteByte(ch)
			}
		default:
			b.WriteByte(ch)
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("unbalanced brackets in selector %q", s)
	}
	if rest := strings.TrimSpace(b.String()); rest != "" {
		groups = append(groups, rest)
	}
	return groups, nil
}

// parseSelectorGroup parses one comma-free group into steps.
func parseSelectorGroup(s string) ([]selectorStep, error) {
	steps := []selectorStep{{comb: combRoot}}
	i := 0
	pending := combDescendant
	for i < len(s) {
		ch := s[i]
		switch ch {
		case ' ', '\t', '\n', '\r':
			pending = combDescendant
			i++
		case '>':
			pending = combChild
			i++
		case '.':
			i++
			start := i
			for i < len(s) && isIdent(s[i]) {
				i++
			}
			if start == i {
				return nil, fmt.Errorf("empty class in selector %q", s)
			}
			last := &steps[len(steps)-1]
			last.sel.classes = append(last.sel.classes, s[start:i])
			pending = 0
		case '#':
			i++
			start := i
			for i < len(s) && isIdent(s[i]) {
				i++
			}
			if start == i {
				return nil, fmt.Errorf("empty id in selector %q", s)
			}
			last := &steps[len(steps)-1]
			last.sel.id = s[start:i]
			pending = 0
		case '[':
			i++
			end := i
			for end < len(s) && s[end] != ']' {
				end++
			}
			if end >= len(s) {
				return nil, fmt.Errorf("unclosed attribute selector %q", s)
			}
			attr, err := parseAttrSel(s[i:end])
			if err != nil {
				return nil, err
			}
			last := &steps[len(steps)-1]
			last.sel.attrs = append(last.sel.attrs, attr)
			i = end + 1
			pending = 0
		default:
			if isIdentStart(ch) {
				start := i
				for i < len(s) && isIdent(s[i]) {
					i++
				}
				ident := s[start:i]
				last := &steps[len(steps)-1]
				if i < len(s) && s[i] == '.' {
					switch ident {
					case "tag":
						i++ // consume '.'
						start2 := i
						for i < len(s) && isIdent(s[i]) {
							i++
						}
						last.sel.tag = s[start2:i]
					case "class":
						i++
						start2 := i
						for i < len(s) && isIdent(s[i]) {
							i++
						}
						last.sel.classes = append(last.sel.classes, s[start2:i])
					case "id":
						i++
						start2 := i
						for i < len(s) && isIdent(s[i]) {
							i++
						}
						last.sel.id = s[start2:i]
					default:
						last.sel.tag = ident
					}
				} else {
					last.sel.tag = ident
				}
				pending = 0
			} else {
				return nil, fmt.Errorf("unexpected character %q in selector %q", ch, s)
			}
		}
		if pending != 0 && i < len(s) {
			steps = append(steps, selectorStep{comb: pending})
			pending = 0
		}
	}
	return steps, nil
}

func parseAttrSel(s string) (attrSel, error) {
	s = strings.TrimSpace(s)
	for i := 0; i < len(s); i++ {
		if s[i] == '=' {
			name := strings.TrimSpace(s[:i])
			val := strings.TrimSpace(s[i+1:])
			val = strings.Trim(val, "\"'")
			return attrSel{name: name, value: val, hasValue: true}, nil
		}
	}
	if s == "" {
		return attrSel{}, fmt.Errorf("empty attribute selector")
	}
	return attrSel{name: s}, nil
}

func isIdentStart(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_' || (ch >= '0' && ch <= '9') || ch == '*'
}

func isIdent(ch byte) bool {
	return isIdentStart(ch) || ch == '-' || ch == ':'
}

func uniqueOrdered(nodes []*html.Node) []*html.Node {
	seen := make(map[*html.Node]bool, len(nodes))
	out := make([]*html.Node, 0, len(nodes))
	for _, n := range nodes {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}
