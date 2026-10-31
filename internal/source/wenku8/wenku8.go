// Package wenku8 implements the 轻小说文库 (wenku8) book source.
//
// The source reads only public pages: search results, a book's detail page,
// its table of contents and the chapter pages. It never logs in and never
// asks for anything behind a permission check.
//
// Chapters are fetched concurrently, a bounded number at a time, then handed
// to epubwrite through Builder. The package itself knows nothing about the
// EPUB container, so tests can replace the writer and assert exactly which
// metadata and chapter HTML a download produced.
package wenku8

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"

	"github.com/laix/quire/internal/epubwrite"
	"github.com/laix/quire/internal/source"
)

// ID is the registry id and the Source field of every Book this package
// returns. It is stable: changing it orphans saved settings.
const ID source.ID = "wenku8"

// Defaults for Options. The site URL is a default, not a promise: the site
// moves, so it is exposed through Settings as well.
const (
	DefaultBaseURL     = "https://www.wenku8.net"
	DefaultTimeout     = 20 * time.Second
	DefaultMaxBytes    = 4 << 20
	DefaultMaxChapters = 2000
	DefaultConcurrency = 4
	maxConcurrency     = 16
	userAgent          = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Quire/1.0"
	retryDelay         = 200 * time.Millisecond
	maxCoverBytes      = 4 << 20
)

// Builder turns the book metadata and chapters into an EPUB. It is exported
// so tests and hosts can substitute the writer; nil means epubwrite.Bytes.
type Builder func(epubwrite.Book, []epubwrite.Chapter) ([]byte, error)

// Options configure a Source.
type Options struct {
	// BaseURL is the site root, default DefaultBaseURL. Tests point it at an
	// httptest server.
	BaseURL string
	// Client is the HTTP client to use; nil means a fresh one.
	Client *http.Client
	// Timeout is the per-request deadline, default DefaultTimeout.
	Timeout time.Duration
	// MaxBytes refuses a single page larger than this, default
	// DefaultMaxBytes.
	MaxBytes int64
	// MaxChapters refuses a book with more chapters, default
	// DefaultMaxChapters.
	MaxChapters int
	// Concurrency is the number of chapter pages fetched at once. Default
	// DefaultConcurrency; values below 1 fall back to the default and values
	// above maxConcurrency are clamped.
	Concurrency int
	// Build is the EPUB writer; nil means epubwrite.Bytes.
	Build Builder
}

// Source is the 轻小说文库 book source. It is safe for concurrent use; the
// only mutable state is the configured site URL.
type Source struct {
	client      *http.Client
	timeout     time.Duration
	maxBytes    int64
	maxChapters int
	concurrency int
	build       Builder

	mu      sync.RWMutex
	baseURL string
}

// New returns a Source configured by opts.
func New(opts Options) *Source {
	base := strings.TrimSpace(opts.BaseURL)
	if base == "" {
		base = DefaultBaseURL
	}
	base = strings.TrimRight(base, "/")

	client := opts.Client
	if client == nil {
		client = &http.Client{}
	} else {
		// Copy so a shared client is not mutated by a source.
		c := *client
		client = &c
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	maxChapters := opts.MaxChapters
	if maxChapters <= 0 {
		maxChapters = DefaultMaxChapters
	}
	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = DefaultConcurrency
	}
	if concurrency > maxConcurrency {
		concurrency = maxConcurrency
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
		baseURL:     base,
	}
}

// ID implements source.Source.
func (s *Source) ID() source.ID { return ID }

// Name implements source.Source.
func (s *Source) Name() string { return "轻小说文库" }

// Caps implements source.Source.
func (s *Source) Caps() source.Caps {
	return source.Caps{Search: true, Download: true, NeedsLogin: false}
}

// Settings implements source.Configurable. Only the site URL is exposed: the
// site changes domains, and everything else is public.
func (s *Source) Settings() []source.Setting {
	return []source.Setting{{
		Key:         "site",
		Label:       "Site URL",
		Help:        "Base URL of 轻小说文库; change it when the site moves.",
		Placeholder: DefaultBaseURL,
		Value:       s.base(),
	}}
}

// Configure implements source.Configurable. Keys missing from values keep
// their current value, so the host may send a partial form.
func (s *Source) Configure(values map[string]string) error {
	raw, ok := values["site"]
	if !ok {
		return nil
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		// site is not Required: clearing it means "use the default", not an
		// error the host has to explain to the reader.
		s.mu.Lock()
		s.baseURL = DefaultBaseURL
		s.mu.Unlock()
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("wenku8: site URL %q: %w", raw, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("wenku8: site URL %q is not an http(s) URL", raw)
	}
	s.mu.Lock()
	s.baseURL = strings.TrimRight(raw, "/")
	s.mu.Unlock()
	return nil
}

func (s *Source) base() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.baseURL
}

// Search implements source.Source. An empty query asks the site for nothing
// and returns no books, because this catalogue is only reachable by keyword.
func (s *Source) Search(ctx context.Context, q source.Query) (source.Result, error) {
	key := strings.TrimSpace(q.Text)
	if key == "" {
		return source.Result{}, nil
	}
	page := source.PageOf(q)
	params := url.Values{}
	params.Set("searchkey", key)
	params.Set("searchtype", "all")
	if page > 1 {
		// The site's search script accepts page as a query parameter; page 1
		// is the default, so omitting it keeps the URL canonical.
		params.Set("page", strconv.Itoa(page))
	}
	rawURL := s.base() + "/modules/article/search.php?" + params.Encode()

	doc, finalURL, err := s.fetch(ctx, rawURL)
	if err != nil {
		return source.Result{}, err
	}
	books, hasMore, err := parseSearch(doc, finalURL, page)
	if err != nil {
		return source.Result{}, err
	}

	limit := source.LimitOf(q, 25)
	if limit > 0 && len(books) > limit {
		books = books[:limit]
		hasMore = true
	}
	return source.Result{Books: books, HasMore: hasMore}, nil
}

// Download implements source.Source. It reads the detail page, the table of
// contents and every chapter page, then asks Builder for the EPUB bytes.
func (s *Source) Download(ctx context.Context, b source.Book, progress func(source.Progress)) (source.File, error) {
	detailURL, id, err := s.detailURL(b)
	if err != nil {
		return source.File{}, err
	}

	doc, finalURL, err := s.fetch(ctx, detailURL)
	if err != nil {
		return source.File{}, err
	}
	info, err := parseBook(doc, finalURL)
	if err != nil {
		return source.File{}, err
	}
	if info.Title == "" {
		info.Title = strings.TrimSpace(b.Title)
	}
	if info.Author == "" {
		info.Author = strings.TrimSpace(b.Author)
	}
	if info.Description == "" {
		info.Description = strings.TrimSpace(b.Description)
	}
	if info.CoverURL == "" {
		info.CoverURL = strings.TrimSpace(b.CoverURL)
	}

	catalogURL := info.CatalogURL
	if catalogURL == "" {
		catalogURL = defaultCatalogURL(finalURL, id)
	}
	catalogDoc, catalogFinalURL, err := s.fetch(ctx, catalogURL)
	if err != nil {
		return source.File{}, err
	}
	refs := parseCatalog(catalogDoc, catalogFinalURL, id)
	if len(refs) == 0 {
		// Some layouts put the chapter list on the detail page itself.
		refs = parseCatalog(doc, finalURL, id)
	}
	if len(refs) == 0 {
		return source.File{}, fmt.Errorf("%w: no chapters found for %q", source.ErrBadResponse, info.Title)
	}
	if s.maxChapters > 0 && len(refs) > s.maxChapters {
		return source.File{}, fmt.Errorf("%w: %q has %d chapters, limit is %d", source.ErrBadResponse, info.Title, len(refs), s.maxChapters)
	}

	chapters, err := s.fetchChapters(ctx, refs, progress)
	if err != nil {
		return source.File{}, err
	}

	book := epubwrite.Book{
		Title:       info.Title,
		Author:      info.Author,
		Language:    "zh",
		Description: info.Description,
		Source:      "wenku8",
	}
	if info.CoverURL != "" {
		// A cover is a nicety: an image that cannot be fetched must not fail
		// the book, and its URL is not enough for epubwrite to embed.
		book.Cover, book.CoverMediaType = s.fetchCover(ctx, info.CoverURL)
	}
	if err := ctx.Err(); err != nil {
		return source.File{}, err
	}

	items := make([]epubwrite.Chapter, len(chapters))
	for i, ch := range chapters {
		items[i] = epubwrite.Chapter{Title: ch.title, HTML: ch.html}
	}
	data, err := s.build(book, items)
	if err != nil {
		return source.File{}, fmt.Errorf("wenku8: build epub: %w", err)
	}
	if len(data) < 2 || data[0] != 'P' || data[1] != 'K' {
		return source.File{}, fmt.Errorf("%w: epub writer returned %d bytes that are not an archive", source.ErrBadResponse, len(data))
	}

	name := safeFileName(info.Title)
	if name == "" {
		name = "wenku8"
	}
	return source.File{Name: name + ".epub", Format: "epub", Data: data}, nil
}

// detailURL accepts every token Search hands out: an absolute detail URL, the
// site-relative /book/<id>.htm form, a bare id, or Extra["url"]/Extra["id"].
func (s *Source) detailURL(b source.Book) (string, string, error) {
	raw := strings.TrimSpace(b.Key)
	if raw == "" && b.Extra != nil {
		raw = strings.TrimSpace(b.Extra["url"])
	}
	id := strings.TrimSpace(b.Extra["id"])
	if raw == "" && id != "" {
		raw = "/book/" + id + ".htm"
	}
	if raw == "" {
		return "", "", fmt.Errorf("%w: wenku8 book has no key", source.ErrBadResponse)
	}
	if got, abs := detailFromHref(s.base(), raw); got != "" {
		return abs, got, nil
	}
	if isDigits(raw) {
		return source.Combine(s.base(), "/book/"+raw+".htm"), raw, nil
	}
	return "", "", fmt.Errorf("%w: unrecognized wenku8 book key %q", source.ErrBadResponse, raw)
}

// fetch GETs one page and returns the parsed document plus the final URL,
// which is the base for relative links after any redirect.
func (s *Source) fetch(ctx context.Context, rawURL string) (*html.Node, string, error) {
	reqCtx, cancel := source.Deadline(ctx, s.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("wenku8: request %s: %w", rawURL, err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")

	resp, err := s.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, "", fmt.Errorf("wenku8: get %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, "", fmt.Errorf("%w: %s", source.ErrNotFound, rawURL)
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, "", fmt.Errorf("%w: %s", source.ErrRateLimited, rawURL)
	case resp.StatusCode >= 400:
		return nil, "", fmt.Errorf("%w: %s returned %s", source.ErrBadResponse, rawURL, resp.Status)
	}
	if ct := resp.Header.Get("Content-Type"); !isHTMLContentType(ct) {
		return nil, "", fmt.Errorf("%w: %s returned content type %q", source.ErrBadResponse, rawURL, ct)
	}

	data, err := readLimit(resp.Body, s.maxBytes)
	if err != nil {
		return nil, "", fmt.Errorf("wenku8: %s: %w", rawURL, err)
	}
	reader, err := charset.NewReader(bytes.NewReader(data), resp.Header.Get("Content-Type"))
	if err != nil {
		return nil, "", fmt.Errorf("%w: %s: %v", source.ErrBadResponse, rawURL, err)
	}
	doc, err := html.Parse(reader)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %s: %v", source.ErrBadResponse, rawURL, err)
	}

	final := rawURL
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL.String()
	}
	return doc, final, nil
}

// fetchCover returns the cover bytes and media type. Every failure is soft:
// the book still downloads without its cover.
func (s *Source) fetchCover(ctx context.Context, rawURL string) ([]byte, string) {
	reqCtx, cancel := source.Deadline(ctx, s.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, ""
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, ""
	}
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if !strings.HasPrefix(mediaType, "image/") {
		return nil, ""
	}
	data, err := readLimit(resp.Body, maxCoverBytes)
	if err != nil || len(data) == 0 {
		return nil, ""
	}
	return data, mediaType
}

// chapter is one fetched chapter page, ready for epubwrite.
type chapter struct {
	title string
	html  string
}

// fetchChapters fetches refs with a bounded worker pool. The first failure
// cancels the rest and names the chapter, because a book with a silent hole is
// worse than a failed download.
func (s *Source) fetchChapters(ctx context.Context, refs []chapterRef, progress func(source.Progress)) ([]chapter, error) {
	total := len(refs)
	out := make([]chapter, total)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg       sync.WaitGroup
		errMu    sync.Mutex
		progMu   sync.Mutex
		firstErr error
		done     atomic.Int64
	)
	setErr := func(err error) {
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
			cancel()
		}
		errMu.Unlock()
	}

	if progress != nil {
		progress(source.Progress{Done: 0, Total: int64(total), Stage: "chapters"})
	}

	jobs := make(chan int)
	workers := s.concurrency
	if workers > total {
		workers = total
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				if runCtx.Err() != nil {
					return
				}
				body, err := s.fetchChapter(runCtx, refs[idx])
				if err != nil {
					setErr(fmt.Errorf("wenku8: chapter %q: %w", refs[idx].title, err))
					return
				}
				out[idx] = chapter{title: refs[idx].title, html: body}
				n := done.Add(1)
				if progress != nil {
					progMu.Lock()
					progress(source.Progress{Done: n, Total: int64(total), Stage: "chapters"})
					progMu.Unlock()
				}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for idx := range refs {
			select {
			case jobs <- idx:
			case <-runCtx.Done():
				return
			}
		}
	}()
	wg.Wait()

	errMu.Lock()
	err := firstErr
	errMu.Unlock()
	if err != nil {
		return nil, err
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	return out, nil
}

// fetchChapter fetches one chapter, retrying once. A 404 is not retried: the
// chapter is gone, and asking again only annoys the site.
func (s *Source) fetchChapter(ctx context.Context, ref chapterRef) (string, error) {
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			if err := sleepCtx(ctx, retryDelay); err != nil {
				return "", err
			}
		}
		doc, _, err := s.fetch(ctx, ref.url)
		if err != nil {
			last = err
			if ctx.Err() != nil || errors.Is(err, source.ErrNotFound) {
				break
			}
			continue
		}
		body, err := parseChapter(doc, ref.url)
		if err != nil {
			last = err
			if ctx.Err() != nil {
				break
			}
			continue
		}
		return body, nil
	}
	if last == nil {
		last = ctx.Err()
	}
	return "", last
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// readLimit reads at most max bytes and reports an over-long page as a bad
// response rather than silently truncating it into malformed HTML.
func readLimit(r io.Reader, max int64) ([]byte, error) {
	if max <= 0 {
		max = DefaultMaxBytes
	}
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, fmt.Errorf("%w: reading body: %v", source.ErrBadResponse, err)
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%w: page exceeds %d bytes", source.ErrBadResponse, max)
	}
	return data, nil
}

func isHTMLContentType(ct string) bool {
	if strings.TrimSpace(ct) == "" {
		return true
	}
	mediaType, _, err := mime.ParseMediaType(ct)
	if err != nil {
		mediaType = strings.ToLower(strings.TrimSpace(strings.Split(ct, ";")[0]))
	}
	switch mediaType {
	case "text/html", "application/xhtml+xml", "application/xml", "text/xml", "text/plain":
		return true
	}
	return false
}

// safeFileName makes a suggested EPUB name from a title: no directory parts,
// no control characters, and a sane length.
func safeFileName(title string) string {
	var b strings.Builder
	for _, r := range title {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			b.WriteRune(' ')
			continue
		case r < 0x20 || r == 0x7f:
			continue
		case strings.ContainsRune(`/\:*?"<>|`, r):
			continue
		}
		b.WriteRune(r)
	}
	name := strings.Join(strings.Fields(b.String()), " ")
	runes := []rune(name)
	if len(runes) > 120 {
		name = strings.TrimSpace(string(runes[:120]))
	}
	return name
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
