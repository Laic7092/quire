package library

import (
	"archive/zip"
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestScanSamples reads the committed real books: metadata must come from
// the OPF, and the cover from the archive, without opening a chapter.
func TestScanSamples(t *testing.T) {
	entries, err := Scan(filepath.Join("..", "book", "testdata", "samples"))
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]Entry, len(entries))
	for _, entry := range entries {
		byName[filepath.Base(entry.Path)] = entry
	}
	epub, ok := byName["aq-zhuan.epub"]
	if !ok {
		t.Fatalf("aq-zhuan.epub not listed: %v", byName)
	}
	if epub.Title != "阿Ｑ正傳" {
		t.Errorf("epub title %q, want the OPF dc:title", epub.Title)
	}
	if epub.Author != "Xun Lu" {
		t.Errorf("epub author %q", epub.Author)
	}
	if epub.Language != "zh" {
		t.Errorf("epub language %q", epub.Language)
	}
	if epub.Format != "epub" || epub.Size == 0 {
		t.Errorf("epub format %q size %d", epub.Format, epub.Size)
	}
	if len(epub.Cover) == 0 || !isImage(epub.Cover) {
		t.Errorf("epub cover missing or not an image (%d bytes)", len(epub.Cover))
	}

	txt, ok := byName["aq-zhuan.txt"]
	if !ok {
		t.Fatalf("aq-zhuan.txt not listed")
	}
	if txt.Title != "aq-zhuan" {
		t.Errorf("txt title %q, want the file name without extension", txt.Title)
	}
	if txt.Format != "txt" || txt.Author != "" || txt.Cover != nil {
		t.Errorf("txt entry %+v", txt)
	}
}

// TestScanMiniEPUB reads an EPUB 3 without a cover or a creator: the
// metadata still comes from the OPF, and the missing cover stays nil.
func TestScanMiniEPUB(t *testing.T) {
	entries, err := Scan(filepath.Join("..", "book", "testdata"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || filepath.Base(entries[0].Path) != "mini.epub" {
		t.Fatalf("entries %+v, want mini.epub only", entries)
	}
	entry := entries[0]
	if entry.Title != "最小的书" || entry.Language != "zh-CN" {
		t.Errorf("entry %+v", entry)
	}
	if entry.Author != "" || entry.Cover != nil {
		t.Errorf("mini.epub has no creator or cover: %+v", entry)
	}
}

// TestScanEPUBCoverImage covers the EPUB 3 properties="cover-image" path on
// a book built for the test.
func TestScanEPUBCoverImage(t *testing.T) {
	dir := t.TempDir()
	// A real PNG: the shelf only keeps covers a painter can decode, so a file
	// that merely starts like one is not enough.
	var pic bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 3))
	img.Set(0, 0, color.RGBA{R: 0x30, G: 0x80, B: 0x40, A: 0xff})
	if err := png.Encode(&pic, img); err != nil {
		t.Fatal(err)
	}
	cover := pic.Bytes()
	writeTestEPUB(t, filepath.Join(dir, "covered.epub"), `
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>封面书</dc:title>
    <dc:creator>Some One</dc:creator>
  </metadata>
  <manifest>
    <item id="cover" href="cover.png" media-type="image/png" properties="cover-image"/>
    <item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="ch1"/></spine>`, "cover.png", cover)

	entries, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries %+v", entries)
	}
	entry := entries[0]
	if entry.Title != "封面书" || entry.Author != "Some One" {
		t.Errorf("entry %+v", entry)
	}
	if string(entry.Cover) != string(cover) {
		t.Errorf("cover %d bytes, want the archive's %d", len(entry.Cover), len(cover))
	}
}

// writeTestEPUB writes a minimal EPUB 3 whose OPF holds pkgXML, plus one
// cover file when name is not empty.
func writeTestEPUB(t *testing.T, path, pkgXML, name string, data []byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	write := func(name string, data []byte) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	write("mimetype", []byte("application/epub+zip"))
	write("META-INF/container.xml", []byte(`<?xml version="1.0"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`))
	write("OEBPS/content.opf", []byte(`<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">`+pkgXML+`
</package>`))
	if name != "" {
		write("OEBPS/"+name, data)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestScanTempDir covers the shelf's own rules: newest first, text titles
// from the file name, corrupt EPUBs skipped, other files and subdirectories
// ignored.
func TestScanTempDir(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeFile(t, dir, "old.txt", "old book", now.Add(-2*time.Hour))
	writeFile(t, dir, "new.txt", "new book", now)
	writeFile(t, dir, "broken.epub", "not a zip at all", now)
	writeFile(t, dir, "notes.md", "ignored", now)
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "sub"), "nested.txt", "not on this shelf", now)

	entries, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, filepath.Base(entry.Path))
	}
	if want := "new.txt,old.txt"; strings.Join(names, ",") != want {
		t.Fatalf("entries %v, want %s", names, want)
	}
	if entries[0].Title != "new" || entries[0].Format != "txt" || entries[0].Size == 0 || entries[0].Modified.IsZero() {
		t.Errorf("first entry %+v", entries[0])
	}
}

// TestScanMissingDir reports the error instead of an empty shelf.
func TestScanMissingDir(t *testing.T) {
	if _, err := Scan(filepath.Join(t.TempDir(), "gone")); err == nil {
		t.Fatal("Scan of a missing directory succeeded")
	}
}

// TestScanZipWithoutPackage keeps a zip that is not an EPUB visible under
// its file name, so a slightly broken book is not silently lost.
func TestScanZipWithoutPackage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "half.epub")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	entries, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Title != "half" {
		t.Fatalf("entries %+v, want one entry titled from the file name", entries)
	}
}

func TestLibraryReloadAndFilter(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "阿Q.txt", "x", time.Now())
	lib := &Library{Dir: dir}
	if err := lib.Reload(); err != nil {
		t.Fatal(err)
	}
	if len(lib.Entries) != 1 {
		t.Fatalf("reload found %d entries", len(lib.Entries))
	}

	// A failed reload keeps the previous shelf.
	lib.Dir = filepath.Join(dir, "gone")
	if err := lib.Reload(); err == nil || len(lib.Entries) != 1 {
		t.Fatalf("failed reload: err %v, entries %d", err, len(lib.Entries))
	}
	lib.Dir = dir

	lib.Entries = []Entry{
		{Path: "/books/阿Q正传.epub", Title: "阿Ｑ正傳", Author: "Xun Lu", Format: "epub"},
		{Path: "/books/notes.txt", Title: "Notes", Author: "Ada Lovelace", Format: "txt"},
	}
	for _, tc := range []struct {
		query string
		want  int
	}{
		{"", 2},
		{"阿Ｑ", 1},
		{"lu", 1},
		{"LOVELACE", 1},
		{"/books/notes", 1},
		{"missing", 0},
	} {
		if got := len(lib.Filter(tc.query)); got != tc.want {
			t.Errorf("Filter(%q) matched %d, want %d", tc.query, got, tc.want)
		}
	}
	if got := lib.Filter("lu"); len(got) == 1 && got[0].Title != "阿Ｑ正傳" {
		t.Errorf("Filter author matched %+v", got[0])
	}
	var nilLib *Library
	if got := nilLib.Filter("x"); got != nil {
		t.Errorf("nil Library Filter returned %v", got)
	}
}

func TestDetailLabel(t *testing.T) {
	entry := Entry{Format: "epub", Size: 1536, Modified: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)}
	if got := detailLabel(entry); got != "EPUB · 2 KB" {
		t.Errorf("detailLabel %q", got)
	}
	entry.Author = "Xun Lu"
	entry.Size = 3 << 20
	if got := detailLabel(entry); got != "Xun Lu · EPUB · 3.0 MB" {
		t.Errorf("detailLabel with author %q", got)
	}
}

func writeFile(t *testing.T, dir, name, content string, modified time.Time) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
}

// isImage reports whether data starts like a PNG or a JPEG, the two formats a
// shelf cover is kept in.
func isImage(data []byte) bool {
	switch {
	case len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n":
		return true
	case len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff:
		return true
	}
	return false
}
