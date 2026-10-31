// Package epubwrite turns chapters fetched from a book source into one EPUB
// file that the local reader can open.
//
// It is deliberately the only place that knows the container format: sources
// produce titles and chapter HTML, never zip entries. Write normalizes the
// chapter HTML into well-formed XHTML, adds the navigation document, and lays
// the archive out the way epub.Open expects (an uncompressed "mimetype" entry
// first, container.xml, then the package document).
//
// The contract for sources:
//   - Chapter.HTML is an HTML fragment, not a whole document. Unclosed tags,
//     entities, <br> and remote <img> are tolerated; media is dropped and its
//     alt text kept, because a source's illustrations would otherwise have to
//     be downloaded and packaged too.
//   - Chapter.Title may be empty; Write invents "Chapter N".
//   - Book.Title may be empty; Write falls back to "Untitled".
//
// Write is pure: it does not touch the network or the disk.
package epubwrite

import (
	"archive/zip"
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"strings"
	"time"
)

// Book is the metadata of the EPUB to write.
type Book struct {
	Title       string
	Author      string
	Language    string
	Description string
	// Cover is a PNG or JPEG image, or nil.
	Cover []byte
	// CoverMediaType is "image/png" or "image/jpeg"; inferred from the bytes
	// when empty.
	CoverMediaType string
	// Identifier is the dc:identifier; Write makes one up when empty.
	Identifier string
	// Source is a short credit such as "Z-Library" or a URL, written as
	// dc:source when not empty.
	Source string
}

// Chapter is one spine item.
type Chapter struct {
	Title string
	HTML  string
}

// errNoChapters reports an empty book, which would be an EPUB with nothing to
// read.
var errNoChapters = errors.New("epubwrite: no chapters")

// zipEpoch is the timestamp given to every entry, so two runs produce the same
// archive layout and tests can compare entries without a moving clock.
var zipEpoch = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

// Write writes an EPUB to w and returns how many bytes it wrote.
//
// The returned archive is deterministic for the same input except for the
// modification times zip requires; sources must not depend on byte equality.
func Write(w io.Writer, b Book, chapters []Chapter) (int64, error) {
	cw := &countWriter{w: w}
	err := build(cw, b, chapters)
	return cw.n, err
}

// Bytes is Write into memory, for sources that hand the host a source.File.
func Bytes(b Book, chapters []Chapter) ([]byte, error) {
	var buf bytes.Buffer
	if _, err := Write(&buf, b, chapters); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// countWriter counts what the zip writer actually put on the wire, including
// headers and the central directory.
type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// build writes the archive. All paths are constants or derived from chapter
// numbers, so nothing from a source can escape the archive.
func build(w io.Writer, b Book, chapters []Chapter) error {
	if len(chapters) == 0 {
		return errNoChapters
	}

	title := displayTitle(b.Title, "Untitled")
	lang := strings.TrimSpace(b.Language)
	if lang == "" {
		lang = "und"
	}
	identifier := strings.TrimSpace(b.Identifier)
	if identifier == "" {
		identifier = generatedIdentifier(b)
	}
	cover := detectCover(b.Cover)

	zw := zip.NewWriter(w)

	// The mimetype entry must come first and must be stored, not deflated.
	// CreateRaw lets us write it without a trailing data descriptor, which
	// the EPUB specification requires.
	mimetype := []byte("application/epub+zip")
	if err := writeStored(zw, "mimetype", mimetype); err != nil {
		return err
	}

	container := []byte(xmlHeader +
		`<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">` +
		`<rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>` +
		`</container>` + "\n")
	if err := writeDeflated(zw, "META-INF/container.xml", container); err != nil {
		return err
	}

	if err := writeDeflated(zw, "OEBPS/content.opf", opfDocument(b, chapters, identifier, title, lang, cover)); err != nil {
		return err
	}
	if err := writeDeflated(zw, "OEBPS/nav.xhtml", navDocument(chapters, title, lang)); err != nil {
		return err
	}
	// EPUB 3 readers use nav.xhtml, but an EPUB 2 reader (and layx/epub's
	// fallback) looks for the NCX the spine names.
	if err := writeDeflated(zw, "OEBPS/toc.ncx", ncxDocument(chapters, identifier, title)); err != nil {
		return err
	}

	if cover.ok() {
		if err := writeDeflated(zw, "OEBPS/"+cover.name, b.Cover); err != nil {
			return err
		}
	}

	for i, ch := range chapters {
		body, err := sanitizeFragment(ch.HTML)
		if err != nil {
			return fmt.Errorf("epubwrite: chapter %d: %w", i+1, err)
		}
		doc := chapterDocument(chapterTitle(ch.Title, i+1), lang, body)
		if err := writeDeflated(zw, fmt.Sprintf("OEBPS/chapter-%d.xhtml", i+1), doc); err != nil {
			return err
		}
	}

	return zw.Close()
}

// writeStored writes an uncompressed entry whose size and CRC are known up
// front, so archive/zip emits neither a data descriptor nor a ZIP64 header.
func writeStored(zw *zip.Writer, name string, data []byte) error {
	fh := &zip.FileHeader{Name: name, Method: zip.Store}
	fh.SetModTime(zipEpoch)
	fh.Flags = 0
	fh.CRC32 = crc32.ChecksumIEEE(data)
	fh.CompressedSize64 = uint64(len(data))
	fh.UncompressedSize64 = uint64(len(data))
	fh.ReaderVersion = 20
	fh.CreatorVersion = 20
	w, err := zw.CreateRaw(fh)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// writeDeflated writes one compressed entry with a fixed timestamp.
func writeDeflated(zw *zip.Writer, name string, data []byte) error {
	fh := &zip.FileHeader{Name: name, Method: zip.Deflate}
	fh.SetModTime(zipEpoch)
	w, err := zw.CreateHeader(fh)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// generatedIdentifier derives a stable URN from the book's identifying
// metadata, so writing the same book twice yields the same identifier.
func generatedIdentifier(b Book) string {
	sum := sha1.Sum([]byte("quire-epubwrite\x00" + b.Title + "\x00" + b.Author + "\x00" + b.Source))
	sum[6] = sum[6]&0x0f | 0x50 // UUID version 5
	sum[8] = sum[8]&0x3f | 0x80 // RFC 4122 variant
	h := hex.EncodeToString(sum[:16])
	return "urn:uuid:" + h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// coverImage is a recognized cover and where it goes in the archive.
type coverImage struct {
	name      string
	mediaType string
	path      string
}

func (c coverImage) ok() bool { return c.name != "" }

// detectCover recognizes PNG and JPEG by signature. Other bytes are ignored:
// a source with a broken cover should still produce a readable book.
func detectCover(data []byte) coverImage {
	switch {
	case len(data) >= 8 && bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n")):
		return coverImage{name: "cover.png", mediaType: "image/png", path: "cover.png"}
	case len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff:
		return coverImage{name: "cover.jpg", mediaType: "image/jpeg", path: "cover.jpg"}
	}
	return coverImage{}
}

// displayTitle trims a title and substitutes fallback when it is empty.
func displayTitle(s, fallback string) string {
	if t := strings.TrimSpace(s); t != "" {
		return t
	}
	return fallback
}

// chapterTitle returns a reader-visible title, inventing "Chapter N" when the
// source did not give one.
func chapterTitle(s string, n int) string {
	if t := strings.TrimSpace(s); t != "" {
		return t
	}
	return fmt.Sprintf("Chapter %d", n)
}
