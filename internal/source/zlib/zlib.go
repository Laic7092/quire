// Package zlib is the Z-Library book source.
//
// It owns everything specific to the site: the login form, the cookie jar, the
// search URL, the result cards and the download links. All of that HTML
// knowledge lives in parse.go, so a site redesign is a change to selectors
// rather than to the transport below.
//
// The source is deliberately offline-friendly to test: New accepts a client,
// a site URL and a minimum request interval, and nothing in this package knows
// a real domain or a real account.
package zlib

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/laix/quire/internal/source"
)

// ID is the stable source identifier used by the store and by Book.Source.
const ID source.ID = "zlib"

const userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/124.0 Safari/537.36 Quire/1.0"

// Options configure one source. Site is normally the only required value at
// construction time: credentials can arrive later through Configure.
type Options struct {
	// Site is the mirror root URL, for example https://example.invalid.
	Site string
	// Email and Password are the account credentials. They are kept in memory
	// and never placed in a URL.
	Email    string
	Password string
	// Client is the HTTP client to use. A nil Client gets a default client
	// with its own cookie jar.
	Client *http.Client
	// MinInterval is the minimum time between two HTTP requests. Zero means
	// the default of one second.
	MinInterval time.Duration
	// MaxBytes limits one response body. Zero means 64 MiB.
	MaxBytes int64
	// Timeout limits one request. Zero means 30 seconds.
	Timeout time.Duration
}

// Source is the Z-Library implementation of source.Source. It is safe for
// concurrent use; requests are serialized so the configured interval is a
// real limit rather than a best effort.
type Source struct {
	// mu guards configuration and the login flag. It is never held across a
	// network call, except for reading a snapshot of the client.
	mu       sync.RWMutex
	site     string
	email    string
	password string
	client   *http.Client
	gen      uint64
	loggedIn bool

	// sessionMu serializes login so concurrent searches share one login.
	sessionMu sync.Mutex

	// reqMu serializes requests and spaces them by minInterval.
	reqMu       sync.Mutex
	lastReq     time.Time
	minInterval time.Duration

	timeout  time.Duration
	maxBytes int64
}

var (
	_ source.Source       = (*Source)(nil)
	_ source.Configurable = (*Source)(nil)
)

// New builds a source. It does not touch the network.
func New(opts Options) *Source {
	s := &Source{
		site:        normalizeSite(opts.Site),
		email:       strings.TrimSpace(opts.Email),
		password:    opts.Password,
		minInterval: opts.MinInterval,
		timeout:     opts.Timeout,
		maxBytes:    opts.MaxBytes,
	}
	if s.minInterval <= 0 {
		s.minInterval = time.Second
	}
	if s.timeout <= 0 {
		s.timeout = 30 * time.Second
	}
	if s.maxBytes <= 0 {
		s.maxBytes = 64 << 20
	}
	if opts.Client == nil {
		s.client = &http.Client{Jar: newJar(), Timeout: s.timeout}
	} else {
		client := *opts.Client
		if client.Jar == nil {
			client.Jar = newJar()
		}
		s.client = &client
	}
	return s
}

// ID implements source.Source.
func (s *Source) ID() source.ID { return ID }

// Name implements source.Source.
func (s *Source) Name() string { return "Z-Library" }

// Caps implements source.Source.
func (s *Source) Caps() source.Caps {
	return source.Caps{Search: true, Download: true, NeedsLogin: true}
}

// Settings implements source.Configurable. The site and email are shown back;
// the password never is.
func (s *Source) Settings() []source.Setting {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return []source.Setting{
		{
			Key:         "site",
			Label:       "Site",
			Help:        "Root URL of the Z-Library mirror to use.",
			Placeholder: "https://example.invalid",
			Value:       s.site,
			Required:    true,
		},
		{
			Key:         "email",
			Label:       "Email",
			Help:        "Account email used to sign in.",
			Placeholder: "you@example.com",
			Value:       s.email,
			Required:    true,
		},
		{
			Key:      "password",
			Label:    "Password",
			Help:     "Account password. Stored by the host and never shown back.",
			Secret:   true,
			Required: false,
		},
	}
}

// Configure implements source.Configurable. Keys missing from values keep
// their current value. A changed site starts a fresh session; changed
// credentials force a fresh login on the next Search.
func (s *Source) Configure(values map[string]string) error {
	if values == nil {
		return nil
	}

	var (
		site    string
		hasSite bool
		email   string
		hasMail bool
		pass    string
		hasPass bool
	)
	if v, ok := values["site"]; ok {
		site = normalizeSite(v)
		if site == "" {
			return errors.New("zlib: site is required")
		}
		hasSite = true
	}
	if v, ok := values["email"]; ok {
		email = strings.TrimSpace(v)
		if email == "" {
			return errors.New("zlib: email is required")
		}
		hasMail = true
	}
	if v, ok := values["password"]; ok {
		pass = v
		hasPass = true
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	changed := false
	if hasSite && site != s.site {
		s.site = site
		// Cookies from the old host must not leak to the new one.
		client := *s.client
		client.Jar = newJar()
		s.client = &client
		s.loggedIn = false
		changed = true
	}
	if hasMail && email != s.email {
		s.email = email
		s.loggedIn = false
		changed = true
	}
	if hasPass && pass != s.password {
		s.password = pass
		s.loggedIn = false
		changed = true
	}
	if changed {
		s.gen++
	}
	return nil
}

// Search implements source.Source. An empty query returns an empty page
// without a network round trip.
func (s *Source) Search(ctx context.Context, q source.Query) (source.Result, error) {
	text := strings.TrimSpace(q.Text)
	if text == "" {
		return source.Result{}, nil
	}
	if err := s.ensureLogin(ctx); err != nil {
		return source.Result{}, err
	}

	site := s.siteURL()
	page := source.PageOf(q)
	limit := source.LimitOf(q, 20)
	rawURL := source.Combine(site, pathSearch+url.PathEscape(text))
	if page > 1 {
		rawURL += fmt.Sprintf("?page=%d", page)
	}

	resp, err := s.do(ctx, http.MethodGet, rawURL, nil, "")
	if err != nil {
		return source.Result{}, err
	}
	doc, err := parseDocument(resp.Body)
	if err != nil {
		return source.Result{}, fmt.Errorf("%w: search page: %v", source.ErrBadResponse, err)
	}
	if isLoginDocument(doc) {
		return source.Result{}, fmt.Errorf("%w: search returned the login page", source.ErrAuthRequired)
	}
	return parseSearch(site, doc, limit)
}

// Download implements source.Source. It opens the detail page, picks an EPUB
// link (or TXT when there is no EPUB), and returns the bytes with a suggested
// file name.
func (s *Source) Download(ctx context.Context, b source.Book, progress func(source.Progress)) (source.File, error) {
	report(progress, 0, -1, "login")
	if err := s.ensureLogin(ctx); err != nil {
		return source.File{}, err
	}

	site := s.siteURL()
	detailURL, err := detailURLFor(site, b)
	if err != nil {
		return source.File{}, err
	}

	report(progress, 0, -1, "detail")
	resp, err := s.do(ctx, http.MethodGet, detailURL, nil, "")
	if err != nil {
		return source.File{}, err
	}
	doc, err := parseDocument(resp.Body)
	if err != nil {
		return source.File{}, fmt.Errorf("%w: detail page: %v", source.ErrBadResponse, err)
	}
	if isLoginDocument(doc) {
		return source.File{}, fmt.Errorf("%w: detail returned the login page", source.ErrAuthRequired)
	}

	detail, err := parseDetail(site, detailURL, doc)
	if err != nil {
		return source.File{}, err
	}
	if len(detail.Downloads) == 0 {
		return source.File{}, fmt.Errorf("%w: no EPUB or TXT download entry", source.ErrUnsupported)
	}

	title := firstNonEmpty(b.Title, detail.Title, "book")
	var lastErr error
	for _, link := range detail.Downloads {
		report(progress, 0, -1, "download")
		resp, err := s.do(ctx, http.MethodGet, link.URL, nil, detailURL)
		if err != nil {
			// A dead EPUB link may still leave a working TXT fallback.
			if errors.Is(err, source.ErrNotFound) {
				lastErr = err
				continue
			}
			return source.File{}, err
		}
		file, err := fileFromResponse(title, link, resp)
		if err != nil {
			if errors.Is(err, source.ErrBadResponse) {
				lastErr = err
				continue
			}
			return source.File{}, err
		}
		report(progress, int64(len(file.Data)), int64(len(file.Data)), "download")
		return file, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("%w: no usable download link", source.ErrUnsupported)
	}
	return source.File{}, lastErr
}

func (s *Source) siteURL() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.site
}

// ensureLogin logs in once. The site and credentials are captured together so
// a concurrent Configure cannot produce a request with a mixed configuration.
func (s *Source) ensureLogin(ctx context.Context) error {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()

	s.mu.RLock()
	if s.loggedIn {
		s.mu.RUnlock()
		return nil
	}
	site, email, password, gen := s.site, s.email, s.password, s.gen
	s.mu.RUnlock()

	if site == "" {
		return fmt.Errorf("%w: zlib site is not configured", source.ErrAuthRequired)
	}
	if email == "" || password == "" {
		return fmt.Errorf("%w: zlib email and password are required", source.ErrAuthRequired)
	}
	if err := s.login(ctx, site, email, password); err != nil {
		return err
	}

	s.mu.Lock()
	if s.gen == gen && s.site == site && s.email == email && s.password == password {
		s.loggedIn = true
	}
	s.mu.Unlock()
	return nil
}

// login fetches the login form, submits every hidden field plus the
// credentials, and treats a returned password form as a rejected login.
func (s *Source) login(ctx context.Context, site, email, password string) error {
	loginURL := source.Combine(site, pathLogin)
	resp, err := s.do(ctx, http.MethodGet, loginURL, nil, "")
	if err != nil {
		return err
	}
	doc, err := parseDocument(resp.Body)
	if err != nil {
		return fmt.Errorf("%w: login page: %v", source.ErrBadResponse, err)
	}
	form := findLoginForm(doc)
	if form == nil {
		return fmt.Errorf("%w: login page has no password form", source.ErrBadResponse)
	}

	action := source.Combine(loginURL, firstAttr(form, "action", "data-action"))
	if action == "" {
		action = loginURL
	}
	values := loginValues(form, email, password)
	resp, err = s.do(ctx, http.MethodPost, action, values, loginURL)
	if err != nil {
		return err
	}
	if isLoginBody(resp.Body) {
		return fmt.Errorf("%w: zlib rejected the email or password", source.ErrAuthRequired)
	}
	return nil
}

// do performs one request under the rate limit and per-request timeout. The
// body is read here so the timeout covers the whole response, not just the
// headers.
func (s *Source) do(ctx context.Context, method, rawURL string, form url.Values, referer string) (*httpResponse, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, fmt.Errorf("zlib: request %s: %w", rawURL, err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.8")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if referer != "" {
		req.Header.Set("Referer", referer)
	}

	s.reqMu.Lock()
	defer s.reqMu.Unlock()
	if err := s.wait(ctx); err != nil {
		return nil, err
	}
	s.lastReq = time.Now()

	s.mu.RLock()
	client := s.client
	timeout := s.timeout
	maxBytes := s.maxBytes
	s.mu.RUnlock()

	reqCtx, cancel := context.WithTimeout(req.Context(), timeout)
	defer cancel()
	req = req.WithContext(reqCtx)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("zlib: %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("zlib: reading %s: %w", rawURL, err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%w: %s exceeded %d bytes", source.ErrBadResponse, rawURL, maxBytes)
	}
	if resp.StatusCode >= 400 {
		return nil, statusError(rawURL, resp.StatusCode)
	}
	return &httpResponse{
		Status:   resp.StatusCode,
		Header:   resp.Header,
		Body:     data,
		FinalURL: resp.Request.URL.String(),
	}, nil
}

func (s *Source) wait(ctx context.Context) error {
	if s.lastReq.IsZero() {
		return nil
	}
	d := s.minInterval - time.Since(s.lastReq)
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type httpResponse struct {
	Status   int
	Header   http.Header
	Body     []byte
	FinalURL string
}

func statusError(rawURL string, status int) error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w: %s returned %s", source.ErrAuthRequired, rawURL, http.StatusText(status))
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", source.ErrNotFound, rawURL)
	case http.StatusTooManyRequests:
		return fmt.Errorf("%w: %s", source.ErrRateLimited, rawURL)
	case http.StatusBadRequest:
		return fmt.Errorf("%w: %s returned 400", source.ErrBadResponse, rawURL)
	default:
		return fmt.Errorf("zlib: %s returned %s", rawURL, http.StatusText(status))
	}
}

// detailURLFor turns what Search put in Key/Extra back into a detail URL.
func detailURLFor(site string, b source.Book) (string, error) {
	if detail := strings.TrimSpace(b.Extra["detail"]); detail != "" {
		if u := source.Combine(site, detail); u != "" {
			return u, nil
		}
	}
	key := strings.TrimSpace(b.Key)
	if key == "" {
		return "", fmt.Errorf("%w: zlib book has no key", source.ErrBadResponse)
	}
	switch {
	case strings.HasPrefix(key, "http://"), strings.HasPrefix(key, "https://"), strings.HasPrefix(key, "//"):
		return source.Combine(site, key), nil
	case strings.HasPrefix(key, "/"):
		return source.Combine(site, key), nil
	case strings.HasPrefix(key, "book/"):
		return source.Combine(site, "/"+key), nil
	default:
		return source.Combine(site, pathBook+url.PathEscape(key)), nil
	}
}

// fileFromResponse validates the downloaded bytes and gives them a name.
func fileFromResponse(title string, link downloadLink, resp *httpResponse) (source.File, error) {
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	format := link.Format
	if bytes.HasPrefix(resp.Body, []byte("PK")) {
		format = "epub"
	}
	if format == "" {
		format = sniffFormat(resp.Body, contentType)
	}
	if len(resp.Body) == 0 {
		return source.File{}, fmt.Errorf("%w: empty download", source.ErrBadResponse)
	}
	if looksHTML(resp.Body, contentType) {
		if isLoginBody(resp.Body) {
			return source.File{}, fmt.Errorf("%w: download returned the login page", source.ErrAuthRequired)
		}
		return source.File{}, fmt.Errorf("%w: download returned HTML", source.ErrBadResponse)
	}
	switch format {
	case "epub":
		if !bytes.HasPrefix(resp.Body, []byte("PK")) {
			return source.File{}, fmt.Errorf("%w: download is not an EPUB archive", source.ErrBadResponse)
		}
	case "txt":
		// Any non-HTML bytes are acceptable as text; the host validates the
		// extension and the reader decides how to display them.
	default:
		return source.File{}, fmt.Errorf("%w: download is not EPUB or TXT", source.ErrBadResponse)
	}
	return source.File{
		Name:   downloadName(title, format, resp.Header.Get("Content-Disposition")),
		Format: format,
		Data:   resp.Body,
	}, nil
}

func sniffFormat(data []byte, contentType string) string {
	switch {
	case bytes.HasPrefix(data, []byte("PK")):
		return "epub"
	case strings.Contains(contentType, "epub"):
		return "epub"
	case strings.Contains(contentType, "text/plain"), strings.Contains(contentType, "text/"):
		return "txt"
	case looksHTML(data, contentType):
		return ""
	default:
		return "txt"
	}
}

func looksHTML(data []byte, contentType string) bool {
	if strings.Contains(contentType, "text/html") || strings.Contains(contentType, "xhtml") {
		return true
	}
	head := bytes.TrimSpace(data)
	if len(head) > 256 {
		head = head[:256]
	}
	head = bytes.ToLower(head)
	return bytes.HasPrefix(head, []byte("<!doctype html")) || bytes.HasPrefix(head, []byte("<html"))
}

func downloadName(title, format, disposition string) string {
	name := dispositionName(disposition)
	if name == "" {
		name = title
	}
	name = safeFileName(name)
	if !strings.HasSuffix(strings.ToLower(name), "."+format) {
		name += "." + format
	}
	return name
}

func dispositionName(v string) string {
	if v == "" {
		return ""
	}
	if _, params, err := mime.ParseMediaType(v); err == nil {
		if name := params["filename"]; name != "" {
			return name
		}
		if name := params["filename*"]; name != "" {
			if at := strings.Index(name, "''"); at >= 0 {
				name = name[at+2:]
			}
			if decoded, err := url.PathUnescape(name); err == nil {
				name = decoded
			}
			return name
		}
	}
	for _, part := range strings.Split(v, ";") {
		part = strings.TrimSpace(part)
		lower := strings.ToLower(part)
		if !strings.HasPrefix(lower, "filename=") && !strings.HasPrefix(lower, "filename*=") {
			continue
		}
		name := strings.TrimSpace(part[strings.IndexByte(part, '=')+1:])
		name = strings.TrimPrefix(name, "UTF-8''")
		name = strings.TrimPrefix(name, "utf-8''")
		name = strings.Trim(name, `"`)
		if decoded, err := url.PathUnescape(name); err == nil {
			name = decoded
		}
		return name
	}
	return ""
}

func safeFileName(name string) string {
	name = strings.TrimSpace(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '/' || r == '\\' || r < 0x20 || r == 0x7f:
			b.WriteRune('-')
		default:
			b.WriteRune(r)
		}
	}
	name = strings.TrimSpace(b.String())
	if name == "" || name == "." || name == ".." {
		return "book"
	}
	if runes := []rune(name); len(runes) > 150 {
		name = string(runes[:150])
	}
	return name
}

func report(progress func(source.Progress), done, total int64, stage string) {
	if progress != nil {
		progress(source.Progress{Done: done, Total: total, Stage: stage})
	}
}

func newJar() http.CookieJar {
	jar, _ := cookiejar.New(nil)
	return jar
}

func normalizeSite(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), "/")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
