package book

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeZip writes a zip archive with the given entries, the shape a broken
// EPUB takes in the tests below.
func writeZip(t *testing.T, name string, entries map[string]string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), name)
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for entry, body := range entries {
		w, err := zw.Create(entry)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return file
}

const testContainerXML = `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
	<rootfiles>
		<rootfile full-path="content.opf" media-type="application/oebps-package+xml"/>
	</rootfiles>
</container>`

// testOPF is a one-chapter package document whose spine is given, so a test can
// build a book with an empty one.
func testOPF(spine string) string {
	return `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0">
	<metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
		<dc:title>测试书</dc:title>
		<dc:language>zh</dc:language>
	</metadata>
	<manifest>
		<item id="c1" href="c1.xhtml" media-type="application/xhtml+xml"/>
	</manifest>
	<spine>` + spine + `</spine>
</package>`
}

// A file that is not a book must come back as an error that says why, never as
// a book with no chapters and never as a panic.
func TestOpenBrokenInput(t *testing.T) {
	cases := []struct {
		name    string
		file    string
		wantSub string
	}{
		{
			name:    "missing",
			file:    filepath.Join(t.TempDir(), "gone.txt"),
			wantSub: "no such file",
		},
		{
			name:    "directory",
			file:    t.TempDir(),
			wantSub: "directory",
		},
		{
			name:    "empty",
			file:    writeTmp(t, "empty.txt", nil),
			wantSub: "no readable text",
		},
		{
			name:    "not-a-zip",
			file:    writeTmp(t, "broken.epub", []byte("this is not an archive")),
			wantSub: "zip",
		},
		{
			name:    "empty-zip",
			file:    writeZip(t, "empty.epub", nil),
			wantSub: "container.xml",
		},
		{
			name: "no-package",
			file: writeZip(t, "nopkg.epub", map[string]string{
				"META-INF/container.xml": testContainerXML,
			}),
			wantSub: "content.opf",
		},
		{
			name: "empty-spine",
			file: writeZip(t, "nospine.epub", map[string]string{
				"META-INF/container.xml": testContainerXML,
				"content.opf":            testOPF(""),
			}),
			wantSub: "no readable chapters",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, err := Open(c.file)
			if err == nil {
				t.Fatalf("opened %s: %d chapters, no error", c.file, len(b.Chapters))
			}
			if b != nil {
				t.Errorf("a book came back with the error")
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Errorf("error %q does not mention %q", err, c.wantSub)
			}
		})
	}
}

// A book with no cover is a book, not an error.
func TestOpenBookWithoutCover(t *testing.T) {
	file := writeZip(t, "nocover.epub", map[string]string{
		"META-INF/container.xml": testContainerXML,
		"content.opf":            testOPF(`<itemref idref="c1"/>`),
		"c1.xhtml":               "<html><body><p>只有正文。</p></body></html>",
	})
	b, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	if b.HasCover {
		t.Error("a book without a cover claims one")
	}
	if len(b.Chapters) != 1 {
		t.Fatalf("chapters %d, want 1", len(b.Chapters))
	}
	if _, _, err := b.Build(18); err != nil {
		t.Fatal(err)
	}
}

// With an unknown extension the content decides: a zip is an EPUB, anything
// else is a text book. The extension itself is not case sensitive.
func TestOpenDispatch(t *testing.T) {
	text := writeTmp(t, "no-extension", []byte("第一章 开始\n\n正文。\n"))
	b, err := Open(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Chapters) != 1 {
		t.Fatalf("text book chapters %d, want 1", len(b.Chapters))
	}

	epubFile := writeZip(t, "no-extension-zip", map[string]string{
		"META-INF/container.xml": testContainerXML,
		"content.opf":            testOPF(`<itemref idref="c1"/>`),
		"c1.xhtml":               "<html><body><p>正文。</p></body></html>",
	})
	b, err = Open(epubFile)
	if err != nil {
		t.Fatal(err)
	}
	if b.Title != "测试书" {
		t.Errorf("title %q, want the package document's", b.Title)
	}

	upper := writeZip(t, "UPPER.EPUB", map[string]string{
		"META-INF/container.xml": testContainerXML,
		"content.opf":            testOPF(`<itemref idref="c1"/>`),
		"c1.xhtml":               "<html><body><p>正文。</p></body></html>",
	})
	if _, err := Open(upper); err != nil {
		t.Errorf("an upper-case .EPUB extension was not recognized: %v", err)
	}
}
