package epubwrite

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"strings"
	"testing"
)

func readEntries(t *testing.T, data []byte) ([]*zip.File, map[string][]byte) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	files := make(map[string][]byte, len(zr.File))
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		files[f.Name] = b
	}
	return zr.File, files
}

func pngCover(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 0x20, G: 0x40, B: 0x60, A: 0xff})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func jpegCover(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 0x80, G: 0x10, B: 0x20, A: 0xff})
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func wellFormed(data []byte) error {
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func TestBytesLayout(t *testing.T) {
	cover := pngCover(t)
	data, err := Bytes(Book{Title: "A Book", Author: "Someone", Language: "en", Cover: cover},
		[]Chapter{{Title: "One", HTML: "<p>first</p>"}, {Title: "", HTML: "<p>second</p>"}})
	if err != nil {
		t.Fatal(err)
	}
	entries, files := readEntries(t, data)
	if len(entries) == 0 {
		t.Fatal("archive has no entries")
	}
	if entries[0].Name != "mimetype" {
		t.Errorf("first entry = %q, want mimetype", entries[0].Name)
	}
	if entries[0].Method != zip.Store {
		t.Errorf("mimetype method = %d, want Store", entries[0].Method)
	}
	if entries[0].Flags&0x8 != 0 {
		t.Error("mimetype has a data descriptor; EPUB requires a plain stored entry")
	}
	if got := string(files["mimetype"]); got != "application/epub+zip" {
		t.Errorf("mimetype = %q", got)
	}
	for _, name := range []string{
		"META-INF/container.xml",
		"OEBPS/content.opf",
		"OEBPS/nav.xhtml",
		"OEBPS/toc.ncx",
		"OEBPS/cover.png",
		"OEBPS/chapter-1.xhtml",
		"OEBPS/chapter-2.xhtml",
	} {
		if _, ok := files[name]; !ok {
			t.Errorf("%s is missing", name)
		}
	}
	if len(entries) != 8 {
		t.Errorf("entries = %d, want 8", len(entries))
	}
	opf := string(files["OEBPS/content.opf"])
	for _, want := range []string{
		`properties="cover-image"`,
		`href="cover.png" media-type="image/png"`,
		`<meta name="cover" content="cover-image"/>`,
		`<itemref idref="chapter-1"/>`,
		`<itemref idref="chapter-2"/>`,
		`<spine toc="ncx">`,
	} {
		if !strings.Contains(opf, want) {
			t.Errorf("OPF does not contain %q", want)
		}
	}
	nav := string(files["OEBPS/nav.xhtml"])
	for _, want := range []string{`epub:type="toc"`, `href="chapter-1.xhtml"`, "One", "Chapter 2"} {
		if !strings.Contains(nav, want) {
			t.Errorf("nav does not contain %q", want)
		}
	}
	ncx := string(files["OEBPS/toc.ncx"])
	for _, want := range []string{"navPoint", `src="chapter-1.xhtml"`, "One", "Chapter 2"} {
		if !strings.Contains(ncx, want) {
			t.Errorf("NCX does not contain %q", want)
		}
	}
	if !bytes.Equal(files["OEBPS/cover.png"], cover) {
		t.Error("cover bytes were not written unchanged")
	}
}

func TestEveryDocumentIsWellFormed(t *testing.T) {
	data, err := Bytes(Book{Title: "书名 & <tags>", Author: `A "quoted" author`, Language: "zh",
		Description: "desc <>&", Source: "https://example.test/?a=1&b=2"},
		[]Chapter{{Title: "第一章 <b>", HTML: `<p>正文 &amp; <a href="javascript:alert(1)">link</a><br>next&nbsp;line</p>`}})
	if err != nil {
		t.Fatal(err)
	}
	_, files := readEntries(t, data)
	for name, content := range files {
		if !strings.HasSuffix(name, ".xml") && !strings.HasSuffix(name, ".opf") &&
			!strings.HasSuffix(name, ".xhtml") && !strings.HasSuffix(name, ".ncx") {
			continue
		}
		if err := wellFormed(content); err != nil {
			t.Errorf("%s is not well-formed XML: %v\n%s", name, err, content)
		}
	}
	if strings.Contains(string(files["OEBPS/chapter-1.xhtml"]), "javascript:") {
		t.Error("chapter kept a javascript: link")
	}
}

func TestXMLHeaderAndDoctype(t *testing.T) {
	data, err := Bytes(Book{}, []Chapter{{HTML: "<p>x</p>"}})
	if err != nil {
		t.Fatal(err)
	}
	_, files := readEntries(t, data)
	ch := string(files["OEBPS/chapter-1.xhtml"])
	if !strings.HasPrefix(ch, xmlHeader) {
		t.Errorf("chapter does not start with the XML declaration: %.60q", ch)
	}
	if !strings.Contains(ch, "<!DOCTYPE html>") {
		t.Error("chapter has no HTML doctype")
	}
	if !strings.Contains(ch, `xmlns="http://www.w3.org/1999/xhtml"`) {
		t.Error("chapter has no XHTML namespace")
	}
}

func TestCoverFormats(t *testing.T) {
	tests := []struct {
		name      string
		cover     func(*testing.T) []byte
		mediaType string
		path      string
	}{
		{"png", pngCover, "image/png", "OEBPS/cover.png"},
		{"jpeg", jpegCover, "image/jpeg", "OEBPS/cover.jpg"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cover := tt.cover(t)
			data, err := Bytes(Book{Title: "x", Cover: cover},
				[]Chapter{{Title: "c", HTML: "<p>c</p>"}})
			if err != nil {
				t.Fatal(err)
			}
			_, files := readEntries(t, data)
			if !bytes.Equal(files[tt.path], cover) {
				t.Errorf("%s is missing or changed", tt.path)
			}
			opf := string(files["OEBPS/content.opf"])
			if !strings.Contains(opf, `media-type="`+tt.mediaType+`"`) {
				t.Errorf("OPF does not declare %s:\n%s", tt.mediaType, opf)
			}
			if !strings.Contains(opf, `properties="cover-image"`) {
				t.Error("OPF does not mark the cover image")
			}
			navCover := `href="` + strings.TrimPrefix(tt.path, "OEBPS/") + `"`
			if !strings.Contains(opf, navCover) {
				t.Errorf("guide does not point at the cover: %q", navCover)
			}
		})
	}
}

func TestBadCoverIsIgnored(t *testing.T) {
	data, err := Bytes(Book{Title: "x", Cover: []byte("not an image")},
		[]Chapter{{Title: "c", HTML: "<p>c</p>"}})
	if err != nil {
		t.Fatalf("bad cover failed the book: %v", err)
	}
	_, files := readEntries(t, data)
	for name := range files {
		if strings.Contains(name, "cover") {
			t.Errorf("unexpected cover entry %q", name)
		}
	}
	if strings.Contains(string(files["OEBPS/content.opf"]), "cover-image") {
		t.Error("OPF declares a cover that was dropped")
	}
}

func TestFallbacks(t *testing.T) {
	data, err := Bytes(Book{}, []Chapter{{HTML: ""}, {HTML: "   "}})
	if err != nil {
		t.Fatal(err)
	}
	_, files := readEntries(t, data)
	opf := string(files["OEBPS/content.opf"])
	for _, want := range []string{">Untitled<", ">und<", "urn:uuid:"} {
		if !strings.Contains(opf, want) {
			t.Errorf("OPF does not contain %q:\n%s", want, opf)
		}
	}
	nav := string(files["OEBPS/nav.xhtml"])
	if !strings.Contains(nav, ">Chapter 1<") || !strings.Contains(nav, ">Chapter 2<") {
		t.Errorf("nav does not use Chapter N titles:\n%s", nav)
	}
	for _, name := range []string{"OEBPS/chapter-1.xhtml", "OEBPS/chapter-2.xhtml"} {
		ch := string(files[name])
		if !strings.Contains(ch, "<body><p></p></body>") {
			t.Errorf("%s has no legal empty body:\n%s", name, ch)
		}
		if err := wellFormed(files[name]); err != nil {
			t.Errorf("%s is not well-formed: %v", name, err)
		}
	}
}

func TestIdentifierIsStable(t *testing.T) {
	book := Book{Title: "Same", Author: "Author", Source: "site"}
	a, err := Bytes(book, []Chapter{{HTML: "<p>x</p>"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Bytes(book, []Chapter{{HTML: "<p>x</p>"}})
	if err != nil {
		t.Fatal(err)
	}
	_, af := readEntries(t, a)
	_, bf := readEntries(t, b)
	if !bytes.Equal(af["OEBPS/content.opf"], bf["OEBPS/content.opf"]) {
		t.Error("identical books produced different package documents")
	}
	id := generatedIdentifier(book)
	if !strings.HasPrefix(id, "urn:uuid:") || len(id) != len("urn:uuid:")+36 {
		t.Errorf("identifier = %q", id)
	}
	if generatedIdentifier(Book{Title: "Other"}) == generatedIdentifier(book) {
		t.Error("different books share an identifier")
	}
}

func TestNoChapters(t *testing.T) {
	if _, err := Bytes(Book{Title: "x"}, nil); err == nil {
		t.Fatal("empty book accepted")
	} else if !strings.Contains(err.Error(), "no chapters") {
		t.Fatalf("err = %v", err)
	}
}

func TestWriteReturnsByteCount(t *testing.T) {
	var buf bytes.Buffer
	n, err := Write(&buf, Book{Title: "x"}, []Chapter{{HTML: "<p>x</p>"}})
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(buf.Len()) {
		t.Errorf("Write reported %d bytes, buffer has %d", n, buf.Len())
	}
	if n <= 0 {
		t.Errorf("Write reported %d bytes", n)
	}
}

func TestEntryNamesCannotEscape(t *testing.T) {
	data, err := Bytes(Book{Title: "../../etc/passwd"}, []Chapter{{Title: "../../x", HTML: `<p><a href="../../etc/passwd">x</a></p>`}})
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := readEntries(t, data)
	for _, f := range entries {
		if strings.Contains(f.Name, "..") || strings.HasPrefix(f.Name, "/") {
			t.Errorf("archive entry escapes the container: %q", f.Name)
		}
	}
}

func ExampleBytes() {
	data, err := Bytes(Book{Title: "Example"}, []Chapter{{Title: "One", HTML: "<p>Hello</p>"}})
	if err != nil {
		panic(err)
	}
	fmt.Println(bytes.HasPrefix(data, []byte("PK")))
	// Output: true
}
