package legado

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/laix/quire/internal/epubwrite"
	"github.com/laix/quire/internal/source"
	"golang.org/x/net/html"
	"golang.org/x/text/encoding/simplifiedchinese"
)

func mustParseHTML(t *testing.T, s string) *html.Node {
	t.Helper()
	doc, err := parseHTML([]byte(s))
	if err != nil {
		t.Fatalf("parseHTML: %v", err)
	}
	return doc
}

func TestImport(t *testing.T) {
	base := `{
		"bookSourceName": "A",
		"bookSourceUrl": "https://a.com",
		"searchUrl": "/s?q={{key}}",
		"ruleSearch": {"bookList": ".item", "name": "h3@text"}
	}`
	t.Run("array", func(t *testing.T) {
		s := New(Options{})
		n, err := s.Import([]byte("[" + base + "]"))
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		if n != 1 {
			t.Fatalf("expected 1, got %d", n)
		}
	})
	t.Run("single object", func(t *testing.T) {
		s := New(Options{})
		n, err := s.Import([]byte(base))
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		if n != 1 {
			t.Fatalf("expected 1, got %d", n)
		}
	})
	t.Run("invalid json", func(t *testing.T) {
		s := New(Options{})
		_, err := s.Import([]byte("not json"))
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("duplicate url", func(t *testing.T) {
		s := New(Options{})
		n, err := s.Import([]byte("[" + base + "," + base + "]"))
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		if n != 1 {
			t.Fatalf("expected 1 after dedup, got %d", n)
		}
	})
	t.Run("missing fields", func(t *testing.T) {
		s := New(Options{})
		data := `[{"bookSourceName":"X","bookSourceUrl":"https://x.com","searchUrl":"/s","ruleSearch":{}}]`
		n, err := s.Import([]byte(data))
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		if n != 0 {
			t.Fatalf("expected 0, got %d", n)
		}
	})
	t.Run("enabled false", func(t *testing.T) {
		s := New(Options{})
		data := `[{"bookSourceName":"A","bookSourceUrl":"https://a.com","searchUrl":"/s","enabled":false,"ruleSearch":{"bookList":".x","name":"h3@text"}}]`
		n, err := s.Import([]byte(data))
		if err != nil {
			t.Fatalf("Import: %v", err)
		}
		if n != 0 {
			t.Fatalf("expected 0, got %d", n)
		}
	})
}

func TestCSSRules(t *testing.T) {
	doc := mustParseHTML(t, `
		<html><body>
		<div class="book-item">
			<a href="/book/1">First Book</a>
			<span class="author">Alice</span>
			<img src="/cover/1.jpg">
			<div class="intro"><p>Hello <b>world</b></p></div>
		</div>
		<div class="book-item">
			<a href="/book/2">Second Book</a>
			<span data-role="author">Bob</span>
		</div>
		</body></html>
	`)

	t.Run("class", func(t *testing.T) {
		v, err := evalField(doc, "https://x.com", "class.book-item@tag.a@text")
		if err != nil {
			t.Fatal(err)
		}
		if v != "First Book" {
			t.Fatalf("got %q", v)
		}
	})
	t.Run("href", func(t *testing.T) {
		v, err := evalField(doc, "https://x.com", "class.book-item@tag.a@href")
		if err != nil {
			t.Fatal(err)
		}
		if v != "https://x.com/book/1" {
			t.Fatalf("got %q", v)
		}
	})
	t.Run("css prefix", func(t *testing.T) {
		v, err := evalField(doc, "https://x.com", "@css:.book-item a@text")
		if err != nil {
			t.Fatal(err)
		}
		if v != "First Book" {
			t.Fatalf("got %q", v)
		}
	})
	t.Run("attr", func(t *testing.T) {
		v, err := evalField(doc, "https://x.com", "@css:span[data-role=author]@attr(data-role)")
		if err != nil {
			t.Fatal(err)
		}
		if v != "author" {
			t.Fatalf("got %q", v)
		}
	})
	t.Run("html", func(t *testing.T) {
		v, err := evalField(doc, "https://x.com", "class.intro@html")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(v, "<p>") {
			t.Fatalf("expected inner HTML, got %q", v)
		}
	})
	t.Run("group", func(t *testing.T) {
		all, err := selectList(doc, "https://x.com", "class.book-item a")
		if err != nil {
			t.Fatal(err)
		}
		if len(all) != 2 {
			t.Fatalf("expected 2, got %d", len(all))
		}
	})
}

func TestJSONPath(t *testing.T) {
	data := map[string]interface{}{
		"data": map[string]interface{}{
			"title": "Root",
			"books": []interface{}{
				map[string]interface{}{"title": "One", "idx": 0},
				map[string]interface{}{"title": "Two", "idx": 1},
			},
		},
	}

	t.Run("array expansion", func(t *testing.T) {
		all, err := selectList(data, "", "@json:$.data.books[*]")
		if err != nil {
			t.Fatal(err)
		}
		if len(all) != 2 {
			t.Fatalf("expected 2, got %d", len(all))
		}
	})
	t.Run("single value", func(t *testing.T) {
		v, err := evalField(data, "", "@json:$.data.title")
		if err != nil {
			t.Fatal(err)
		}
		if v != "Root" {
			t.Fatalf("got %q", v)
		}
	})
	t.Run("subscript", func(t *testing.T) {
		v, err := evalField(data, "", "@json:$.data.books[1].title")
		if err != nil {
			t.Fatal(err)
		}
		if v != "Two" {
			t.Fatalf("got %q", v)
		}
	})
}

func TestRegex(t *testing.T) {
	t.Run("replace", func(t *testing.T) {
		v, err := evalField("Hello 123", "", "##\\d+##X")
		if err != nil {
			t.Fatal(err)
		}
		if v != "Hello X" {
			t.Fatalf("got %q", v)
		}
	})
	t.Run("capture", func(t *testing.T) {
		v, err := evalField("file-12.txt", "", "##.*-(\\d+)\\.txt##Item-$1")
		if err != nil {
			t.Fatal(err)
		}
		if v != "Item-12" {
			t.Fatalf("got %q", v)
		}
	})
}

func TestSearchURLTemplate(t *testing.T) {
	t.Run("get", func(t *testing.T) {
		u, _, err := buildSearchURL("https://x.com", "/search?q={{key}}&page={{page}}", "hello world", 2)
		if err != nil {
			t.Fatal(err)
		}
		want := "https://x.com/search?q=hello+world&page=2"
		if u != want {
			t.Fatalf("got %q, want %q", u, want)
		}
	})
	t.Run("post options", func(t *testing.T) {
		u, opts, err := buildSearchURL("https://x.com", `/search,{"method":"POST","body":"kw={{key}}"}`, "中文", 1)
		if err != nil {
			t.Fatal(err)
		}
		if u != "https://x.com/search" {
			t.Fatalf("got %q", u)
		}
		if opts.Method != "POST" || opts.Body != "kw=%E4%B8%AD%E6%96%87" {
			t.Fatalf("opts: %+v", opts)
		}
	})
}

func TestUnsupportedJS(t *testing.T) {
	_, err := evalField(nil, "", "@js:java.api(...)")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, source.ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
}

func TestEmptyResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `<html><body><div class="item"></div></body></html>`)
	}))
	defer srv.Close()

	s := New(Options{})
	data := fmt.Sprintf(`[{
		"bookSourceName": "E",
		"bookSourceUrl": %q,
		"searchUrl": "/search?q={{key}}",
		"ruleSearch": {"bookList": ".item", "name": "a@text"}
	}]`, srv.URL)
	if _, err := s.Import([]byte(data)); err != nil {
		t.Fatal(err)
	}
	res, err := s.Search(context.Background(), source.Query{Text: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Books) != 0 {
		t.Fatalf("expected 0 books, got %d", len(res.Books))
	}
}

func Test404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	s := New(Options{})
	data := fmt.Sprintf(`[{
		"bookSourceName": "E",
		"bookSourceUrl": %q,
		"searchUrl": "/search?q={{key}}",
		"ruleSearch": {"bookList": ".item", "name": "a@text"}
	}]`, srv.URL)
	if _, err := s.Import([]byte(data)); err != nil {
		t.Fatal(err)
	}
	_, err := s.Search(context.Background(), source.Query{Text: "x"})
	if !errors.Is(err, source.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func Test429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	s := New(Options{})
	data := fmt.Sprintf(`[{
		"bookSourceName": "E",
		"bookSourceUrl": %q,
		"searchUrl": "/search?q={{key}}",
		"ruleSearch": {"bookList": ".item", "name": "a@text"}
	}]`, srv.URL)
	if _, err := s.Import([]byte(data)); err != nil {
		t.Fatal(err)
	}
	_, err := s.Search(context.Background(), source.Query{Text: "x"})
	if !errors.Is(err, source.ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited, got %v", err)
	}
}

func TestBadHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `<html><body>`) // truncated
	}))
	defer srv.Close()

	s := New(Options{})
	data := fmt.Sprintf(`[{
		"bookSourceName": "E",
		"bookSourceUrl": %q,
		"searchUrl": "/search?q={{key}}",
		"ruleSearch": {"bookList": ".item", "name": "a@text"}
	}]`, srv.URL)
	if _, err := s.Import([]byte(data)); err != nil {
		t.Fatal(err)
	}
	res, err := s.Search(context.Background(), source.Query{Text: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Books) != 0 {
		t.Fatalf("expected 0 books, got %d", len(res.Books))
	}
}

func TestGBKSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=gbk")
		utf8 := `<html><body><div class="item"><a href="/b">测试书名</a></div></body></html>`
		encoded, _ := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(utf8))
		w.Write(encoded)
	}))
	defer srv.Close()

	s := New(Options{})
	data := fmt.Sprintf(`[{
		"bookSourceName": "G",
		"bookSourceUrl": %q,
		"searchUrl": "/search?q={{key}}",
		"ruleSearch": {"bookList": ".item", "name": "a@text", "bookUrl": "a@href"}
	}]`, srv.URL)
	if _, err := s.Import([]byte(data)); err != nil {
		t.Fatal(err)
	}
	res, err := s.Search(context.Background(), source.Query{Text: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Books) != 1 {
		t.Fatalf("expected 1 book, got %d", len(res.Books))
	}
	if res.Books[0].Title != "测试书名" {
		t.Fatalf("got %q", res.Books[0].Title)
	}
}

func TestOneDefinitionFailsOtherSucceeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "bad") {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, `<html><body><div class="item"><a href="/b">OK Book</a></div></body></html>`)
	}))
	defer srv.Close()

	s := New(Options{})
	data := fmt.Sprintf(`[
		{"bookSourceName": "Bad", "bookSourceUrl": %q, "searchUrl": "/bad?q={{key}}", "ruleSearch": {"bookList": ".item", "name": "a@text"}},
		{"bookSourceName": "Good", "bookSourceUrl": %q, "searchUrl": "/good?q={{key}}", "ruleSearch": {"bookList": ".item", "name": "a@text", "bookUrl": "a@href"}}
	]`, srv.URL, srv.URL+"/v2")
	if _, err := s.Import([]byte(data)); err != nil {
		t.Fatal(err)
	}
	res, err := s.Search(context.Background(), source.Query{Text: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Books) != 1 {
		t.Fatalf("expected 1 book, got %d", len(res.Books))
	}
	if res.Books[0].Title != "OK Book" {
		t.Fatalf("got %q", res.Books[0].Title)
	}
}

func TestEndToEnd(t *testing.T) {
	var gotBook epubwrite.Book
	var gotChapters []epubwrite.Chapter
	builder := func(b epubwrite.Book, cs []epubwrite.Chapter) ([]byte, error) {
		gotBook = b
		gotChapters = cs
		return []byte("PK fake epub"), nil
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/search"):
			io.WriteString(w, `<html><body>
				<div class="book-item">
					<a href="/book/1">Demo Book</a>
					<span class="author">Demo Author</span>
					<div class="intro">A demo book.</div>
				</div>
				<div class="book-item">
					<a href="/book/2">Second Book</a>
					<span class="author">Other</span>
				</div>
			</body></html>`)
		case strings.HasPrefix(r.URL.Path, "/book/"):
			io.WriteString(w, `<html><body>
				<h1 id="book-name">Demo Book</h1>
				<p id="author">Demo Author</p>
				<div id="intro"><p>Longer intro.</p></div>
				<a id="toc" href="/toc/1">TOC</a>
			</body></html>`)
		case strings.HasPrefix(r.URL.Path, "/toc/"):
			io.WriteString(w, `<html><body>
				<ul class="chapters">
					<li class="chapter"><a href="/chapter/1">Chapter 1</a></li>
					<li class="chapter"><a href="/chapter/2">Chapter 2</a></li>
				</ul>
			</body></html>`)
		case strings.HasPrefix(r.URL.Path, "/chapter/"):
			io.WriteString(w, `<html><body><div id="content"><p>Body of `+r.URL.Path+`.</p></div></body></html>`)
		}
	}))
	defer srv.Close()

	s := New(Options{Build: builder, Concurrency: 2})
	data := fmt.Sprintf(`[{
		"bookSourceName": "Demo",
		"bookSourceUrl": %q,
		"searchUrl": "/search?q={{key}}&page={{page}}",
		"ruleSearch": {
			"bookList": "class.book-item",
			"name": "tag.a@text",
			"author": "class.author@text",
			"intro": "class.intro@text",
			"bookUrl": "tag.a@href"
		},
		"ruleBookInfo": {
			"name": "id.book-name@text",
			"author": "id.author@text",
			"intro": "id.intro@html",
			"tocUrl": "id.toc@href"
		},
		"ruleToc": {
			"chapterList": "class.chapter",
			"chapterName": "tag.a@text",
			"chapterUrl": "tag.a@href"
		},
		"ruleContent": {
			"content": "id.content@html"
		}
	}]`, srv.URL)
	if _, err := s.Import([]byte(data)); err != nil {
		t.Fatal(err)
	}

	res, err := s.Search(context.Background(), source.Query{Text: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Books) != 2 {
		t.Fatalf("expected 2 books, got %d", len(res.Books))
	}
	book := res.Books[0]
	if book.Title != "Demo Book" {
		t.Fatalf("title: %q", book.Title)
	}
	if book.Author != "Demo Author" {
		t.Fatalf("author: %q", book.Author)
	}

	var progress []source.Progress
	file, err := s.Download(context.Background(), book, func(p source.Progress) {
		progress = append(progress, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	if file.Format != "epub" {
		t.Fatalf("format: %q", file.Format)
	}
	if !bytes.HasPrefix(file.Data, []byte("PK")) {
		t.Fatal("expected PK prefix")
	}
	if gotBook.Title != "Demo Book" || gotBook.Author != "Demo Author" {
		t.Fatalf("builder book: %+v", gotBook)
	}
	if len(gotChapters) != 2 {
		t.Fatalf("expected 2 chapters, got %d", len(gotChapters))
	}
	if gotChapters[0].Title != "Chapter 1" || gotChapters[1].Title != "Chapter 2" {
		t.Fatalf("chapters: %+v", gotChapters)
	}
	if !strings.Contains(gotChapters[0].HTML, "Body of /chapter/1") {
		t.Fatalf("chapter html: %q", gotChapters[0].HTML)
	}
	if len(progress) == 0 {
		t.Fatal("expected progress")
	}
	if progress[0].Stage != "chapters" {
		t.Fatalf("stage: %q", progress[0].Stage)
	}
}

func TestExport(t *testing.T) {
	s := New(Options{})
	data := `[{"bookSourceName":"A","bookSourceUrl":"https://a.com","searchUrl":"/s","ruleSearch":{"bookList":".x","name":"h3@text"}}]`
	if _, err := s.Import([]byte(data)); err != nil {
		t.Fatal(err)
	}
	out, err := s.Export()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "bookSourceName") {
		t.Fatalf("export: %s", out)
	}
}

func TestContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		io.WriteString(w, `<html><body><div class="item"><a href="/b">X</a></div></body></html>`)
	}))
	defer srv.Close()

	s := New(Options{Timeout: 500 * time.Millisecond})
	data := fmt.Sprintf(`[{
		"bookSourceName": "S",
		"bookSourceUrl": %q,
		"searchUrl": "/search?q={{key}}",
		"ruleSearch": {"bookList": ".item", "name": "a@text"}
	}]`, srv.URL)
	if _, err := s.Import([]byte(data)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := s.Search(ctx, source.Query{Text: "x"})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}
