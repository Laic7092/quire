package zlib

import (
	"bytes"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/laix/quire/internal/source"
	"golang.org/x/net/html"
)

// Every site-specific HTML hook is here. Z-Library changes its markup and its
// mirrors differ slightly, so the parser accepts several shapes instead of
// depending on one. A redesign should only need edits in this block.
var (
	pathLogin  = "/login"
	pathSearch = "/s/"
	pathBook   = "/book/"

	cardTags       = []string{"z-bookcard"}
	cardClasses    = []string{"book-item", "bookItem", "resItemBox", "book-item-wrapper"}
	titleClasses   = []string{"book-title", "bookTitle", "book-title-link", "book-item-title"}
	authorClasses  = []string{"book-author", "book-authors", "bookAuthor", "authors", "author"}
	coverClasses   = []string{"book-cover", "bookCover", "cover", "cover-image"}
	detailClasses  = []string{"book-details", "book-details-button", "property__file", "book-property", "file-details", "book-meta"}
	descClasses    = []string{"book-description", "book-description__text", "bookDescription", "description", "book-summary"}
	yearClasses    = []string{"book-year", "year"}
	nextClasses    = []string{"next", "pagination-next", "pagination__next", "page-next"}
	noResultClass  = []string{"no-result", "no-results", "noResult", "search-empty", "searchEmpty", "not-found", "empty"}
	catalogClasses = []string{"book-list", "bookList", "book-items", "search-result", "search-results", "result-list", "resItemBox"}

	noResultText = []string{
		"no results", "nothing found", "no books found", "0 results",
		"没有找到", "无结果", "未找到",
	}

	bookIDRe    = regexp.MustCompile(`(?i)/book/([^/?#]+)`)
	anyFormatRe = regexp.MustCompile(`(?i)(^|[^a-z0-9])(epub|txt|pdf|mobi|azw3|fb2|djvu)([^a-z0-9]|$)`)
	epubRe      = regexp.MustCompile(`(?i)(^|[^a-z0-9])epub([^a-z0-9]|$)`)
	txtRe       = regexp.MustCompile(`(?i)(^|[^a-z0-9])txt([^a-z0-9]|$)`)
	sizeRe      = regexp.MustCompile(`(?i)(\d+(?:[.,]\d+)?)\s*(bytes?|[kmgt]i?b|b)\b`)
	yearRe      = regexp.MustCompile(`\b(19|20)\d{2}\b`)
	onclickURL  = regexp.MustCompile(`['"]([^'"]+)['"]`)
)

func parseDocument(data []byte) (*html.Node, error) {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return doc, nil
}

func walk(n *html.Node, fn func(*html.Node)) {
	if n == nil {
		return
	}
	fn(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, fn)
	}
}

func findFirst(root *html.Node, match func(*html.Node) bool) *html.Node {
	var found *html.Node
	walk(root, func(n *html.Node) {
		if found == nil && match(n) {
			found = n
		}
	})
	return found
}

func findAll(root *html.Node, match func(*html.Node) bool) []*html.Node {
	var out []*html.Node
	walk(root, func(n *html.Node) {
		if match(n) {
			out = append(out, n)
		}
	})
	return out
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

func hasAttr(n *html.Node, key string) bool {
	if n == nil {
		return false
	}
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return true
		}
	}
	return false
}

func firstAttr(n *html.Node, keys ...string) string {
	for _, key := range keys {
		if v := strings.TrimSpace(attr(n, key)); v != "" {
			return v
		}
	}
	return ""
}

func isTag(n *html.Node, tags ...string) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	for _, tag := range tags {
		if strings.EqualFold(n.Data, tag) {
			return true
		}
	}
	return false
}

func hasClass(n *html.Node, class string) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	for _, c := range strings.Fields(attr(n, "class")) {
		if strings.EqualFold(c, class) {
			return true
		}
	}
	return false
}

func hasAnyClass(n *html.Node, classes []string) bool {
	for _, class := range classes {
		if hasClass(n, class) {
			return true
		}
	}
	return false
}

// text returns the visible text of a node, collapsed to single spaces.
func text(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var collect func(*html.Node)
	collect = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			b.WriteString(n.Data)
			b.WriteByte(' ')
			return
		case html.ElementNode:
			switch strings.ToLower(n.Data) {
			case "script", "style", "noscript", "template":
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			collect(c)
		}
	}
	collect(n)
	return source.CleanText(b.String())
}

func textByClass(root *html.Node, classes []string) string {
	n := findFirst(root, func(n *html.Node) bool {
		return n.Type == html.ElementNode && hasAnyClass(n, classes)
	})
	return text(n)
}

func hasAncestorTag(n *html.Node, tags ...string) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if isTag(p, tags...) {
			return true
		}
	}
	return false
}

func isBookHref(href string) bool {
	h := strings.ToLower(strings.TrimSpace(href))
	return strings.Contains(h, "/book/") || strings.HasPrefix(h, "book/")
}

func isHTTPURL(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

// isSearchCard recognizes the three layouts the site has used: the
// div.book-item card, the z-bookcard custom element, and a results table row.
func isSearchCard(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode {
		return false
	}
	if isTag(n, cardTags...) || hasAnyClass(n, cardClasses) {
		return true
	}
	if isTag(n, "tr") && hasAncestorTag(n, "table") {
		return findFirst(n, func(x *html.Node) bool {
			return isTag(x, "a") && isBookHref(attr(x, "href"))
		}) != nil
	}
	return false
}

func hasCardAncestor(n *html.Node) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if isSearchCard(p) {
			return true
		}
	}
	return false
}

func parseSearch(site string, doc *html.Node, limit int) (source.Result, error) {
	var cards []*html.Node
	for _, n := range findAll(doc, isSearchCard) {
		if !hasCardAncestor(n) {
			cards = append(cards, n)
		}
	}

	books := make([]source.Book, 0, len(cards))
	seen := make(map[string]bool, len(cards))
	for _, card := range cards {
		book, ok := parseBookCard(site, card)
		if !ok || seen[book.Key] {
			continue
		}
		seen[book.Key] = true
		books = append(books, book)
	}
	if len(cards) > 0 && len(books) == 0 {
		return source.Result{}, fmt.Errorf("%w: result cards had no usable title or link", source.ErrBadResponse)
	}
	if len(cards) == 0 && !looksLikeCatalog(doc) {
		return source.Result{}, fmt.Errorf("%w: search page has no result cards", source.ErrBadResponse)
	}

	hasMore := hasNextPage(doc) || (len(books) >= limit && limit > 0)
	return source.Result{Books: books, HasMore: hasMore}, nil
}

func looksLikeCatalog(doc *html.Node) bool {
	if hasNoResults(doc) || hasSearchForm(doc) {
		return true
	}
	return findFirst(doc, func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return false
		}
		if hasAnyClass(n, catalogClasses) || isTag(n, cardTags...) {
			return true
		}
		return isTag(n, "table") && (hasAnyClass(n, []string{"res-table", "results", "book-table"}) || isBookHref(attr(n, "href")))
	}) != nil
}

func hasNoResults(doc *html.Node) bool {
	if findFirst(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && hasAnyClass(n, noResultClass)
	}) != nil {
		return true
	}
	low := strings.ToLower(text(doc))
	for _, phrase := range noResultText {
		if strings.Contains(low, phrase) {
			return true
		}
	}
	return false
}

func hasSearchForm(doc *html.Node) bool {
	return findFirst(doc, func(n *html.Node) bool {
		if !isTag(n, "form") {
			return false
		}
		return findFirst(n, func(x *html.Node) bool {
			if !isTag(x, "input") {
				return false
			}
			if strings.EqualFold(attr(x, "type"), "search") {
				return true
			}
			switch strings.ToLower(attr(x, "name")) {
			case "q", "query", "search", "searchkey", "search_key":
				return true
			}
			return false
		}) != nil
	}) != nil
}

func hasNextPage(doc *html.Node) bool {
	return findFirst(doc, func(n *html.Node) bool {
		if !isTag(n, "a", "link") {
			return false
		}
		if strings.EqualFold(attr(n, "rel"), "next") {
			return true
		}
		if hasAnyClass(n, nextClasses) {
			return true
		}
		if strings.Contains(strings.ToLower(attr(n, "aria-label")), "next") {
			return true
		}
		switch strings.ToLower(text(n)) {
		case "next", "next page", "›", "»":
			return true
		}
		return false
	}) != nil
}

func parseBookCard(site string, n *html.Node) (source.Book, bool) {
	book := source.Book{Source: ID}
	extra := make(map[string]string, 3)

	rawDetail := firstAttr(n, "href", "data-href", "data-url", "url")
	if rawDetail == "" {
		link := firstBookLink(n)
		if link != nil {
			rawDetail = firstAttr(link, "href", "data-href", "data-url")
		}
	}
	detail := ""
	if rawDetail != "" {
		detail = source.Combine(site, rawDetail)
	}

	id := firstAttr(n, "data-book-id", "data-id", "book_id", "book-id")
	if id == "" && isTag(n, "z-bookcard") {
		id = strings.TrimSpace(attr(n, "id"))
	}
	if id == "" && detail != "" {
		id = bookIDFromURL(detail)
	}
	if detail == "" && id != "" {
		detail = source.Combine(site, pathBook+url.PathEscape(id))
	}

	key := detail
	if key == "" {
		key = id
	}
	if key == "" {
		return source.Book{}, false
	}
	book.Key = key

	title := firstAttr(n, "title", "book-title", "data-title")
	if title == "" {
		title = textByClass(n, titleClasses)
	}
	if title == "" {
		if link := firstBookLink(n); link != nil {
			title = firstNonEmpty(attr(link, "title"), text(link))
		}
	}
	title = source.CleanText(title)
	if title == "" {
		return source.Book{}, false
	}
	book.Title = title

	author := firstAttr(n, "author", "authors", "data-author")
	if author == "" {
		author = textByClass(n, authorClasses)
	}
	if author == "" {
		author = authorFromLink(n)
	}
	if author == "" && isTag(n, "tr") {
		author = tableRowAuthor(n, title)
	}
	book.Author = source.CleanText(author)

	book.Description = textByClass(n, descClasses)
	book.CoverURL = coverURL(site, n)

	detailText := textByClass(n, detailClasses)
	formatText := firstAttr(n, "extension", "data-extension", "format", "data-format")
	if formatText == "" {
		formatText = detailText
	}
	if isTag(n, "tr") && formatText == "" {
		formatText = tableRowDetails(n, title, book.Author)
	}
	book.Format = detectAnyFormat(formatText)

	sizeText := firstAttr(n, "filesize", "data-filesize", "size", "data-size")
	if sizeText == "" {
		sizeText = formatText
	}
	book.Size = parseSize(sizeText)

	if year := firstAttr(n, "year", "data-year"); year != "" {
		book.Year = year
	} else if year := textByClass(n, yearClasses); year != "" {
		book.Year = source.CleanText(year)
	} else {
		book.Year = yearRe.FindString(formatText)
	}
	book.Language = firstAttr(n, "language", "data-language", "lang")

	if detail != "" {
		extra["detail"] = detail
	}
	if id != "" {
		extra["id"] = id
	}
	if download := firstAttr(n, "data-download", "download-url"); download != "" {
		extra["download"] = source.Combine(site, download)
	}
	if len(extra) > 0 {
		book.Extra = extra
	}
	return book, true
}

func firstBookLink(root *html.Node) *html.Node {
	return findFirst(root, func(n *html.Node) bool {
		return isTag(n, "a") && isBookHref(attr(n, "href"))
	})
}

func bookIDFromURL(rawURL string) string {
	if m := bookIDRe.FindStringSubmatch(rawURL); m != nil {
		return m[1]
	}
	return ""
}

func authorFromLink(root *html.Node) string {
	link := findFirst(root, func(n *html.Node) bool {
		if !isTag(n, "a") {
			return false
		}
		h := strings.ToLower(attr(n, "href"))
		return strings.Contains(h, "/author/") || strings.Contains(h, "/authors/")
	})
	return text(link)
}

func coverURL(site string, n *html.Node) string {
	raw := firstAttr(n, "cover", "data-cover", "cover-url", "data-cover-url", "image")
	img := findFirst(n, func(x *html.Node) bool {
		return isTag(x, "img") && hasAnyClass(x, coverClasses)
	})
	if img == nil {
		img = findFirst(n, func(x *html.Node) bool {
			return isTag(x, "img") && firstAttr(x, "data-src", "data-original", "data-lazy-src", "src") != ""
		})
	}
	if raw == "" && img != nil {
		raw = firstAttr(img, "data-src", "data-original", "data-lazy-src", "src")
	}
	if raw == "" {
		return ""
	}
	resolved := source.Combine(site, raw)
	if isHTTPURL(resolved) {
		return resolved
	}
	return ""
}

func tableRowAuthor(n *html.Node, title string) string {
	for _, cell := range rowCells(n) {
		t := text(cell)
		if t == "" || t == title || looksLikeFileDetails(t) {
			continue
		}
		if firstBookLink(cell) != nil {
			continue
		}
		if yearRe.MatchString(t) && source.CleanText(t) == yearRe.FindString(t) {
			continue
		}
		return t
	}
	return ""
}

func tableRowDetails(n *html.Node, title, author string) string {
	var parts []string
	for _, cell := range rowCells(n) {
		t := text(cell)
		if t == "" || t == title || t == author {
			continue
		}
		parts = append(parts, t)
	}
	return strings.Join(parts, " ")
}

func rowCells(n *html.Node) []*html.Node {
	var cells []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if isTag(c, "td", "th") {
			cells = append(cells, c)
		}
	}
	return cells
}

func looksLikeFileDetails(s string) bool {
	return detectAnyFormat(s) != "" || sizeRe.MatchString(s)
}

func detectAnyFormat(s string) string {
	m := anyFormatRe.FindStringSubmatch(strings.ToLower(s))
	if m == nil {
		return ""
	}
	return m[2]
}

func parseSize(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
		return n
	}
	m := sizeRe.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	value, err := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", "."), 64)
	if err != nil {
		return 0
	}
	var multiplier float64
	switch strings.ToLower(m[2]) {
	case "b", "byte", "bytes":
		multiplier = 1
	case "kb", "kib":
		multiplier = 1 << 10
	case "mb", "mib":
		multiplier = 1 << 20
	case "gb", "gib":
		multiplier = 1 << 30
	case "tb", "tib":
		multiplier = 1 << 40
	}
	return int64(value * multiplier)
}

type bookDetail struct {
	Title       string
	Author      string
	Description string
	CoverURL    string
	Downloads   []downloadLink
}

type downloadLink struct {
	URL    string
	Format string
}

func parseDetail(site, pageURL string, doc *html.Node) (bookDetail, error) {
	detail := bookDetail{
		Title:       detailTitle(doc),
		Author:      detailAuthor(doc),
		Description: detailDescription(doc),
		CoverURL:    detailCover(site, doc),
		Downloads:   collectDownloadLinks(site, pageURL, doc),
	}
	if detail.Title == "" && len(detail.Downloads) == 0 {
		return detail, fmt.Errorf("%w: detail page has no title or download links", source.ErrBadResponse)
	}
	return detail, nil
}

func detailTitle(doc *html.Node) string {
	if n := findFirst(doc, func(n *html.Node) bool {
		return isTag(n, "h1") && text(n) != ""
	}); n != nil {
		return text(n)
	}
	if title := textByClass(doc, titleClasses); title != "" {
		return title
	}
	if content := metaContent(doc, "og:title"); content != "" {
		return content
	}
	if n := findFirst(doc, func(n *html.Node) bool { return isTag(n, "title") }); n != nil {
		title := text(n)
		if i := strings.LastIndexAny(title, "|–—"); i > 0 {
			title = title[:i]
		}
		return source.CleanText(title)
	}
	return ""
}

func detailAuthor(doc *html.Node) string {
	if author := textByClass(doc, authorClasses); author != "" {
		return author
	}
	if author := authorFromLink(doc); author != "" {
		return author
	}
	return source.CleanText(metaContent(doc, "author"))
}

func detailDescription(doc *html.Node) string {
	if desc := textByClass(doc, descClasses); desc != "" {
		return desc
	}
	return source.CleanText(metaContent(doc, "description"))
}

func detailCover(site string, doc *html.Node) string {
	raw := metaContent(doc, "og:image")
	if raw == "" {
		img := findFirst(doc, func(n *html.Node) bool {
			return isTag(n, "img") && (hasAnyClass(n, coverClasses) || strings.EqualFold(attr(n, "itemprop"), "image"))
		})
		if img != nil {
			raw = firstAttr(img, "data-src", "data-original", "src")
		}
	}
	if raw == "" {
		return ""
	}
	resolved := source.Combine(site, raw)
	if isHTTPURL(resolved) {
		return resolved
	}
	return ""
}

func metaContent(doc *html.Node, key string) string {
	n := findFirst(doc, func(n *html.Node) bool {
		if !isTag(n, "meta") {
			return false
		}
		property := strings.ToLower(attr(n, "property"))
		name := strings.ToLower(attr(n, "name"))
		return property == strings.ToLower(key) || name == strings.ToLower(key)
	})
	return source.CleanText(attr(n, "content"))
}

// collectDownloadLinks finds anchors, forms and data-href elements that point
// at an EPUB or TXT file. Unknown targets are accepted only when they look
// like a direct download, so a "download our app" link is ignored.
func collectDownloadLinks(site, pageURL string, doc *html.Node) []downloadLink {
	type candidate struct {
		link downloadLink
		rank int
	}
	var candidates []candidate
	seen := make(map[string]int)

	walk(doc, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		var ref string
		switch {
		case isTag(n, "a"):
			ref = firstAttr(n, "href", "data-href", "data-url", "data-download")
		case isTag(n, "form"):
			ref = firstAttr(n, "action", "data-href", "data-url")
		default:
			ref = firstAttr(n, "data-href", "data-url", "data-download")
		}
		if ref == "" {
			ref = onclickRef(attr(n, "onclick"))
		}
		ref = strings.TrimSpace(ref)
		if ref == "" {
			return
		}
		low := strings.ToLower(ref)
		if strings.HasPrefix(low, "#") || strings.HasPrefix(low, "javascript:") ||
			strings.HasPrefix(low, "mailto:") || strings.HasPrefix(low, "data:") {
			return
		}

		format := downloadFormat(n, ref)
		if format != "" && format != "epub" && format != "txt" {
			return
		}
		if format == "" && !directDownload(n, ref) {
			return
		}

		abs := source.Combine(pageURL, ref)
		if abs == "" {
			abs = source.Combine(site, ref)
		}
		if !isHTTPURL(abs) {
			return
		}
		rank := map[string]int{"": 1, "txt": 2, "epub": 3}[format]
		if at, ok := seen[abs]; ok {
			if rank > candidates[at].rank {
				candidates[at].link.Format = format
				candidates[at].rank = rank
			}
			return
		}
		seen[abs] = len(candidates)
		candidates = append(candidates, candidate{link: downloadLink{URL: abs, Format: format}, rank: rank})
	})

	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].rank > candidates[j].rank
	})
	out := make([]downloadLink, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, c.link)
	}
	return out
}

func onclickRef(onclick string) string {
	if onclick == "" {
		return ""
	}
	for _, m := range onclickURL.FindAllStringSubmatch(onclick, -1) {
		ref := strings.TrimSpace(m[1])
		low := strings.ToLower(ref)
		if strings.Contains(low, "/dl/") || strings.Contains(low, "download") {
			return ref
		}
	}
	return ""
}

func downloadFormat(n *html.Node, ref string) string {
	for _, key := range []string{"data-extension", "data-format", "extension", "format", "data-type", "type"} {
		if f := downloadFormatOf(attr(n, key)); f != "" {
			return f
		}
	}
	if f := downloadFormatOf(text(n)); f != "" {
		return f
	}
	if f := downloadFormatOf(attr(n, "aria-label")); f != "" {
		return f
	}
	if f := downloadFormatOf(attr(n, "title")); f != "" {
		return f
	}
	return downloadFormatFromURL(ref)
}

func downloadFormatOf(s string) string {
	low := strings.ToLower(s)
	switch {
	case epubRe.MatchString(low):
		return "epub"
	case txtRe.MatchString(low):
		return "txt"
	}
	return ""
}

func downloadFormatFromURL(ref string) string {
	full := strings.ToLower(ref)
	switch {
	case strings.Contains(full, "format=epub"), strings.Contains(full, "extension=epub"):
		return "epub"
	case strings.Contains(full, "format=txt"), strings.Contains(full, "extension=txt"):
		return "txt"
	}
	low := full
	if i := strings.IndexAny(low, "?#"); i >= 0 {
		low = low[:i]
	}
	switch {
	case strings.HasSuffix(low, ".epub"), strings.Contains(low, "/epub"):
		return "epub"
	case strings.HasSuffix(low, ".txt"), strings.Contains(low, "/txt"):
		return "txt"
	}
	return ""
}

func directDownload(n *html.Node, ref string) bool {
	lowURL := strings.ToLower(ref)
	if strings.Contains(lowURL, "/dl/") || strings.Contains(lowURL, "download") {
		return true
	}
	if hasAttr(n, "download") {
		return true
	}
	for _, c := range strings.Fields(strings.ToLower(attr(n, "class") + " " + attr(n, "id"))) {
		if strings.Contains(c, "dlbutton") || strings.Contains(c, "download") || c == "dl" {
			return true
		}
	}
	text := strings.ToLower(text(n))
	return text == "download" || text == "download book" || text == "下载"
}

// findLoginForm finds the form that carries a password input. It is used both
// to log in and to recognize a page that still wants credentials.
func findLoginForm(doc *html.Node) *html.Node {
	return findFirst(doc, func(n *html.Node) bool {
		if !isTag(n, "form") {
			return false
		}
		return findFirst(n, isPasswordInput) != nil
	})
}

func isPasswordInput(n *html.Node) bool {
	if !isTag(n, "input") {
		return false
	}
	if strings.EqualFold(attr(n, "type"), "password") {
		return true
	}
	return strings.EqualFold(attr(n, "name"), "password")
}

func loginValues(form *html.Node, email, password string) url.Values {
	values := make(url.Values)
	emailSet := false
	var fallbackEmail string

	walk(form, func(n *html.Node) {
		if !isTag(n, "input") {
			return
		}
		name := strings.TrimSpace(attr(n, "name"))
		if name == "" {
			return
		}
		typ := strings.ToLower(attr(n, "type"))
		switch typ {
		case "hidden":
			values.Set(name, attr(n, "value"))
		case "password":
			values.Set(name, password)
		case "email":
			values.Set(name, email)
			emailSet = true
		case "checkbox", "radio", "submit", "button", "reset", "file", "image":
			// Not needed to authenticate; some forms require checked boxes,
			// but submitting unknown values is worse than leaving them out.
		default:
			switch strings.ToLower(name) {
			case "email", "username", "user", "login", "login_name":
				values.Set(name, email)
				emailSet = true
			case "password", "pass", "passwd":
				values.Set(name, password)
			default:
				if fallbackEmail == "" {
					fallbackEmail = name
				}
			}
		}
	})
	if !emailSet && fallbackEmail != "" {
		values.Set(fallbackEmail, email)
	}
	return values
}

func isLoginDocument(doc *html.Node) bool {
	return findLoginForm(doc) != nil
}

func isLoginBody(data []byte) bool {
	doc, err := parseDocument(data)
	if err != nil {
		return false
	}
	return isLoginDocument(doc)
}
