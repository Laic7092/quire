package library

import (
	"errors"
	"fmt"

	"github.com/laix/layx/epub"
)

// errNotArchive reports a file that cannot be opened as a zip. Scan skips
// such a file; a zip whose package document is missing or broken is listed
// under its file name instead, as a book that may still open later.
var errNotArchive = errors.New("library: not a readable archive")

// epubMeta is what a shelf needs from an EPUB without opening the book.
type epubMeta struct {
	Title    string
	Creator  string
	Language string
	Cover    []byte
}

// readEPUBMeta reads the package document and the cover image, and nothing
// else: spine items are never opened, so this costs a few kilobytes rather
// than the whole book. The parsing belongs to github.com/laix/layx/epub, which
// keeps a shelf's needs next to the reader's.
func readEPUBMeta(file string) (epubMeta, error) {
	m, err := epub.ReadMetadata(file)
	if err != nil {
		if errors.Is(err, epub.ErrNotArchive) {
			return epubMeta{}, fmt.Errorf("%w: %v", errNotArchive, err)
		}
		return epubMeta{}, err
	}
	return epubMeta{
		Title:    m.Title,
		Creator:  m.Creator,
		Language: m.Language,
		Cover:    m.Cover,
	}, nil
}
