package wenku8

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// The site has two catalog URL shapes in the wild. The older one nests the
// book under a type number, and its chapter links do the same; both must be
// recognised, or a book found by search cannot be downloaded.
func TestNovelTypeCatalogLayout(t *testing.T) {
	id, detail := detailFromHref("https://www.wenku8.net/book/1785.htm", "/novel/1/1785/index.htm")
	if id != "1785" {
		t.Fatalf("index id = %q, want 1785", id)
	}
	if detail != "https://www.wenku8.net/book/1785.htm" {
		t.Fatalf("canonical detail = %q", detail)
	}

	doc, err := html.Parse(strings.NewReader(`<table>
		<tr><td class="vcss" colspan="4">第一卷</td></tr>
		<tr><td class="ccss"><a href="/novel/1/1785/12345.htm">第一章</a></td></tr>
		<tr><td class="ccss"><a href="/novel/1/1785/12346.htm">第二章</a></td></tr>
	</table>`))
	if err != nil {
		t.Fatal(err)
	}
	refs := parseCatalog(doc, "https://www.wenku8.net/novel/1/1785/index.htm", "1785")
	if len(refs) != 2 {
		t.Fatalf("chapters = %d, want 2: %+v", len(refs), refs)
	}
	if refs[0].title != "第一卷 第一章" {
		t.Errorf("title = %q", refs[0].title)
	}
	if refs[0].url != "https://www.wenku8.net/novel/1/1785/12345.htm" {
		t.Errorf("url = %q", refs[0].url)
	}
}

func TestBookPageFindsNovelIndex(t *testing.T) {
	doc, err := html.Parse(strings.NewReader(`<html><body>
		<h1>书</h1>
		<a href="/novel/2/42/index.htm">章节目录</a>
	</body></html>`))
	if err != nil {
		t.Fatal(err)
	}
	info, err := parseBook(doc, "https://www.wenku8.net/book/42.htm")
	if err != nil {
		t.Fatal(err)
	}
	if info.CatalogURL != "https://www.wenku8.net/novel/2/42/index.htm" {
		t.Errorf("catalog = %q", info.CatalogURL)
	}
}
