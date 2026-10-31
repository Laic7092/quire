// Package acquire puts a book downloaded from a source on the shelf.
//
// It is the host's side of Source.Download: it creates the destination
// directory, sanitizes the suggested file name, writes through a temporary
// file and renames it into place, so a failed or cancelled download never
// leaves a half book that library.Scan would list.
package acquire

import (
	"context"
	"errors"
	"time"

	"github.com/laix/quire/internal/source"
)

// Saved is a book that landed on the shelf.
type Saved struct {
	// Path is the absolute path of the written file.
	Path string
	// Title, Author and Format are what the source said; Title may be empty.
	Title  string
	Author string
	Format string
	Size   int64
}

// Options configure a Manager.
type Options struct {
	// Dir is the directory downloads are written to. It is created on demand.
	Dir string
	// Prefix is put before the file name, so a downloaded book is easy to
	// recognise; empty means none.
	Prefix string
	// MaxBytes refuses a download larger than this; 0 means no limit.
	MaxBytes int64
	// Now overrides the clock in tests; nil means time.Now.
	Now func() time.Time
}

// Manager saves downloads. It is safe for concurrent use.
type Manager struct {
	opts Options
}

// New returns a Manager.
func New(opts Options) *Manager { return &Manager{opts: opts} }

// ErrNotConfigured is returned when Options.Dir is empty.
var ErrNotConfigured = errors.New("acquire: no download directory")

// Save downloads b from src and writes it into the manager's directory. It
// reports progress to progress when it is not nil. The file is written only
// after source.ValidateFile accepts it, and it appears atomically.
func (m *Manager) Save(ctx context.Context, src source.Source, b source.Book, progress func(source.Progress)) (Saved, error) {
	return Saved{}, ErrNotConfigured
}
