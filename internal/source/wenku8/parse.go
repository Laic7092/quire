package wenku8

import (
	"fmt"
	stdhtml "html"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"github.com/laix/quire/internal/source"
)

var (
	bookDetailPathRe  = regexp.MustCompile(`^/book/(\d+)(?:\.html?)?$`)
	bookIndexPathRe   = regexp.MustCompile(`^/book/(\d+)/index\.html?$`)
	chapterPathRe     = regexp.MustCompile(`^/book/(\d+)/([^/]+)\.html?$`)
	volumeTitleRe     = regexp.MustCompile(`^第[0-9零一二三四五六七八九十百千两]+[卷部]`)
	dateRe            = regexp.MustCompile(`\d{4}[-/年]\d{1,2}[-/月]\d{1,2}`)
	spaceRunRe        = regexp.MustCompile(`[ \t\r\n]+`)
	assetSchemePrefix = []string{"http://", "https://"}
)

// bookAnchor is one distinct book link seen on a search page, with every
// occurrence's text so the title wins over a cover link's "封面" or an empty
// image anchor.
type bookAnchor struct {
	id    string
	url   string
	title string
	node  *html.Node
}

func parseSearch(root *html.Node, pageURL string, page int) ([]source.Book, bool, error) {
	var order []*bookAnchor
	byID := make(map[string]*bookAnchor)
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, "a") {
			id, detail := detailFromHref(pageURL, attr(n, "href"))
			if id != "" {
				title := source.CleanText(nodeText(n))
				if a, ok := byID[id]; ok {
					if len(title) > len(a.title) {
						a.title = title
					}
				} else {
					a := &bookAnchor{id: id, url: detail, title: title, node: n}
					byID[id] = a
					order = append(order, a)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)

	if len(order) == 0 {
		if isNoResultsPage(root) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("%w: search page has no result rows", source.ErrBadResponse)
	}

	books := make([]source.Book, 0, len(order))
	for _, a := range order {
		title := a.title
		if title == "" {
			title = source.CleanText(attr(a.node, "title"))
		}
		if title == "" {
			// A row whose only link is the cover still needs a title; use the
			// row's text after removing the author and date columns.
			title = source.CleanText(nodeText(nearestContainer(a.node, 6)))
		}
		if title == "" {
			continue
		}
		container := nearestContainer(a.node, 6)
		cols := searchColumns(container)
		author, status, updated, description := searchRowFields(container, cols, title)
		cover := ""
		if img := find(container, func(n *html.Node) bool { return n.Data == "img" }); img != nil {
			src := attr(img, "data-src")
			if src == "" {
				src = attr(img, "src")
			}
			cover = absoluteAsset(pageURL, src)
		}
		extra := map[string]string{"id": a.id, "url": a.url}
		if status != "" {
			extra["status"] = status
		}
		if updated != "" {
			extra["updated"] = updated
		}
		books = append(books, source.Book{
			Source:      ID,
			Key:         a.url,
			Title:       title,
			Author:      author,
			Description: description,
			CoverURL:    cover,
			Language:    "zh",
			Format:      "epub",
			Year:        updated,
			Extra:       extra,
		})
	}
	return books, pageHasNext(root, pageURL, page), nil
}

func isNoResultsPage(root *html.Node) bool {
	text := strings.ToLower(nodeText(root))
	for _, marker := range []string{"没有找到", "没有搜索到", "无搜索结果", "找不到", "抱歉", "no results", "nothing found"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	if strings.Contains(text, "搜索结果") {
		return true
	}
	// A result table with its header and no rows is a legitimate empty
	// search. A site-wide search box alone is not: maintenance and error
	// pages carry one too, so it must not turn a bad page into "no results".
	return find(root, func(n *html.Node) bool {
		if n.Type != html.ElementNode || n.Data != "table" {
			return false
		}
		t := strings.ToLower(nodeText(n))
		return strings.Contains(t, "标题") && strings.Contains(t, "作者")
	}) != nil
}

func pageHasNext(root *html.Node, pageURL string, page int) bool {
	next := false
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if next {
			return
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			text := source.CleanText(nodeText(n))
			if strings.Contains(text, "下一页") || text == "下页" {
				next = true
				return
			}
			href := attr(n, "href")
			if href != "" {
				if u, err := url.Parse(source.Combine(pageURL, href)); err == nil {
					if v := u.Query().Get("page"); v != "" {
						if got, err := strconv.Atoi(v); err == nil && got == page+1 {
							next = true
							return
						}
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return next
}

// searchColumn describes which field a search-result table column holds.
type searchColumn int

const (
	columnUnknown searchColumn = iota
	columnTitle
	columnAuthor
	columnStatus
	columnUpdated
	columnDescription
)

type searchColumnMap map[searchColumn]int

// searchColumns reads the table header that precedes a result row. The site
// changes column order occasionally, so the header, not a hard-coded index,
// is the authority.
func searchColumns(container *html.Node) searchColumnMap {
	cols := searchColumnMap{}
	table := ancestor(container, "table", 8)
	if table == nil {
		return cols
	}
	var header *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if header != nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "tr" {
			cells := childCells(n)
			kinds := make([]searchColumn, len(cells))
			hasAuthor, hasTitle := false, false
			for i, cell := range cells {
				kinds[i] = columnKind(nodeText(cell))
				switch kinds[i] {
				case columnAuthor:
					hasAuthor = true
				case columnTitle:
					hasTitle = true
				}
			}
			if hasAuthor && hasTitle {
				header = n
				for i, kind := range kinds {
					if kind != columnUnknown {
						if _, exists := cols[kind]; !exists {
							cols[kind] = i
						}
					}
				}
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(table)
	return cols
}

func columnKind(text string) searchColumn {
	text = strings.ToLower(source.CleanText(text))
	switch {
	case strings.Contains(text, "简介") || strings.Contains(text, "介绍") || text == "内容":
		return columnDescription
	case strings.Contains(text, "作者"):
		return columnAuthor
	case strings.Contains(text, "状态"):
		return columnStatus
	case strings.Contains(text, "更新") || strings.Contains(text, "日期") || strings.Contains(text, "时间"):
		return columnUpdated
	case strings.Contains(text, "标题") || strings.Contains(text, "书名") || strings.Contains(text, "文章") || text == "书":
		return columnTitle
	}
	return columnUnknown
}

func searchRowFields(container *html.Node, cols searchColumnMap, title string) (author, status, updated, description string) {
	cells := childCells(container)
	cell := func(kind searchColumn) string {
		if i, ok := cols[kind]; ok && i >= 0 && i < len(cells) {
			return source.CleanText(nodeText(cells[i]))
		}
		return ""
	}
	author = cell(columnAuthor)
	status = cell(columnStatus)
	updated = cell(columnUpdated)
	description = cell(columnDescription)

	for _, kind := range []struct {
		field *string
		label string
	}{
		{&author, "作者"},
		{&status, "状态"},
		{&updated, "更新"},
		{&description, "简介"},
	} {
		if *kind.field == "" {
			*kind.field = labeledValue(container, kind.label)
		}
	}
	if author == "" {
		if n := findByClass(container, "author", "writer"); n != nil {
			author = source.CleanText(nodeText(n))
		}
	}
	if description == "" {
		if n := findByClass(container, "intro", "description", "desc", "summary"); n != nil {
			description = source.CleanText(nodeText(n))
		}
	}
	if updated == "" {
		updated = dateRe.FindString(nodeText(container))
	}
	// Without a usable header, the remaining cells are all we have: a short
	// cell that is not the title, a date or a status is the author.
	if author == "" {
		for _, c := range cells {
			v := source.CleanText(nodeText(c))
			if v == "" || v == title || dateRe.MatchString(v) || isStatusText(v) {
				continue
			}
			if len([]rune(v)) <= 40 {
				author = v
				break
			}
		}
	}
	return author, status, updated, description
}

func isStatusText(s string) bool {
	s = strings.ToLower(source.CleanText(s))
	return strings.Contains(s, "连载") || strings.Contains(s, "完结") || strings.Contains(s, "已完") || strings.Contains(s, "状态")
}

// parseBook reads the detail page: title, author, synopsis, cover and the
// link to the table of contents.
type bookInfo struct {
	Title       string
	Author      string
	Description string
	CoverURL    string
	CatalogURL  string
}

func parseBook(root *html.Node, pageURL string) (bookInfo, error) {
	var info bookInfo

	titleNode := find(root, func(n *html.Node) bool {
		if n.Type != html.ElementNode || n.Data != "h1" {
			return false
		}
		return insideID(n, "info") || insideID(n, "bookinfo") || insideClass(n, "book_info", "bookinfo", "book-info")
	})
	if titleNode == nil {
		titleNode = find(root, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "h1"
		})
	}
	if titleNode != nil {
		info.Title = cleanTitle(nodeText(titleNode))
	}
	if info.Title == "" {
		if n := find(root, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "title"
		}); n != nil {
			info.Title = cleanTitle(nodeText(n))
		}
	}
	if info.Title == "" {
		return bookInfo{}, fmt.Errorf("%w: book page has no title", source.ErrBadResponse)
	}

	info.Author = labeledValue(root, "作者")
	if info.Author == "" {
		if n := find(root, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "meta" &&
				strings.EqualFold(attr(n, "name"), "author")
		}); n != nil {
			info.Author = source.CleanText(attr(n, "content"))
		}
	}

	if n := findID(root, "content"); n != nil {
		info.Description = source.CleanText(nodeText(n))
	}
	if info.Description == "" {
		if n := findByClass(root, "intro", "description", "book_intro", "book-intro", "summary"); n != nil {
			info.Description = source.CleanText(nodeText(n))
		}
	}
	if info.Description == "" {
		if n := find(root, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "meta" &&
				strings.EqualFold(attr(n, "name"), "description")
		}); n != nil {
			info.Description = source.CleanText(attr(n, "content"))
		}
	}

	if n := find(root, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "meta" &&
			strings.EqualFold(attr(n, "property"), "og:image")
	}); n != nil {
		info.CoverURL = absoluteAsset(pageURL, attr(n, "content"))
	}
	if info.CoverURL == "" {
		if n := find(root, func(n *html.Node) bool {
			if n.Type != html.ElementNode || n.Data != "img" {
				return false
			}
			return insideID(n, "cover") || insideID(n, "info") || insideClass(n, "cover", "book_cover", "book-cover")
		}); n != nil {
			src := attr(n, "data-src")
			if src == "" {
				src = attr(n, "src")
			}
			info.CoverURL = absoluteAsset(pageURL, src)
		}
	}

	var catalogNode *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if catalogNode != nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			href := attr(n, "href")
			text := source.CleanText(nodeText(n))
			if href != "" && (strings.Contains(text, "目录") || strings.Contains(text, "开始阅读") ||
				strings.Contains(text, "章节目录") || strings.Contains(text, "点击阅读")) {
				catalogNode = n
				return
			}
			if href != "" {
				if u, err := url.Parse(source.Combine(pageURL, href)); err == nil {
					if bookIndexPathRe.MatchString(path.Clean(u.Path)) {
						catalogNode = n
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	if catalogNode != nil {
		info.CatalogURL = source.Combine(pageURL, attr(catalogNode, "href"))
	}

	return info, nil
}

// chapterRef is one row of the table of contents. The title already includes
// its volume name, when the page groups chapters into volumes.
type chapterRef struct {
	title string
	url   string
}

func parseCatalog(root *html.Node, pageURL, bookID string) []chapterRef {
	var refs []chapterRef
	seen := make(map[string]bool)
	volume := ""

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if isVolumeHeader(n) {
				if title := source.CleanText(nodeText(n)); title != "" {
					volume = title
				}
			}
			if strings.EqualFold(n.Data, "a") {
				if abs, ok := chapterHref(pageURL, attr(n, "href"), bookID); ok && !seen[abs] {
					title := source.CleanText(nodeText(n))
					if title == "" {
						title = source.CleanText(attr(n, "title"))
					}
					if title != "" {
						seen[abs] = true
						refs = append(refs, chapterRef{title: joinVolume(volume, title), url: abs})
					}
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return refs
}

func isVolumeHeader(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	switch strings.ToLower(n.Data) {
	case "td", "div", "dt", "h1", "h2", "h3", "h4", "span", "li":
	default:
		return false
	}
	if hasClass(n, "vcss", "volume", "vol", "part", "juan") ||
		strings.Contains(strings.ToLower(attr(n, "id")), "volume") {
		return true
	}
	// A bare "第一卷" cell also marks a volume, but only if it is a heading
	// rather than a chapter link or a row that contains one.
	if find(n, func(c *html.Node) bool { return c.Type == html.ElementNode && c.Data == "a" }) != nil {
		return false
	}
	return volumeTitleRe.MatchString(source.CleanText(nodeText(n)))
}

func joinVolume(volume, title string) string {
	if volume == "" || strings.HasPrefix(title, volume) {
		return title
	}
	return volume + " " + title
}

func chapterHref(pageURL, href, bookID string) (string, bool) {
	href = strings.TrimSpace(href)
	if href == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(strings.ToLower(href), "javascript:") {
		return "", false
	}
	abs := source.Combine(pageURL, href)
	u, err := url.Parse(abs)
	if err != nil {
		return "", false
	}
	m := chapterPathRe.FindStringSubmatch(path.Clean(u.Path))
	if m == nil {
		return "", false
	}
	if bookID != "" && m[1] != bookID {
		return "", false
	}
	if strings.EqualFold(m[2], "index") {
		return "", false
	}
	return abs, true
}

// parseChapter returns a normalized HTML fragment for one chapter page.
func parseChapter(root *html.Node, pageURL string) (string, error) {
	container := findID(root, "content")
	if container == nil {
		container = findByClass(root, "content", "chapter-content", "chapter_content", "booktext", "book-text")
	}
	if container == nil {
		container = findID(root, "BookText")
	}
	if container == nil {
		return "", fmt.Errorf("%w: chapter page has no content container", source.ErrBadResponse)
	}
	if source.CleanText(nodeText(container)) == "" && !hasImage(container) {
		return "", fmt.Errorf("%w: chapter page has empty content", source.ErrBadResponse)
	}
	return renderContent(container), nil
}

// renderContent turns the site's loose HTML into paragraphs. Every <br> ends
// a paragraph, which matches how the site separates Chinese prose lines, and
// block elements do the same. Scripts, navigation and ads never reach the
// output, and an image survives as its alt text.
func renderContent(root *html.Node) string {
	var (
		paras []string
		cur   strings.Builder
	)
	flush := func() {
		s := strings.TrimSpace(cur.String())
		if s != "" {
			paras = append(paras, "<p>"+s+"</p>")
		}
		cur.Reset()
	}

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			if strings.TrimSpace(n.Data) == "" {
				if cur.Len() > 0 {
					cur.WriteString(" ")
				}
				return
			}
			s := escapeInline(n.Data)
			if cur.Len() == 0 {
				s = strings.TrimLeft(s, " ")
			}
			cur.WriteString(s)
			return
		case html.ElementNode:
			if skipNode(n) {
				return
			}
			switch strings.ToLower(n.Data) {
			case "br":
				flush()
				return
			case "img":
				src := attr(n, "data-src")
				if src == "" {
					src = attr(n, "src")
				}
				alt := source.CleanText(attr(n, "alt"))
				if src == "" && alt == "" {
					return
				}
				cur.WriteString(`<img src="` + stdhtml.EscapeString(src) + `" alt="` + stdhtml.EscapeString(alt) + `"/>`)
				return
			case "p", "div", "h1", "h2", "h3", "h4", "h5", "h6", "li", "blockquote",
				"section", "article", "tr", "dd", "dt", "pre":
				flush()
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c)
				}
				flush()
				return
			case "hr":
				flush()
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	flush()
	return strings.Join(paras, "\n")
}

func escapeInline(s string) string {
	s = strings.ReplaceAll(s, "\u00a0", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	s = spaceRunRe.ReplaceAllString(s, " ")
	return stdhtml.EscapeString(s)
}

// skipNode reports markup that is navigation, an ad or active content rather
// than prose.
func skipNode(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	switch strings.ToLower(n.Data) {
	case "script", "style", "iframe", "noscript", "template", "nav", "form", "button",
		"input", "select", "textarea", "object", "embed", "svg", "canvas", "audio", "video":
		return true
	}
	return hasClass(n, "ad", "ads", "adv", "advert", "advertisement", "adsbygoogle", "gg")
}

// detailFromHref recognizes every book link the site uses and returns the
// book id plus a canonical detail URL.
func detailFromHref(pageURL, href string) (string, string) {
	href = strings.TrimSpace(href)
	if href == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(strings.ToLower(href), "javascript:") {
		return "", ""
	}
	abs := source.Combine(pageURL, href)
	u, err := url.Parse(abs)
	if err != nil {
		return "", ""
	}
	p := path.Clean(u.Path)
	if m := bookDetailPathRe.FindStringSubmatch(p); m != nil {
		return m[1], canonicalDetail(u, m[1])
	}
	if m := bookIndexPathRe.FindStringSubmatch(p); m != nil {
		return m[1], canonicalDetail(u, m[1])
	}
	if strings.Contains(p, "articleinfo") {
		if id := u.Query().Get("id"); isDigits(id) {
			return id, canonicalDetail(u, id)
		}
	}
	return "", ""
}

func canonicalDetail(u *url.URL, id string) string {
	base := u.Scheme + "://" + u.Host
	if u.Scheme == "" || u.Host == "" {
		base = ""
	}
	return base + "/book/" + id + ".htm"
}

func defaultCatalogURL(detailURL, id string) string {
	if id == "" {
		return ""
	}
	if u, err := url.Parse(detailURL); err == nil && u.Scheme != "" && u.Host != "" {
		return u.Scheme + "://" + u.Host + "/book/" + id + "/index.htm"
	}
	return "/book/" + id + "/index.htm"
}

func absoluteAsset(pageURL, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	abs := source.Combine(pageURL, ref)
	for _, prefix := range assetSchemePrefix {
		if strings.HasPrefix(strings.ToLower(abs), prefix) {
			return abs
		}
	}
	return ""
}

func cleanTitle(s string) string {
	s = source.CleanText(s)
	for _, sep := range []string{"_", "|", " - "} {
		if i := strings.Index(s, sep); i > 0 {
			rest := strings.ToLower(s[i:])
			if strings.Contains(rest, "文库") || strings.Contains(rest, "wenku8") {
				s = strings.TrimSpace(s[:i])
				break
			}
		}
	}
	return s
}

// labeledValue finds a "作者：..." style value in either one text node or a
// label cell followed by a value cell.
func labeledValue(root *html.Node, labels ...string) string {
	var walk func(*html.Node) string
	walk = func(n *html.Node) string {
		if n.Type == html.ElementNode && skipNode(n) {
			return ""
		}
		if n.Type == html.TextNode {
			t := source.CleanText(n.Data)
			for _, label := range labels {
				for _, sep := range []string{"：", ":"} {
					key := label + sep
					if i := strings.Index(t, key); i >= 0 {
						if v := source.CleanText(t[i+len(key):]); v != "" {
							return v
						}
						if n.NextSibling != nil {
							if v := source.CleanText(nodeText(n.NextSibling)); v != "" {
								return v
							}
						}
						if n.Parent != nil && n.Parent.NextSibling != nil {
							if v := source.CleanText(nodeText(n.Parent.NextSibling)); v != "" {
								return v
							}
						}
					}
				}
				if strings.TrimSpace(strings.TrimRight(t, "：:")) == label {
					if n.NextSibling != nil {
						if v := source.CleanText(nodeText(n.NextSibling)); v != "" {
							return v
						}
					}
					if n.Parent != nil && n.Parent.NextSibling != nil {
						if v := source.CleanText(nodeText(n.Parent.NextSibling)); v != "" {
							return v
						}
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if v := walk(c); v != "" {
				return v
			}
		}
		return ""
	}
	return walk(root)
}

func attr(n *html.Node, key string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func hasClass(n *html.Node, names ...string) bool {
	class := strings.ToLower(attr(n, "class"))
	if class == "" {
		return false
	}
	tokens := strings.FieldsFunc(class, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '-' || r == '_'
	})
	for _, name := range names {
		for _, token := range tokens {
			if token == name {
				return true
			}
		}
	}
	return false
}

func findID(n *html.Node, id string) *html.Node {
	return find(n, func(c *html.Node) bool {
		return c.Type == html.ElementNode && strings.EqualFold(attr(c, "id"), id)
	})
}

func findByClass(n *html.Node, names ...string) *html.Node {
	return find(n, func(c *html.Node) bool {
		return c.Type == html.ElementNode && hasClass(c, names...)
	})
}

func insideID(n *html.Node, id string) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && strings.EqualFold(attr(p, "id"), id) {
			return true
		}
	}
	return false
}

func insideClass(n *html.Node, names ...string) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type == html.ElementNode && hasClass(p, names...) {
			return true
		}
	}
	return false
}

func find(n *html.Node, pred func(*html.Node) bool) *html.Node {
	if n == nil {
		return nil
	}
	if pred(n) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if got := find(c, pred); got != nil {
			return got
		}
	}
	return nil
}

func hasImage(n *html.Node) bool {
	return find(n, func(c *html.Node) bool {
		return c.Type == html.ElementNode && c.Data == "img" && !skipNode(c)
	}) != nil
}

// nodeText returns visible text, skipping scripts, styles, navigation and ads.
func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(c *html.Node) {
		if c.Type == html.ElementNode && skipNode(c) {
			return
		}
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
			b.WriteString(" ")
		}
		for child := c.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(n)
	return b.String()
}

func childCells(n *html.Node) []*html.Node {
	if n == nil {
		return nil
	}
	var cells []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && (c.Data == "td" || c.Data == "th") {
			cells = append(cells, c)
		}
	}
	return cells
}

// nearestContainer climbs from a link to the row or card that holds its
// metadata, so a title link and an author cell share one container.
func nearestContainer(n *html.Node, maxDepth int) *html.Node {
	depth := 0
	for p := n.Parent; p != nil && depth < maxDepth; p = p.Parent {
		if p.Type == html.ElementNode {
			switch p.Data {
			case "tr", "li":
				return p
			}
			if hasClass(p, "item", "book", "result", "search_item", "search-item", "grid_row", "grid-row") {
				return p
			}
		}
		depth++
	}
	if n.Parent != nil {
		return n.Parent
	}
	return n
}

func ancestor(n *html.Node, tag string, maxDepth int) *html.Node {
	depth := 0
	for p := n.Parent; p != nil && depth < maxDepth; p = p.Parent {
		if p.Type == html.ElementNode && strings.EqualFold(p.Data, tag) {
			return p
		}
		depth++
	}
	return nil
}
