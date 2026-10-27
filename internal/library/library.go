// Package library describes the book files a shelf lists and shows them in a
// side panel.
//
// The list of books is the caller's: the reader keeps it in the store and asks
// this package to describe each file, while Scan reads a whole directory for
// the one action that imports books in bulk. Both deliberately avoid opening a
// book: an EPUB's metadata and cover come straight from the zip's
// container.xml, OPF package document and cover image, and the chapter XHTML is
// never read. A shelf of a few hundred books therefore costs a few kilobytes
// per EPUB and no parsing of a chapter.
//
// Scan reads dir itself and does not descend into subdirectories: an imported
// directory is one directory of books, not a tree.
package library

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Entry is one book on the shelf.
type Entry struct {
	Path string
	// Title is the OPF dc:title of an EPUB, or the file name without its
	// extension for a text book.
	Title string
	// Author is the OPF dc:creator, possibly empty.
	Author string
	// Language is the OPF dc:language, possibly empty.
	Language string
	// Format is "epub" or "txt", from the file extension.
	Format string
	Size   int64
	// Modified is the file's modification time; Scan sorts by it, newest
	// first.
	Modified time.Time
	// Cover is the encoded bytes of an EPUB cover (PNG or JPEG), nil when
	// the book has none or it is larger than a few megabytes. Text books
	// have no cover.
	Cover []byte
}

// Scan lists the EPUB and TXT books directly inside dir, newest first.
//
// A file that cannot be read as a book (a truncated EPUB, a zip without a
// package document) is skipped rather than failing the whole scan. A file
// whose zip opens but whose metadata does not read is listed under its file
// name, so a slightly broken book stays visible.
func Scan(dir string) ([]Entry, error) {
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("library: scan %s: %w", dir, err)
	}
	entries := make([]Entry, 0, len(items))
	for _, item := range items {
		if item.IsDir() {
			continue
		}
		if entry, ok := Meta(filepath.Join(dir, item.Name())); ok {
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if !entries[i].Modified.Equal(entries[j].Modified) {
			return entries[i].Modified.After(entries[j].Modified)
		}
		return entries[i].Path < entries[j].Path
	})
	return entries, nil
}

// Meta describes one book file: its size and modification time from the file
// itself, its title and author from an EPUB's package document. It reports
// false when path is not a book the reader can use — gone, not an EPUB or TXT,
// or an archive that does not open — so a caller listing remembered books can
// skip it rather than show a row that cannot open.
func Meta(path string) (Entry, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return Entry{}, false
	}
	format := formatOf(path)
	if format == "" {
		return Entry{}, false
	}
	entry := Entry{
		Path:     path,
		Title:    strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
		Format:   format,
		Size:     info.Size(),
		Modified: info.ModTime(),
	}
	if entry.Title == "" {
		// A file named ".epub" has an extension but no name: listing it would put
		// a blank row on the shelf, and there is nothing to call the book.
		return Entry{}, false
	}
	if format == "epub" {
		meta, err := readEPUBMeta(path)
		if errors.Is(err, errNotArchive) {
			return Entry{}, false // a truncated download is not a book
		}
		if title := strings.TrimSpace(meta.Title); title != "" {
			entry.Title = title
		}
		entry.Author = strings.TrimSpace(meta.Creator)
		entry.Language = strings.TrimSpace(meta.Language)
		entry.Cover = meta.Cover
	}
	return entry, true
}

// formatOf is "epub" or "txt" from the extension, "" for anything else.
func formatOf(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".epub":
		return "epub"
	case ".txt":
		return "txt"
	}
	return ""
}

// Library is a shelf's state, held by the caller: the entries it lists. Where
// they come from — the store's remembered books, or one import — is the
// caller's business; the panel only draws them.
type Library struct {
	Entries []Entry
}

// Filter returns the entries whose title, author or path contains query,
// ignoring case; an empty query returns them all. Matching is a plain
// substring search, with no folding of accents or width forms.
func (l *Library) Filter(query string) []Entry {
	if l == nil {
		return nil
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return l.Entries
	}
	matched := make([]Entry, 0, len(l.Entries))
	for _, entry := range l.Entries {
		if strings.Contains(strings.ToLower(entry.Title), query) ||
			strings.Contains(strings.ToLower(entry.Author), query) ||
			strings.Contains(strings.ToLower(entry.Path), query) {
			matched = append(matched, entry)
		}
	}
	return matched
}
