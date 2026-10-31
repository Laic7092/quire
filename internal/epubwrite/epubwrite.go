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
	"errors"
	"io"
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

// ErrUnavailable means this build of the package has no writer yet. It exists
// only while the package is a contract; the implementation replaces it.
var ErrUnavailable = errors.New("epubwrite: writer not implemented")

// Write writes an EPUB to w and returns how many bytes it wrote.
//
// The returned archive is deterministic for the same input except for the
// modification times zip requires; sources must not depend on byte equality.
func Write(w io.Writer, b Book, chapters []Chapter) (int64, error) {
	return 0, ErrUnavailable
}

// Bytes is Write into memory, for sources that hand the host a source.File.
func Bytes(b Book, chapters []Chapter) ([]byte, error) {
	return nil, ErrUnavailable
}
