package legado

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/laix/quire/internal/epubwrite"
	"github.com/laix/quire/internal/source"
	"golang.org/x/net/html"
	"golang.org/x/text/encoding/simplifiedchinese"
)

const (
	id   source.ID = "legado"
	name           = "阅读 3.0"
)

const defaultUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.0"

// Builder produces an EPUB from parsed metadata and chapters.
type Builder = func(epubwrite.Book, []epubwrite.Chapter) ([]byte, error)

// Options configures a Legado source.
type Options struct {
	Client      *http.Client
	Timeout     time.Duration
	MaxBytes    int64
	MaxChapters int
	Concurrency int
	Build       Builder
}

// Source implements source.Source and source.Importer for Legado 3.0 definitions.
type Source struct {
	client      *http.Client
	timeout     time.Duration
	maxBytes    int64
	maxChapters int
	concurrency int
	build       Builder

	mu   sync.RWMutex
	defs []Definition
}

// New returns a source with the given options and defaults.
func New(opts Options) *Source {
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 8 << 20
	}
	maxChapters := opts.MaxChapters
	if maxChapters <= 0 {
		maxChapters = 5000
	}
	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = 4
	}
	build := opts.Build
	if build == nil {
		build = epubwrite.Bytes
	}
	return &Source{
		client:      client,
		timeout:     timeout,
		maxBytes:    maxBytes,
		maxChapters: maxChapters,
		concurrency: concurrency,
		build:       build,
	}
}

// ID returns the stable source identifier.
func (s *Source) ID() source.ID { return id }

// Name returns the human-readable source name.
func (s *Source) Name() string { return name }

// Caps reports what this source can do.
func (s *Source) Caps() source.Caps {
	return source.Caps{Search: true, Download: true, NeedsLogin: false}
}

// Import replaces the current definitions with those parsed from data.
// It accepts a JSON array or a single JSON object. Invalid single entries are
// skipped; a JSON syntax error fails the whole import.
func (s *Source) Import(data []byte) (int, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return 0, fmt.Errorf("%w: empty import data", source.ErrBadResponse)
	}

	var raw []json.RawMessage
	switch data[0] {
	case '[':
		if err := json.Unmarshal(data, &raw); err != nil {
			return 0, err
		}
	case '{':
		raw = []json.RawMessage{data}
	default:
		return 0, fmt.Errorf("%w: import data must be a JSON object or array", source.ErrBadResponse)
	}

	var defs []Definition
	seen := make(map[string]bool)
	for _, r := range raw {
		var d Definition
		if err := json.Unmarshal(r, &d); err != nil {
			continue
		}
		if !s.validDefinition(&d) {
			continue
		}
		u := strings.TrimSpace(d.BookSourceURL)
		if seen[u] {
			continue
		}
		seen[u] = true
		defs = append(defs, d)
	}

	s.mu.Lock()
	s.defs = defs
	s.mu.Unlock()
	return len(defs), nil
}

// Export returns the current definitions as a formatted JSON array.
func (s *Source) Export() ([]byte, error) {
	s.mu.RLock()
	defs := append([]Definition(nil), s.defs...)
	s.mu.RUnlock()
	return json.MarshalIndent(defs, "", "  ")
}

// Search searches every enabled definition concurrently and aggregates results
// in definition order.
func (s *Source) Search(ctx context.Context, q source.Query) (source.Result, error) {
	s.mu.RLock()
	defs := append([]Definition(nil), s.defs...)
	s.mu.RUnlock()

	page := source.PageOf(q)
	results := make([][]source.Book, len(defs))
	var firstErr error
	var errOnce sync.Once

	var wg sync.WaitGroup
	sem := make(chan struct{}, s.concurrency)
	for i := range defs {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()
			books, err := s.searchOne(ctx, &defs[idx], idx, q.Text, page)
			if err != nil {
				errOnce.Do(func() { firstErr = err })
				return
			}
			results[idx] = books
		}(i)
	}
	wg.Wait()

	var out source.Result
	for _, list := range results {
		out.Books = append(out.Books, list...)
	}
	if len(out.Books) == 0 && firstErr != nil {
		return source.Result{}, firstErr
	}
	return out, nil
}

// Download fetches the book identified by b using the stored definition index.
func (s *Source) Download(ctx context.Context, b source.Book, progress func(source.Progress)) (source.File, error) {
	defIdxStr, ok := b.Extra["def"]
	if !ok {
		return source.File{}, fmt.Errorf("%w: missing definition index", source.ErrBadResponse)
	}
	defIdx, err := strconv.Atoi(defIdxStr)
	if err != nil {
		return source.File{}, fmt.Errorf("%w: invalid definition index", source.ErrBadResponse)
	}
	s.mu.RLock()
	if defIdx < 0 || defIdx >= len(s.defs) {
		s.mu.RUnlock()
		return source.File{}, fmt.Errorf("%w: definition index out of range", source.ErrBadResponse)
	}
	def := s.defs[defIdx]
	s.mu.RUnlock()

	baseURL := def.BookSourceURL
	bookURL := b.Extra["bookUrl"]
	if bookURL == "" {
		bookURL = b.Key
	}

	// Optional book-info page.
	var bookTitle, bookAuthor, bookIntro, bookCover string
	tocURL := bookURL
	if hasBookInfoRule(&def) {
		body, _, err := s.fetch(ctx, &def, bookURL, requestOpts{})
		if err != nil {
			return source.File{}, err
		}
		doc, err := parseHTML(body)
		if err != nil {
			return source.File{}, fmt.Errorf("%w: cannot parse book info page", source.ErrBadResponse)
		}
		if def.RuleBookInfo.Name != "" {
			bookTitle, _ = evalField(doc, baseURL, def.RuleBookInfo.Name)
		}
		if def.RuleBookInfo.Author != "" {
			bookAuthor, _ = evalField(doc, baseURL, def.RuleBookInfo.Author)
		}
		if def.RuleBookInfo.Intro != "" {
			bookIntro, _ = evalField(doc, baseURL, def.RuleBookInfo.Intro)
		}
		if def.RuleBookInfo.CoverURL != "" {
			bookCover, _ = evalField(doc, baseURL, def.RuleBookInfo.CoverURL)
		}
		if def.RuleBookInfo.TocURL != "" {
			t, err := evalField(doc, baseURL, def.RuleBookInfo.TocURL)
			if err == nil && t != "" {
				tocURL = source.Combine(baseURL, t)
			}
		}
	}
	if bookTitle == "" {
		bookTitle = b.Title
	}
	if bookAuthor == "" {
		bookAuthor = b.Author
	}

	// Table of contents.
	body, _, err := s.fetch(ctx, &def, tocURL, requestOpts{})
	if err != nil {
		return source.File{}, err
	}
	doc, err := parseHTML(body)
	if err != nil {
		return source.File{}, fmt.Errorf("%w: cannot parse toc page", source.ErrBadResponse)
	}
	items, err := selectList(doc, baseURL, def.RuleToc.ChapterList)
	if err != nil {
		return source.File{}, err
	}
	if len(items) == 0 {
		return source.File{}, fmt.Errorf("%w: no chapters found", source.ErrNotFound)
	}
	if len(items) > s.maxChapters {
		items = items[:s.maxChapters]
	}

	type chap struct {
		title string
		url   string
	}
	chapters := make([]chap, 0, len(items))
	for _, it := range items {
		title, err := evalField(it, baseURL, def.RuleToc.ChapterName)
		if err != nil {
			return source.File{}, err
		}
		u, err := evalField(it, baseURL, def.RuleToc.ChapterURL)
		if err != nil {
			return source.File{}, err
		}
		if u == "" {
			continue
		}
		chapters = append(chapters, chap{title: title, url: source.Combine(baseURL, u)})
	}
	if len(chapters) == 0 {
		return source.File{}, fmt.Errorf("%w: no chapter urls found", source.ErrNotFound)
	}

	// Fetch chapters concurrently.
	htmls := make([]string, len(chapters))
	var done int64
	var firstErr error
	var errOnce sync.Once

	var wg sync.WaitGroup
	sem := make(chan struct{}, s.concurrency)
	for i, c := range chapters {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, c chap) {
			defer wg.Done()
			defer func() { <-sem }()
			body, _, err := s.fetch(ctx, &def, c.url, requestOpts{})
			if err != nil {
				errOnce.Do(func() { firstErr = err })
				return
			}
			doc, err := parseHTML(body)
			if err != nil {
				errOnce.Do(func() { firstErr = fmt.Errorf("%w: cannot parse chapter page", source.ErrBadResponse) })
				return
			}
			html, err := evalField(doc, baseURL, def.RuleContent.Content)
			if err != nil {
				errOnce.Do(func() { firstErr = err })
				return
			}
			htmls[idx] = html
			n := atomic.AddInt64(&done, 1)
			if progress != nil {
				progress(source.Progress{Stage: "chapters", Done: n, Total: int64(len(chapters))})
			}
		}(i, c)
	}
	wg.Wait()
	if firstErr != nil {
		return source.File{}, firstErr
	}

	var ewChapters []epubwrite.Chapter
	for i, c := range chapters {
		ewChapters = append(ewChapters, epubwrite.Chapter{Title: c.title, HTML: htmls[i]})
	}
	book := epubwrite.Book{
		Title:       bookTitle,
		Author:      bookAuthor,
		Description: bookIntro,
		Source:      def.BookSourceURL,
	}
	if bookCover != "" {
		// epubwrite does not download remote images; keep cover empty.
		_ = bookCover
	}
	data, err := s.build(book, ewChapters)
	if err != nil {
		return source.File{}, fmt.Errorf("%w: build failed: %v", source.ErrBadResponse, err)
	}

	name := bookTitle
	if name == "" {
		name = "untitled"
	}
	return source.File{
		Name:   sanitizeFileName(name) + ".epub",
		Format: "epub",
		Data:   data,
	}, nil
}

// searchOne runs one definition's search page.
func (s *Source) searchOne(ctx context.Context, def *Definition, idx int, key string, page int) ([]source.Book, error) {
	url, opts, err := buildSearchURL(def.BookSourceURL, def.SearchURL, key, page)
	if err != nil {
		return nil, err
	}
	body, ctype, err := s.fetch(ctx, def, url, opts)
	if err != nil {
		return nil, err
	}

	var listCtx interface{}
	if isJSONResponse(ctype, body) {
		var v interface{}
		if err := json.Unmarshal(body, &v); err != nil {
			return nil, fmt.Errorf("%w: invalid search JSON: %v", source.ErrBadResponse, err)
		}
		listCtx = v
	} else {
		doc, err := parseHTML(body)
		if err != nil {
			return nil, fmt.Errorf("%w: cannot parse search page", source.ErrBadResponse)
		}
		listCtx = doc
	}

	items, err := selectList(listCtx, def.BookSourceURL, def.RuleSearch.BookList)
	if err != nil {
		return nil, err
	}

	var out []source.Book
	for _, it := range items {
		title, err := evalField(it, def.BookSourceURL, def.RuleSearch.Name)
		if err != nil {
			return nil, err
		}
		title = source.CleanText(title)
		if title == "" {
			continue
		}
		author, _ := evalField(it, def.BookSourceURL, def.RuleSearch.Author)
		intro, _ := evalField(it, def.BookSourceURL, def.RuleSearch.Intro)
		coverURL, _ := evalField(it, def.BookSourceURL, def.RuleSearch.CoverURL)
		bookURL, err := evalField(it, def.BookSourceURL, def.RuleSearch.BookURL)
		if err != nil {
			return nil, err
		}
		if bookURL == "" {
			continue
		}
		bookURL = source.Combine(def.BookSourceURL, bookURL)
		out = append(out, source.Book{
			Source:      id,
			Key:         bookURL,
			Title:       title,
			Author:      source.CleanText(author),
			Description: source.CleanText(intro),
			CoverURL:    coverURL,
			Extra: map[string]string{
				"def":     strconv.Itoa(idx),
				"bookUrl": bookURL,
			},
		})
	}
	return out, nil
}

// fetch performs one HTTP request with timeouts, limits and charset decoding.
func (s *Source) fetch(ctx context.Context, def *Definition, rawURL string, opts requestOpts) ([]byte, string, error) {
	if rawURL == "" {
		return nil, "", fmt.Errorf("%w: empty url", source.ErrBadResponse)
	}
	u := rawURL
	if !hasScheme(u) {
		u = source.Combine(def.BookSourceURL, u)
	}
	if u == "" {
		return nil, "", fmt.Errorf("%w: cannot resolve url %q", source.ErrBadResponse, rawURL)
	}

	method := strings.ToUpper(opts.Method)
	if method == "" {
		method = http.MethodGet
	}
	var bodyReader io.Reader
	if opts.Body != "" {
		bodyReader = strings.NewReader(opts.Body)
	}

	reqCtx, cancel := source.Deadline(ctx, s.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, method, u, bodyReader)
	if err != nil {
		return nil, "", fmt.Errorf("%w: invalid url %q: %v", source.ErrBadResponse, u, err)
	}
	req.Header.Set("User-Agent", defaultUA)
	if bodyReader != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		if reqCtx.Err() == context.DeadlineExceeded {
			return nil, "", fmt.Errorf("%w: request timeout for %q", source.ErrBadResponse, u)
		}
		return nil, "", fmt.Errorf("%w: %v", source.ErrBadResponse, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusPartialContent:
		// ok
	case http.StatusNotFound:
		return nil, "", fmt.Errorf("%w: %s", source.ErrNotFound, u)
	case http.StatusTooManyRequests:
		return nil, "", fmt.Errorf("%w: %s", source.ErrRateLimited, u)
	default:
		return nil, "", fmt.Errorf("%w: HTTP %d for %s", source.ErrBadResponse, resp.StatusCode, u)
	}

	limited := io.LimitReader(resp.Body, s.maxBytes)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, "", fmt.Errorf("%w: read body: %v", source.ErrBadResponse, err)
	}

	charset := opts.Charset
	if charset == "" {
		charset = detectCharset(data, resp.Header.Get("Content-Type"))
	}
	if charset == "" && def != nil {
		charset = def.BookSourceCharset
	}
	data, err = decodeCharset(data, charset)
	if err != nil {
		return nil, "", fmt.Errorf("%w: decode charset %q: %v", source.ErrBadResponse, charset, err)
	}
	return data, resp.Header.Get("Content-Type"), nil
}

// hasScheme is a small helper; source.hasScheme is not exported.
func hasScheme(s string) bool {
	i := strings.Index(s, "://")
	if i <= 0 {
		return false
	}
	for _, r := range s[:i] {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}

// buildSearchURL expands the searchUrl template and parses its trailing options.
func buildSearchURL(baseURL, tpl, key string, page int) (string, requestOpts, error) {
	rule, opts, err := parseOptions(tpl)
	if err != nil {
		return "", requestOpts{}, err
	}
	rule = expandTemplates(rule, key, page, baseURL)
	opts.Body = expandTemplates(opts.Body, key, page, baseURL)
	if !hasScheme(rule) {
		rule = source.Combine(baseURL, rule)
	}
	return rule, opts, nil
}

func expandTemplates(s, key string, page int, baseURL string) string {
	s = strings.ReplaceAll(s, "{{key}}", url.QueryEscape(key))
	s = strings.ReplaceAll(s, "{{searchKey}}", url.QueryEscape(key))
	s = strings.ReplaceAll(s, "{{page}}", strconv.Itoa(page))
	s = strings.ReplaceAll(s, "{{baseUrl}}", baseURL)
	return s
}

func hasBookInfoRule(def *Definition) bool {
	ri := def.RuleBookInfo
	return ri.Name != "" || ri.Author != "" || ri.Intro != "" || ri.CoverURL != "" || ri.TocURL != ""
}

func parseHTML(data []byte) (*html.Node, error) {
	return html.Parse(bytes.NewReader(data))
}

func isJSONResponse(ctype string, data []byte) bool {
	mt, _, _ := mime.ParseMediaType(ctype)
	if mt == "application/json" || mt == "text/json" {
		return true
	}
	trimmed := bytes.TrimSpace(data)
	return len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[')
}

func detectCharset(body []byte, ctype string) string {
	if _, params, err := mime.ParseMediaType(ctype); err == nil {
		if cs, ok := params["charset"]; ok {
			return cs
		}
	}
	s := string(body)
	if len(s) > 4096 {
		s = s[:4096]
	}
	if m := metaCharsetRe.FindStringSubmatch(s); m != nil {
		return strings.TrimSpace(m[1])
	}
	if m := metaContentTypeRe.FindStringSubmatch(s); m != nil {
		if _, params, err := mime.ParseMediaType(m[1]); err == nil {
			if cs, ok := params["charset"]; ok {
				return cs
			}
		}
	}
	return ""
}

var metaCharsetRe = regexp.MustCompile(`(?i)<meta[^>]+charset\s*=\s*["']?([^"'>;\s]+)`)
var metaContentTypeRe = regexp.MustCompile(`(?i)<meta[^>]+http-equiv\s*=\s*["']?\s*Content-Type\s*["']?[^>]+content\s*=\s*["']([^"']+)["']`)

func decodeCharset(data []byte, charset string) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(charset)) {
	case "", "utf-8", "utf8":
		return data, nil
	case "gbk", "gb2312", "gb18030":
		return simplifiedchinese.GB18030.NewDecoder().Bytes(data)
	default:
		return data, nil
	}
}

func sanitizeFileName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			b.WriteRune('_')
		case 0:
			// drop control chars
		default:
			b.WriteRune(r)
		}
	}
	name := strings.TrimSpace(b.String())
	if name == "" {
		return "untitled"
	}
	return name
}

// validDefinition reports whether a definition is usable by this engine.
func (s *Source) validDefinition(d *Definition) bool {
	if d.Enabled != nil && !*d.Enabled {
		return false
	}
	if strings.TrimSpace(d.BookSourceName) == "" ||
		strings.TrimSpace(d.BookSourceURL) == "" ||
		strings.TrimSpace(d.SearchURL) == "" {
		return false
	}
	if strings.TrimSpace(d.RuleSearch.BookList) == "" {
		return false
	}
	base := d.BookSourceURL

	if err := checkRule(d.RuleSearch.BookList); err != nil {
		return false
	}
	if err := checkRule(d.RuleSearch.Name); err != nil {
		return false
	}
	for _, r := range []string{
		d.RuleSearch.Author, d.RuleSearch.Kind, d.RuleSearch.WordCount,
		d.RuleSearch.LastChapter, d.RuleSearch.Intro, d.RuleSearch.CoverURL, d.RuleSearch.BookURL,
	} {
		if r != "" {
			if err := checkRule(r); err != nil {
				return false
			}
		}
	}
	for _, r := range []string{
		d.RuleBookInfo.Name, d.RuleBookInfo.Author, d.RuleBookInfo.Intro,
		d.RuleBookInfo.CoverURL, d.RuleBookInfo.TocURL,
	} {
		if r != "" {
			if err := checkRule(r); err != nil {
				return false
			}
		}
	}
	if d.RuleToc.ChapterList != "" {
		if err := checkRule(d.RuleToc.ChapterList); err != nil {
			return false
		}
		if err := checkRule(d.RuleToc.ChapterName); err != nil {
			return false
		}
		if err := checkRule(d.RuleToc.ChapterURL); err != nil {
			return false
		}
	}
	if d.RuleContent.Content != "" {
		if err := checkRule(d.RuleContent.Content); err != nil {
			return false
		}
	}
	// Validate the searchUrl template can be expanded without unsupported rules.
	if _, _, err := buildSearchURL(base, d.SearchURL, "x", 1); err != nil {
		return false
	}
	return true
}

// checkRule reports whether a rule can be parsed by this engine.
func checkRule(rule string) error {
	if strings.TrimSpace(rule) == "" {
		return nil
	}
	rule, _, err := parseOptions(rule)
	if err != nil {
		return err
	}
	if isUnsupported(rule) {
		return source.ErrUnsupported
	}
	selector, re, _, hasRegex := splitRegex(rule)
	if hasRegex && re == nil {
		return source.ErrUnsupported
	}
	if isJSONRule(selector) {
		_, err := jsonPath(nil, strings.TrimPrefix(strings.TrimPrefix(selector, "@json:"), "$"))
		_ = err
		return nil
	}
	return checkCSSChain(selector)
}

// checkCSSChain validates each selector segment of a Legado @-chain.
func checkCSSChain(rule string) error {
	segs := splitChain(rule)
	if len(segs) > 0 && isExtraction(segs[len(segs)-1]) {
		segs = segs[:len(segs)-1]
	}
	for _, seg := range segs {
		seg = strings.TrimPrefix(seg, "@css:")
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		if _, err := parseSelectorGroup(seg); err != nil {
			return err
		}
	}
	return nil
}
