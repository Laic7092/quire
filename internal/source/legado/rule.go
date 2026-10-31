package legado

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/laix/quire/internal/source"
	"golang.org/x/net/html"
)

// evalField evaluates a Legado rule against ctx and returns the first value.
// ctx may be *html.Node or any JSON value.
func evalField(ctx interface{}, baseURL string, rule string) (string, error) {
	values, err := evalFieldAll(ctx, baseURL, rule)
	if err != nil {
		return "", err
	}
	if len(values) == 0 {
		return "", nil
	}
	return values[0], nil
}

// evalFieldAll evaluates a rule and returns all matched values.
func evalFieldAll(ctx interface{}, baseURL string, rule string) ([]string, error) {
	rule, opts, err := parseOptions(rule)
	if err != nil {
		return nil, err
	}
	_ = opts
	if isUnsupported(rule) {
		return nil, fmt.Errorf("%w: unsupported rule %q", source.ErrUnsupported, rule)
	}

	// Regex-only forms.
	if strings.HasPrefix(rule, "regex:") {
		pat := strings.TrimPrefix(rule, "regex:")
		re, err := regexp.Compile(pat)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid regex %q: %v", source.ErrUnsupported, pat, err)
		}
		txt := ctxText(ctx)
		return matchStrings(re.FindAllString(txt, -1)), nil
	}

	// Split trailing ##pattern##replacement.
	selector, re, replace, hasRegex := splitRegex(rule)

	var values []string
	switch {
	case selector == "":
		values = []string{ctxText(ctx)}
	case isJSONRule(selector):
		values, err = evalJSONRule(ctx, selector)
	case looksLikeCSS(selector):
		values, err = evalCSSRule(ctx, baseURL, selector)
	default:
		// Plain text input, e.g. "Hello 123##\\d+##X".
		values = []string{selector}
	}
	if err != nil {
		return nil, err
	}

	if hasRegex {
		if re == nil {
			return nil, fmt.Errorf("%w: invalid regex in %q", source.ErrUnsupported, rule)
		}
		for i := range values {
			values[i] = re.ReplaceAllString(values[i], replace)
		}
	}
	return values, nil
}

// selectList evaluates a rule that is expected to return a list of items.
// Each item is a context (*html.Node or JSON value) that fields can be read from.
func selectList(ctx interface{}, baseURL string, rule string) ([]interface{}, error) {
	rule, _, err := parseOptions(rule)
	if err != nil {
		return nil, err
	}
	if isUnsupported(rule) {
		return nil, fmt.Errorf("%w: unsupported rule %q", source.ErrUnsupported, rule)
	}
	selector, _, _, _ := splitRegex(rule)

	if isJSONRule(selector) {
		values, err := evalJSONRule(ctx, selector)
		if err != nil {
			return nil, err
		}
		out := make([]interface{}, 0, len(values))
		for _, v := range values {
			out = append(out, v)
		}
		return out, nil
	}

	nodes, err := evalCSSNodes(ctx, baseURL, selector)
	if err != nil {
		return nil, err
	}
	out := make([]interface{}, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n)
	}
	return out, nil
}

// isUnsupported reports rules this engine deliberately does not implement.
func isUnsupported(rule string) bool {
	return strings.Contains(rule, "@js:") || strings.Contains(rule, "<js>")
}

// parseOptions strips the trailing `,{...}` request options from a rule.
func parseOptions(rule string) (string, requestOpts, error) {
	rule = strings.TrimSpace(rule)
	if !strings.HasSuffix(rule, "}") {
		return rule, requestOpts{}, nil
	}
	idx := strings.LastIndex(rule, ",{")
	if idx < 0 {
		return rule, requestOpts{}, nil
	}
	prefix := rule[:idx]
	suffix := rule[idx+1:]
	var opts requestOpts
	if err := json.Unmarshal([]byte(suffix), &opts); err != nil {
		return rule, requestOpts{}, nil
	}
	return strings.TrimSpace(prefix), opts, nil
}

// splitRegex separates a selector from an optional ##pattern##replacement suffix.
func splitRegex(rule string) (selector string, re *regexp.Regexp, replace string, ok bool) {
	parts := strings.SplitN(rule, "##", 3)
	if len(parts) != 3 {
		return rule, nil, "", false
	}
	re, err := regexp.Compile(parts[1])
	if err != nil {
		return rule, nil, "", false
	}
	return parts[0], re, parts[2], true
}

func isJSONRule(rule string) bool {
	s := strings.TrimSpace(rule)
	return strings.HasPrefix(s, "@json:") || strings.HasPrefix(s, "$")
}

func looksLikeCSS(rule string) bool {
	s := strings.TrimSpace(rule)
	if strings.HasPrefix(s, "@css:") {
		return true
	}
	if strings.ContainsAny(s, ".#[]@> ") || strings.HasPrefix(s, "tag.") || strings.HasPrefix(s, "class.") || strings.HasPrefix(s, "id.") {
		return true
	}
	return isSingleIdent(s)
}

func isSingleIdent(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isIdent(s[i]) {
			return false
		}
	}
	return true
}

func evalJSONRule(ctx interface{}, rule string) ([]string, error) {
	s := strings.TrimSpace(rule)
	s = strings.TrimPrefix(s, "@json:")
	s = strings.TrimSpace(s)

	var root interface{}
	switch v := ctx.(type) {
	case nil:
		return nil, nil
	case *html.Node:
		// Parse the HTML text content as JSON.
		txt := textContent(v)
		if err := json.Unmarshal([]byte(txt), &root); err != nil {
			return nil, fmt.Errorf("%w: invalid JSON %q", source.ErrBadResponse, rule)
		}
	default:
		root = v
	}

	values, err := jsonPath(root, s)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid JSONPath %q: %v", source.ErrUnsupported, rule, err)
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, jsonValueString(v))
	}
	return out, nil
}

func evalCSSRule(ctx interface{}, baseURL string, rule string) ([]string, error) {
	nodes, extraction, err := evalCSSNodesAndExtraction(ctx, baseURL, rule)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		s := extractFromNode(n, baseURL, extraction)
		out = append(out, s)
	}
	return out, nil
}

func evalCSSNodes(ctx interface{}, baseURL string, rule string) ([]*html.Node, error) {
	nodes, _, err := evalCSSNodesAndExtraction(ctx, baseURL, rule)
	return nodes, err
}

func evalCSSNodesAndExtraction(ctx interface{}, baseURL string, rule string) ([]*html.Node, string, error) {
	node, ok := ctx.(*html.Node)
	if !ok {
		// CSS rules do not apply to JSON values.
		return nil, "", nil
	}
	segments := splitChain(rule)
	if len(segments) == 0 {
		return []*html.Node{node}, "text", nil
	}
	extraction := "text"
	last := segments[len(segments)-1]
	if isExtraction(last) {
		extraction = last
		segments = segments[:len(segments)-1]
	}
	if len(segments) == 0 {
		return []*html.Node{node}, extraction, nil
	}

	nodes := []*html.Node{node}
	for _, seg := range segments {
		// Strip an optional @css: prefix from intermediate selectors.
		seg = strings.TrimPrefix(seg, "@css:")
		seg = strings.TrimSpace(seg)
		var err error
		nodes, err = queryAllRelative(nodes, seg)
		if err != nil {
			return nil, "", err
		}
	}
	return nodes, extraction, nil
}

// splitChain splits a rule by '@' while leaving prefixes like @css: and @json: intact.
func splitChain(rule string) []string {
	var parts []string
	var b strings.Builder
	i := 0
	for i < len(rule) {
		if rule[i] == '@' {
			switch {
			case strings.HasPrefix(rule[i:], "@css:"):
				b.WriteString("@css:")
				i += 5
			case strings.HasPrefix(rule[i:], "@json:"):
				b.WriteString("@json:")
				i += 6
			default:
				if b.Len() > 0 {
					parts = append(parts, b.String())
					b.Reset()
				}
				i++
			}
		} else {
			b.WriteByte(rule[i])
			i++
		}
	}
	if b.Len() > 0 {
		parts = append(parts, b.String())
	}
	return parts
}

func isExtraction(s string) bool {
	s = strings.TrimSpace(s)
	return s == "text" || s == "html" || s == "href" || s == "src" || strings.HasPrefix(s, "attr(")
}

func extractFromNode(n *html.Node, baseURL string, extraction string) string {
	switch extraction {
	case "html":
		return innerHTML(n)
	case "href":
		return source.Combine(baseURL, attrValue(n, "href"))
	case "src":
		return source.Combine(baseURL, attrValue(n, "src"))
	}
	if strings.HasPrefix(extraction, "attr(") {
		name := strings.TrimPrefix(extraction, "attr(")
		name = strings.TrimSuffix(name, ")")
		name = strings.TrimSpace(name)
		v := attrValue(n, name)
		if strings.EqualFold(name, "href") || strings.EqualFold(name, "src") {
			return source.Combine(baseURL, v)
		}
		return v
	}
	return source.CleanText(textContent(n))
}

func queryAllRelative(nodes []*html.Node, selector string) ([]*html.Node, error) {
	seen := make(map[*html.Node]bool)
	var out []*html.Node
	for _, n := range nodes {
		matches, err := queryAll(n, selector)
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	return out, nil
}

func ctxText(ctx interface{}) string {
	switch v := ctx.(type) {
	case nil:
		return ""
	case *html.Node:
		return textContent(v)
	case string:
		return v
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

func matchStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func jsonValueString(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

// jsonPath evaluates a tiny JSONPath subset: .field, [n] and [*].
func jsonPath(root interface{}, path string) ([]interface{}, error) {
	path = strings.TrimSpace(path)
	if path == "" || path == "$" {
		return []interface{}{root}, nil
	}
	cur := []interface{}{root}
	i := 0
	if path[0] == '$' {
		i = 1
	}
	for i < len(path) {
		ch := path[i]
		switch ch {
		case '.':
			i++
			start := i
			for i < len(path) && path[i] != '.' && path[i] != '[' {
				i++
			}
			field := path[start:i]
			if field == "" {
				return nil, fmt.Errorf("empty field at %d", i)
			}
			next := make([]interface{}, 0, len(cur))
			for _, v := range cur {
				if m, ok := v.(map[string]interface{}); ok {
					if val, ok := m[field]; ok {
						next = append(next, val)
					}
				}
			}
			cur = next
		case '[':
			i++
			end := strings.IndexByte(path[i:], ']')
			if end < 0 {
				return nil, fmt.Errorf("unclosed bracket")
			}
			inner := path[i : i+end]
			i += end + 1
			next := make([]interface{}, 0, len(cur))
			if inner == "*" {
				for _, v := range cur {
					if arr, ok := v.([]interface{}); ok {
						next = append(next, arr...)
					}
				}
			} else {
				idx, err := strconv.Atoi(inner)
				if err != nil {
					return nil, fmt.Errorf("invalid index %q", inner)
				}
				for _, v := range cur {
					if arr, ok := v.([]interface{}); ok && idx >= 0 && idx < len(arr) {
						next = append(next, arr[idx])
					}
				}
			}
			cur = next
		default:
			start := i
			for i < len(path) && path[i] != '.' && path[i] != '[' {
				i++
			}
			field := path[start:i]
			next := make([]interface{}, 0, len(cur))
			for _, v := range cur {
				if m, ok := v.(map[string]interface{}); ok {
					if val, ok := m[field]; ok {
						next = append(next, val)
					}
				}
			}
			cur = next
		}
	}
	return cur, nil
}
