// Package library finds the books of a directory and shows them in a side
// panel.
//
// Scanning deliberately avoids opening a book: an EPUB's metadata and cover
// come straight from the zip's container.xml, OPF package document and cover
// image, and the chapter XHTML is never read. A shelf of a few hundred books
// therefore costs a directory listing plus a few kilobytes per EPUB.
//
// Scan reads dir itself and does not descend into subdirectories: a shelf is
// one directory of books, not a tree.
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
		ext := strings.ToLower(filepath.Ext(item.Name()))
		var format string
		switch ext {
		case ".epub":
			format = "epub"
		case ".txt":
			format = "txt"
		default:
			continue
		}
		info, err := item.Info()
		if err != nil {
			continue // it vanished between ReadDir and Info
		}
		path := filepath.Join(dir, item.Name())
		entry := Entry{
			Path:     path,
			Title:    strings.TrimSuffix(item.Name(), filepath.Ext(item.Name())),
			Format:   format,
			Size:     info.Size(),
			Modified: info.ModTime(),
		}
		if format == "epub" {
			meta, err := readEPUBMeta(path)
			if errors.Is(err, errNotArchive) {
				continue // a truncated download is not a book
			}
			if title := strings.TrimSpace(meta.Title); title != "" {
				entry.Title = title
			}
			entry.Author = strings.TrimSpace(meta.Creator)
			entry.Language = strings.TrimSpace(meta.Language)
			entry.Cover = meta.Cover
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		if !entries[i].Modified.Equal(entries[j].Modified) {
			return entries[i].Modified.After(entries[j].Modified)
		}
		return entries[i].Path < entries[j].Path
	})
	return entries, nil
}

// Library is a shelf's state, held by the caller: the directory and the
// entries last scanned from it.
type Library struct {
	Dir     string
	Entries []Entry
}

// Reload scans Dir again and replaces Entries only when the scan succeeds,
// so a temporary error leaves the current shelf as it was.
func (l *Library) Reload() error {
	entries, err := Scan(l.Dir)
	if err != nil {
		return err
	}
	l.Entries = entries
	return nil
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
