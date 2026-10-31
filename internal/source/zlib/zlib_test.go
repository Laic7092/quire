package zlib

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/laix/quire/internal/source"
)

// testSite is an offline Z-Library-shaped site: a login form with a hidden
// token, cookie-protected search and detail pages, and three result layouts.
type testSite struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	logins int
}

func newTestSite(t *testing.T) *testSite {
	t.Helper()
	site := &testSite{t: t}
	site.srv = httptest.NewServer(http.HandlerFunc(site.serve))
	t.Cleanup(site.srv.Close)
	return site
}

func (s *testSite) loginCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logins
}

func (s *testSite) serve(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/login" && r.Method == http.MethodGet:
		s.loginPage(w)
	case r.URL.Path == "/login" && r.Method == http.MethodPost:
		s.login(w, r)
	case r.URL.Path == "/":
		fmt.Fprint(w, `<html><body><h1>Home</h1></body></html>`)
	case strings.HasPrefix(r.URL.Path, "/s/"):
		s.search(w, r)
	case strings.HasPrefix(r.URL.Path, "/dl/"), strings.HasPrefix(r.URL.Path, "/book/3/files/"):
		s.download(w, r)
	case strings.HasPrefix(r.URL.Path, "/book/"):
		s.detail(w, r)
	case strings.HasPrefix(r.URL.Path, "/covers/"):
		w.Header().Set("Content-Type", "image/jpeg")
		fmt.Fprint(w, "cover")
	default:
		http.NotFound(w, r)
	}
}

func (s *testSite) loginPage(w http.ResponseWriter) {
	fmt.Fprint(w, `<html><body>
<form method="post" action="/login">
<input type="hidden" name="csrf" value="tok">
<input type="email" name="email">
<input type="password" name="password">
<button type="submit">Log in</button>
</form></body></html>`)
}

func (s *testSite) login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if r.PostFormValue("csrf") != "tok" || r.PostFormValue("email") == "" || r.PostFormValue("password") == "" {
		s.loginPage(w)
		return
	}
	s.mu.Lock()
	s.logins++
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "sid", Value: "ok", Path: "/"})
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *testSite) authed(r *http.Request) bool {
	c, err := r.Cookie("sid")
	return err == nil && c.Value == "ok"
}

func (s *testSite) search(w http.ResponseWriter, r *http.Request) {
	if !s.authed(r) {
		http.Error(w, "login required", http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/s/alpha":
		fmt.Fprint(w, `<html><body><div class="book-list">
<div class="book-item">
  <img data-src="/covers/a.jpg" alt="">
  <h3 class="book-title"><a href="/book/1/alpha">Alpha</a></h3>
  <div class="authors"><a href="/author/ann">Ann Author</a></div>
  <div class="property__file">English · EPUB · 1.5 MB</div>
</div>
</div></body></html>`)
	case "/s/components":
		fmt.Fprintf(w, `<html><body>
<z-bookcard id="2" href="/book/2/beta" title="Beta" author="Bob Writer"
 cover="/covers/b.png" extension="TXT" filesize="12 KB" year="2020"></z-bookcard>
</body></html>`)
	case "/s/table":
		fmt.Fprint(w, `<html><body><table class="res-table">
<tr><th>Title</th><th>Author</th><th>File</th></tr>
<tr><td><a href="/book/7/gamma">Gamma</a></td><td>Carol</td><td>English · PDF · 2 MB</td></tr>
</table></body></html>`)
	case "/s/empty":
		fmt.Fprint(w, `<html><body><div id="searchResultBox" class="book-list">
<div class="no-result">No books found</div></div></body></html>`)
	case "/s/bad":
		fmt.Fprint(w, `<html><body><h1>Maintenance</h1><p>We are offline.</p></body></html>`)
	case "/s/denied":
		http.Error(w, "expired session", http.StatusUnauthorized)
	case "/s/limited":
		http.Error(w, "slow down", http.StatusTooManyRequests)
	default:
		http.NotFound(w, r)
	}
}

func (s *testSite) detail(w http.ResponseWriter, r *http.Request) {
	if !s.authed(r) {
		http.Error(w, "login required", http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/book/1/alpha":
		fmt.Fprint(w, `<html><body>
<h1>Alpha</h1><div class="book-authors">Ann Author</div>
<a class="dlButton" href="/dl/1.epub">EPUB</a>
<a class="dlButton" href="/dl/1.txt">TXT</a>
</body></html>`)
	case "/book/2/beta":
		fmt.Fprint(w, `<html><body>
<h1>Beta</h1><div class="book-authors">Bob Writer</div>
<a href="/dl/2.txt">TXT</a>
</body></html>`)
	case "/book/3/rel":
		fmt.Fprint(w, `<html><body><h1>Relative</h1>
<a class="download" href="files/3.epub">EPUB</a></body></html>`)
	case "/book/4/abs":
		fmt.Fprintf(w, `<html><body><h1>Absolute</h1>
<a class="download" href="%s/dl/4.epub">EPUB</a></body></html>`, s.srv.URL)
	case "/book/5/none":
		fmt.Fprint(w, `<html><body><h1>No file</h1><p>Nothing to download.</p></body></html>`)
	case "/book/6/bad":
		fmt.Fprint(w, `<html><body><p>Service unavailable</p></body></html>`)
	default:
		http.NotFound(w, r)
	}
}

func (s *testSite) download(w http.ResponseWriter, r *http.Request) {
	if !s.authed(r) {
		http.Error(w, "login required", http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/dl/1.epub":
		w.Header().Set("Content-Type", "application/epub+zip")
		w.Header().Set("Content-Disposition", `attachment; filename="alpha.epub"`)
		fmt.Fprint(w, "PK\x03\x04epub-bytes")
	case "/dl/1.txt", "/dl/2.txt":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, "plain text book")
	case "/book/3/files/3.epub":
		w.Header().Set("Content-Type", "application/epub+zip")
		fmt.Fprint(w, "PK\x03\x04relative-epub")
	case "/dl/4.epub":
		w.Header().Set("Content-Type", "application/epub+zip")
		fmt.Fprint(w, "PK\x03\x04absolute-epub")
	default:
		http.NotFound(w, r)
	}
}

func newSource(site *testSite) *Source {
	return New(Options{
		Site:        site.srv.URL,
		Email:       "reader@example.test",
		Password:    "secret",
		Client:      site.srv.Client(),
		MinInterval: time.Millisecond,
		MaxBytes:    1 << 20,
		Timeout:     2 * time.Second,
	})
}

func search(t *testing.T, s *Source, text string) source.Result {
	t.Helper()
	res, err := s.Search(context.Background(), source.Query{Text: text})
	if err != nil {
		t.Fatalf("Search(%q): %v", text, err)
	}
	return res
}

func TestSearchLogsInParsesCardsAndKeepsSession(t *testing.T) {
	site := newTestSite(t)
	src := newSource(site)
	res := search(t, src, "alpha")
	if len(res.Books) != 1 {
		t.Fatalf("books = %d, want 1", len(res.Books))
	}
	book := res.Books[0]
	if book.Source != ID || book.Title != "Alpha" || book.Author != "Ann Author" {
		t.Errorf("book = %+v", book)
	}
	if book.Format != "epub" {
		t.Errorf("format = %q, want epub", book.Format)
	}
	if book.Size != int64(1.5*(1<<20)) {
		t.Errorf("size = %d", book.Size)
	}
	if book.CoverURL != site.srv.URL+"/covers/a.jpg" {
		t.Errorf("cover = %q", book.CoverURL)
	}
	if book.Key != site.srv.URL+"/book/1/alpha" {
		t.Errorf("key = %q", book.Key)
	}
	if book.Extra["detail"] != site.srv.URL+"/book/1/alpha" {
		t.Errorf("extra = %+v", book.Extra)
	}
	if site.loginCount() != 1 {
		t.Fatalf("logins = %d, want 1", site.loginCount())
	}
	// A second search must reuse the cookie from the first login.
	_ = search(t, src, "alpha")
	if site.loginCount() != 1 {
		t.Errorf("logins after second search = %d, want 1", site.loginCount())
	}
}

func TestSearchCardShapes(t *testing.T) {
	site := newTestSite(t)
	for _, tc := range []struct {
		query string
		check func(*testing.T, source.Book)
	}{
		{"components", func(t *testing.T, b source.Book) {
			if b.Title != "Beta" || b.Author != "Bob Writer" || b.Format != "txt" {
				t.Errorf("component book = %+v", b)
			}
			if b.Size != 12*(1<<10) || b.Year != "2020" {
				t.Errorf("component size/year = %d/%q", b.Size, b.Year)
			}
			if b.CoverURL != site.srv.URL+"/covers/b.png" {
				t.Errorf("component cover = %q", b.CoverURL)
			}
		}},
		{"table", func(t *testing.T, b source.Book) {
			if b.Title != "Gamma" || b.Author != "Carol" {
				t.Errorf("table book = %+v", b)
			}
			if b.Format != "pdf" || b.Size != 2*(1<<20) {
				t.Errorf("table format/size = %q/%d", b.Format, b.Size)
			}
		}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			res := search(t, newSource(site), tc.query)
			if len(res.Books) != 1 {
				t.Fatalf("books = %d, want 1", len(res.Books))
			}
			tc.check(t, res.Books[0])
		})
	}
}

func TestSearchErrors(t *testing.T) {
	site := newTestSite(t)
	for _, tc := range []struct {
		query string
		want  error
	}{
		{"denied", source.ErrAuthRequired},
		{"limited", source.ErrRateLimited},
		{"missing", source.ErrNotFound},
		{"bad", source.ErrBadResponse},
	} {
		t.Run(tc.query, func(t *testing.T) {
			_, err := newSource(site).Search(context.Background(), source.Query{Text: tc.query})
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestSearchEmptyResultIsNotAnError(t *testing.T) {
	site := newTestSite(t)
	res := search(t, newSource(site), "empty")
	if len(res.Books) != 0 || res.HasMore {
		t.Errorf("result = %+v", res)
	}
}

func TestDownloadEPUBAndTXT(t *testing.T) {
	site := newTestSite(t)
	src := newSource(site)

	var stages []string
	var last source.Progress
	file, err := src.Download(context.Background(), source.Book{
		Source: ID,
		Key:    site.srv.URL + "/book/1/alpha",
		Title:  "Alpha",
	}, func(p source.Progress) {
		stages = append(stages, p.Stage)
		last = p
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if file.Format != "epub" || !strings.HasPrefix(string(file.Data), "PK") {
		t.Errorf("file = %q %q", file.Format, file.Data)
	}
	if file.Name != "alpha.epub" {
		t.Errorf("name = %q, want alpha.epub", file.Name)
	}
	for _, stage := range []string{"login", "detail", "download"} {
		if !contains(stages, stage) {
			t.Errorf("stages = %v, missing %q", stages, stage)
		}
	}
	if last.Done == 0 || last.Done != last.Total {
		t.Errorf("last progress = %+v", last)
	}

	txt, err := src.Download(context.Background(), source.Book{
		Source: ID,
		Key:    site.srv.URL + "/book/2/beta",
		Title:  "Beta",
	}, nil)
	if err != nil {
		t.Fatalf("Download fallback: %v", err)
	}
	if txt.Format != "txt" || !strings.Contains(string(txt.Data), "plain text") {
		t.Errorf("fallback = %q %q", txt.Format, txt.Data)
	}
	if !strings.HasSuffix(txt.Name, ".txt") {
		t.Errorf("fallback name = %q", txt.Name)
	}
}

func TestDownloadRelativeAndAbsoluteLinks(t *testing.T) {
	site := newTestSite(t)
	src := newSource(site)
	for _, tc := range []struct {
		key  string
		want string
	}{
		{"rel", site.srv.URL + "/book/3/rel"},
		{"abs", site.srv.URL + "/book/4/abs"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			file, err := src.Download(context.Background(), source.Book{Source: ID, Key: tc.want, Title: tc.key}, nil)
			if err != nil {
				t.Fatalf("Download: %v", err)
			}
			if file.Format != "epub" || !strings.HasPrefix(string(file.Data), "PK") {
				t.Errorf("file = %q %q", file.Format, file.Data)
			}
		})
	}
}

func TestDownloadErrors(t *testing.T) {
	site := newTestSite(t)
	src := newSource(site)
	if _, err := src.Download(context.Background(), source.Book{Source: ID, Key: site.srv.URL + "/book/5/none"}, nil); !errors.Is(err, source.ErrUnsupported) {
		t.Errorf("no file error = %v, want ErrUnsupported", err)
	}
	if _, err := src.Download(context.Background(), source.Book{Source: ID, Key: site.srv.URL + "/book/6/bad"}, nil); !errors.Is(err, source.ErrBadResponse) {
		t.Errorf("bad detail error = %v, want ErrBadResponse", err)
	}
}

func TestSettingsAndConfigure(t *testing.T) {
	siteA := newTestSite(t)
	src := newSource(siteA)

	settings := src.Settings()
	if len(settings) != 3 {
		t.Fatalf("settings = %d, want 3", len(settings))
	}
	byKey := make(map[string]source.Setting)
	for _, setting := range settings {
		byKey[setting.Key] = setting
	}
	if byKey["site"].Value != siteA.srv.URL || !byKey["site"].Required {
		t.Errorf("site setting = %+v", byKey["site"])
	}
	if byKey["email"].Value != "reader@example.test" || !byKey["email"].Required {
		t.Errorf("email setting = %+v", byKey["email"])
	}
	if !byKey["password"].Secret || byKey["password"].Value != "" {
		t.Errorf("password setting = %+v", byKey["password"])
	}

	_ = search(t, src, "alpha")
	if siteA.loginCount() != 1 {
		t.Fatalf("logins A = %d", siteA.loginCount())
	}

	// Partial update: only the email changes, site and password stay.
	if err := src.Configure(map[string]string{"email": "second@example.test"}); err != nil {
		t.Fatal(err)
	}
	if got := src.Settings(); got[0].Value != siteA.srv.URL || got[1].Value != "second@example.test" {
		t.Errorf("settings after partial update = %+v", got)
	}
	_ = search(t, src, "alpha")
	if siteA.loginCount() != 2 {
		t.Errorf("logins after email change = %d, want 2", siteA.loginCount())
	}

	// Changing the site must forget the old session and log in to the new one.
	siteB := newTestSite(t)
	if err := src.Configure(map[string]string{"site": siteB.srv.URL}); err != nil {
		t.Fatal(err)
	}
	_ = search(t, src, "components")
	if siteB.loginCount() != 1 {
		t.Errorf("logins B = %d, want 1", siteB.loginCount())
	}
	_ = search(t, src, "components")
	if siteB.loginCount() != 1 {
		t.Errorf("logins B after second search = %d, want 1", siteB.loginCount())
	}

	// Required values are refused when explicitly emptied.
	if err := src.Configure(map[string]string{"site": " "}); err == nil {
		t.Error("empty site accepted")
	}
	if err := src.Configure(map[string]string{"email": ""}); err == nil {
		t.Error("empty email accepted")
	}
	if got := src.Settings()[0].Value; got != siteB.srv.URL {
		t.Errorf("site after rejected update = %q", got)
	}
}

func TestMissingCredentialsReturnsAuthRequired(t *testing.T) {
	site := newTestSite(t)
	src := New(Options{Site: site.srv.URL, MinInterval: time.Millisecond, Timeout: time.Second})
	_, err := src.Search(context.Background(), source.Query{Text: "alpha"})
	if !errors.Is(err, source.ErrAuthRequired) {
		t.Fatalf("error = %v, want ErrAuthRequired", err)
	}
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
