package wenku8

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/laix/quire/internal/epubwrite"
	"github.com/laix/quire/internal/source"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read testdata/%s: %v", name, err)
	}
	return data
}

func serveHTML(w http.ResponseWriter, contentType string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func TestIdentityAndSettings(t *testing.T) {
	s := New(Options{})
	if got, want := s.ID(), ID; got != want {
		t.Errorf("ID() = %q, want %q", got, want)
	}
	if got, want := s.Name(), "轻小说文库"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
	caps := s.Caps()
	if !caps.Search || !caps.Download || caps.NeedsLogin {
		t.Errorf("Caps() = %+v", caps)
	}

	settings := s.Settings()
	if len(settings) != 1 || settings[0].Key != "site" || settings[0].Secret || settings[0].Required {
		t.Fatalf("Settings() = %+v", settings)
	}
	if settings[0].Value != DefaultBaseURL {
		t.Errorf("default site = %q, want %q", settings[0].Value, DefaultBaseURL)
	}

	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	if err := s.Configure(map[string]string{"site": srv.URL + "/"}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	if got := s.Settings()[0].Value; got != srv.URL {
		t.Errorf("configured site = %q, want %q", got, srv.URL)
	}
	if err := s.Configure(map[string]string{}); err != nil {
		t.Fatalf("partial Configure: %v", err)
	}
	if got := s.Settings()[0].Value; got != srv.URL {
		t.Errorf("partial Configure changed site to %q", got)
	}
	if err := s.Configure(map[string]string{"site": "  "}); err != nil {
		t.Fatalf("clearing site: %v", err)
	}
	if got := s.Settings()[0].Value; got != DefaultBaseURL {
		t.Errorf("cleared site = %q, want default %q", got, DefaultBaseURL)
	}
	for _, bad := range []string{"ftp://example.test", "example.test", "https://"} {
		if err := s.Configure(map[string]string{"site": bad}); err == nil {
			t.Errorf("Configure(%q) succeeded", bad)
		}
	}
}

func TestSearchUTF8(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/modules/article/search.php" {
			t.Errorf("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		gotQuery = r.URL.Query()
		serveHTML(w, "text/html; charset=utf-8", readTestdata(t, "search_utf8.html"))
	}))
	defer srv.Close()

	s := New(Options{BaseURL: srv.URL, Client: srv.Client()})
	res, err := s.Search(context.Background(), source.Query{Text: "狼"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := gotQuery.Get("searchkey"); got != "狼" {
		t.Errorf("searchkey = %q", got)
	}
	if got := gotQuery.Get("searchtype"); got != "all" {
		t.Errorf("searchtype = %q", got)
	}
	if len(res.Books) != 2 {
		t.Fatalf("books = %d, want 2: %+v", len(res.Books), res.Books)
	}

	b := res.Books[0]
	if b.Source != ID {
		t.Errorf("Source = %q", b.Source)
	}
	if b.Title != "狼与香辛料" {
		t.Errorf("Title = %q", b.Title)
	}
	if b.Author != "支仓冻砂" {
		t.Errorf("Author = %q", b.Author)
	}
	if b.Key != srv.URL+"/book/1036.htm" {
		t.Errorf("Key = %q", b.Key)
	}
	if b.Extra["id"] != "1036" || b.Extra["url"] != srv.URL+"/book/1036.htm" {
		t.Errorf("Extra = %+v", b.Extra)
	}
	if b.Extra["status"] != "完结" {
		t.Errorf("status = %q", b.Extra["status"])
	}
	if b.Extra["updated"] != "2023-05-01" {
		t.Errorf("updated = %q", b.Extra["updated"])
	}
	if !strings.Contains(b.Description, "罗伦斯") {
		t.Errorf("Description = %q", b.Description)
	}
	if b.CoverURL != srv.URL+"/image/1036s.jpg" {
		t.Errorf("CoverURL = %q", b.CoverURL)
	}
	if b.Format != "epub" || b.Language != "zh" {
		t.Errorf("Format/Language = %q/%q", b.Format, b.Language)
	}
	if res.Books[1].Author != "时雨泽惠一" {
		t.Errorf("second author = %q", res.Books[1].Author)
	}
	if !res.HasMore {
		t.Error("HasMore = false, want true from the next-page link")
	}
}

func TestSearchGBK(t *testing.T) {
	body := readTestdata(t, "search_gbk.html")
	for _, tc := range []struct {
		name        string
		contentType string
	}{
		{"header charset", "text/html; charset=gbk"},
		{"meta charset", "text/html"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				serveHTML(w, tc.contentType, body)
			}))
			defer srv.Close()

			s := New(Options{BaseURL: srv.URL, Client: srv.Client()})
			res, err := s.Search(context.Background(), source.Query{Text: "狼"})
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if len(res.Books) != 1 {
				t.Fatalf("books = %d, want 1", len(res.Books))
			}
			if got := res.Books[0].Title; got != "狼与香辛料" {
				t.Errorf("Title = %q", got)
			}
			if got := res.Books[0].Author; got != "支仓冻砂" {
				t.Errorf("Author = %q", got)
			}
		})
	}
}

func TestSearchEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveHTML(w, "text/html; charset=utf-8", readTestdata(t, "search_empty.html"))
	}))
	defer srv.Close()

	s := New(Options{BaseURL: srv.URL, Client: srv.Client()})
	res, err := s.Search(context.Background(), source.Query{Text: "不存在的书"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Books) != 0 || res.HasMore {
		t.Errorf("Result = %+v, want empty", res)
	}
}

func TestSearchEmptyQueryDoesNotFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL)
		http.NotFound(w, r)
	}))
	defer srv.Close()

	s := New(Options{BaseURL: srv.URL, Client: srv.Client()})
	res, err := s.Search(context.Background(), source.Query{Text: "   "})
	if err != nil || len(res.Books) != 0 {
		t.Errorf("Search(empty) = %+v, %v", res, err)
	}
}

func TestSearchErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		wantErr error
	}{
		{"not found", http.StatusNotFound, "<html><body>gone</body></html>", source.ErrNotFound},
		{"bad html", http.StatusOK, "<html><body><h1>503 Service Unavailable</h1><p>maintenance</p></body></html>", source.ErrBadResponse},
		{"bad html with search box", http.StatusOK, `<html><body><form action="/modules/article/search.php"><input name="searchkey"></form><p>maintenance</p></body></html>`, source.ErrBadResponse},
		{"server error", http.StatusInternalServerError, "boom", source.ErrBadResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			s := New(Options{BaseURL: srv.URL, Client: srv.Client()})
			_, err := s.Search(context.Background(), source.Query{Text: "x"})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// bookServer serves the detail page, catalog, chapters and cover used by the
// download tests. paths records every request.
func bookServer(t *testing.T, chapterFailures map[string]int) (*httptest.Server, *[]string) {
	t.Helper()
	var (
		mu    sync.Mutex
		paths []string
	)
	cover := []byte("\x89PNG\r\n\x1a\nfake cover")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case "/book/1036.htm":
			serveHTML(w, "text/html; charset=utf-8", readTestdata(t, "book.html"))
		case "/book/1036/index.htm":
			serveHTML(w, "text/html; charset=utf-8", readTestdata(t, "catalog.html"))
		case "/book/1036/2001.htm":
			serveHTML(w, "text/html; charset=utf-8", readTestdata(t, "chapter1.html"))
		case "/book/1036/2002.htm":
			if n := chapterFailures[r.URL.Path]; n > 0 {
				chapterFailures[r.URL.Path] = n - 1
				http.Error(w, "temporary failure", http.StatusInternalServerError)
				return
			}
			serveHTML(w, "text/html; charset=utf-8", readTestdata(t, "chapter2.html"))
		case "/book/1036/2003.htm":
			serveHTML(w, "text/html; charset=utf-8", readTestdata(t, "chapter2.html"))
		case "/image/1036.jpg":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(cover)
		default:
			http.NotFound(w, r)
		}
	}))
	return srv, &paths
}

func TestDownloadBuildsEPUB(t *testing.T) {
	srv, paths := bookServer(t, nil)
	defer srv.Close()

	var (
		gotBook     epubwrite.Book
		gotChapters []epubwrite.Chapter
		buildCalls  int
	)
	progressMu := sync.Mutex{}
	var progress []source.Progress

	s := New(Options{
		BaseURL:     srv.URL,
		Client:      srv.Client(),
		Concurrency: 2,
		Build: func(b epubwrite.Book, chapters []epubwrite.Chapter) ([]byte, error) {
			buildCalls++
			gotBook = b
			gotChapters = append([]epubwrite.Chapter(nil), chapters...)
			return []byte("PK\x03\x04fake epub bytes"), nil
		},
	})
	f, err := s.Download(context.Background(), source.Book{
		Source: ID,
		Key:    srv.URL + "/book/1036.htm",
		Title:  "fallback title",
	}, func(p source.Progress) {
		progressMu.Lock()
		progress = append(progress, p)
		progressMu.Unlock()
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if buildCalls != 1 {
		t.Errorf("build calls = %d, want 1", buildCalls)
	}
	if f.Name != "狼与香辛料.epub" || f.Format != "epub" {
		t.Errorf("File = %q, %q", f.Name, f.Format)
	}
	if !strings.HasPrefix(string(f.Data), "PK") {
		t.Errorf("Data = %q, want an archive", f.Data)
	}

	if gotBook.Title != "狼与香辛料" {
		t.Errorf("Book.Title = %q", gotBook.Title)
	}
	if gotBook.Author != "支仓冻砂" {
		t.Errorf("Book.Author = %q", gotBook.Author)
	}
	if !strings.Contains(gotBook.Description, "罗伦斯") {
		t.Errorf("Book.Description = %q", gotBook.Description)
	}
	if len(gotBook.Cover) == 0 || gotBook.CoverMediaType != "image/png" {
		t.Errorf("Cover = %d bytes, %q", len(gotBook.Cover), gotBook.CoverMediaType)
	}
	if gotBook.Language != "zh" || gotBook.Source != "wenku8" {
		t.Errorf("Book metadata = %+v", gotBook)
	}

	wantTitles := []string{
		"第一卷 第一章 旅行商人",
		"第一卷 第二章 贤狼",
		"第二卷 第一章 新的旅程",
	}
	if len(gotChapters) != len(wantTitles) {
		t.Fatalf("chapters = %d, want %d", len(gotChapters), len(wantTitles))
	}
	for i, want := range wantTitles {
		if gotChapters[i].Title != want {
			t.Errorf("chapter %d title = %q, want %q", i, gotChapters[i].Title, want)
		}
	}

	first := gotChapters[0].HTML
	for _, want := range []string{"罗伦斯背着货物", "他自言自语", `<img src="https://img.example/illust1.jpg" alt="插图一"/>`} {
		if !strings.Contains(first, want) {
			t.Errorf("chapter HTML missing %q:\n%s", want, first)
		}
	}
	for _, unwanted := range []string{"tracking-script", "广告：买这本轻小说", "导航文本", "<script", "<iframe", "<nav"} {
		if strings.Contains(first, unwanted) {
			t.Errorf("chapter HTML contains %q:\n%s", unwanted, first)
		}
	}
	if !strings.Contains(gotChapters[1].HTML, "贤狼") {
		t.Errorf("second chapter HTML = %q", gotChapters[1].HTML)
	}

	// Relative chapter links in catalog.html must resolve against the catalog
	// URL, not against the site root or a detail-page directory.
	requested := make(map[string]bool)
	for _, p := range *paths {
		requested[p] = true
	}
	for _, want := range []string{
		"/book/1036.htm",
		"/book/1036/index.htm",
		"/book/1036/2001.htm",
		"/book/1036/2002.htm",
		"/book/1036/2003.htm",
		"/image/1036.jpg",
	} {
		if !requested[want] {
			t.Errorf("did not request %q; requested %v", want, *paths)
		}
	}

	progressMu.Lock()
	defer progressMu.Unlock()
	if len(progress) < len(wantTitles)+1 {
		t.Fatalf("progress updates = %d, want at least %d", len(progress), len(wantTitles)+1)
	}
	if progress[0].Stage != "chapters" || progress[0].Done != 0 || progress[0].Total != int64(len(wantTitles)) {
		t.Errorf("first progress = %+v", progress[0])
	}
	final := progress[len(progress)-1]
	if final.Stage != "chapters" || final.Done != int64(len(wantTitles)) || final.Total != int64(len(wantTitles)) {
		t.Errorf("final progress = %+v", final)
	}
}

func TestDownloadRetriesChapterOnceThenFails(t *testing.T) {
	// The chapter fails on both attempts: the download must fail, name the
	// chapter, and never call Build with a hole.
	srv, paths := bookServer(t, map[string]int{"/book/1036/2002.htm": 2})
	defer srv.Close()

	buildCalls := 0
	s := New(Options{
		BaseURL: srv.URL,
		Client:  srv.Client(),
		Build: func(epubwrite.Book, []epubwrite.Chapter) ([]byte, error) {
			buildCalls++
			return []byte("PK\x03\x04"), nil
		},
	})
	_, err := s.Download(context.Background(), source.Book{
		Source: ID,
		Key:    srv.URL + "/book/1036.htm",
	}, nil)
	if err == nil {
		t.Fatal("Download succeeded with a failed chapter")
	}
	if !strings.Contains(err.Error(), "第一章 新的旅程") && !strings.Contains(err.Error(), "贤狼") {
		t.Errorf("error does not name the chapter: %v", err)
	}
	if buildCalls != 0 {
		t.Errorf("Build called %d times after a chapter failure", buildCalls)
	}
	want := "/book/1036/2002.htm"
	got := 0
	for _, p := range *paths {
		if p == want {
			got++
		}
	}
	if got != 2 {
		t.Errorf("%s requested %d times, want 2 (one retry)", want, got)
	}
}

func TestDownloadChapterCanRecoverOnRetry(t *testing.T) {
	srv, _ := bookServer(t, map[string]int{"/book/1036/2002.htm": 1})
	defer srv.Close()

	s := New(Options{
		BaseURL: srv.URL,
		Client:  srv.Client(),
		Build: func(_ epubwrite.Book, chapters []epubwrite.Chapter) ([]byte, error) {
			if len(chapters) != 3 {
				return nil, fmt.Errorf("chapters = %d", len(chapters))
			}
			return []byte("PK\x03\x04"), nil
		},
	})
	if _, err := s.Download(context.Background(), source.Book{Source: ID, Key: srv.URL + "/book/1036.htm"}, nil); err != nil {
		t.Fatalf("Download: %v", err)
	}
}

func TestDownloadLimits(t *testing.T) {
	t.Run("max chapters", func(t *testing.T) {
		srv, paths := bookServer(t, nil)
		defer srv.Close()

		buildCalls := 0
		s := New(Options{
			BaseURL:     srv.URL,
			Client:      srv.Client(),
			MaxChapters: 2,
			Build: func(epubwrite.Book, []epubwrite.Chapter) ([]byte, error) {
				buildCalls++
				return []byte("PK\x03\x04"), nil
			},
		})
		_, err := s.Download(context.Background(), source.Book{Source: ID, Key: srv.URL + "/book/1036.htm"}, nil)
		if !errors.Is(err, source.ErrBadResponse) {
			t.Fatalf("err = %v, want ErrBadResponse", err)
		}
		if buildCalls != 0 {
			t.Errorf("Build called after chapter limit")
		}
		for _, p := range *paths {
			if strings.HasPrefix(p, "/book/1036/2") {
				t.Errorf("chapter fetched despite limit: %s", p)
			}
		}
	})

	t.Run("max bytes", func(t *testing.T) {
		srv, _ := bookServer(t, nil)
		defer srv.Close()

		s := New(Options{
			BaseURL:  srv.URL,
			Client:   srv.Client(),
			MaxBytes: 128,
			Build: func(epubwrite.Book, []epubwrite.Chapter) ([]byte, error) {
				t.Error("Build called after page limit")
				return []byte("PK\x03\x04"), nil
			},
		})
		_, err := s.Download(context.Background(), source.Book{Source: ID, Key: srv.URL + "/book/1036.htm"}, nil)
		if !errors.Is(err, source.ErrBadResponse) {
			t.Fatalf("err = %v, want ErrBadResponse", err)
		}
	})
}

func TestDownloadRejectsBadBookKey(t *testing.T) {
	s := New(Options{})
	for _, key := range []string{"", "http://[::1", "not-a-book"} {
		if _, err := s.Download(context.Background(), source.Book{Source: ID, Key: key}, nil); !errors.Is(err, source.ErrBadResponse) {
			t.Errorf("Download(%q) err = %v, want ErrBadResponse", key, err)
		}
	}
}

func TestSafeFileName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"狼与香辛料", "狼与香辛料"},
		{"a/b\\c:d*e?f\"g<h>i|j", "abcdefghij"},
		{"  spaced\tname  ", "spaced name"},
		{"bad\x00name", "badname"},
	} {
		if got := safeFileName(tc.in); got != tc.want {
			t.Errorf("safeFileName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDownloadRejectsNonEPUBFromBuilder(t *testing.T) {
	srv, _ := bookServer(t, nil)
	defer srv.Close()

	s := New(Options{
		BaseURL: srv.URL,
		Client:  srv.Client(),
		Build: func(epubwrite.Book, []epubwrite.Chapter) ([]byte, error) {
			return []byte("not an epub"), nil
		},
	})
	_, err := s.Download(context.Background(), source.Book{Source: ID, Key: srv.URL + "/book/1036.htm"}, nil)
	if !errors.Is(err, source.ErrBadResponse) {
		t.Fatalf("err = %v, want ErrBadResponse", err)
	}
}
