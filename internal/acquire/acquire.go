// Package acquire puts a book downloaded from a source on the shelf.
//
// It is the host's side of Source.Download: it creates the destination
// directory, sanitizes the suggested file name, writes through a temporary
// file and renames it into place, so a failed or cancelled download never
// leaves a half book that library.Scan would list.
package acquire

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

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
	// mu serializes name selection and rename within one manager. Without it
	// two downloads could both see a free name and the second rename would
	// overwrite the first book.
	mu sync.Mutex
}

// New returns a Manager.
func New(opts Options) *Manager { return &Manager{opts: opts} }

// ErrNotConfigured is returned when Options.Dir is empty.
var ErrNotConfigured = errors.New("acquire: no download directory")

// maxNameBytes keeps a sanitized file name (extension included) within the
// limits of common filesystems and of a readable shelf row. Truncation is by
// rune, never in the middle of a UTF-8 sequence.
const maxNameBytes = 120

// Save downloads b from src and writes it into the manager's directory. It
// reports progress to progress when it is not nil. The file is written only
// after source.ValidateFile accepts it, and it appears atomically.
func (m *Manager) Save(ctx context.Context, src source.Source, b source.Book, progress func(source.Progress)) (Saved, error) {
	if strings.TrimSpace(m.opts.Dir) == "" {
		return Saved{}, ErrNotConfigured
	}
	if src == nil {
		return Saved{}, fmt.Errorf("acquire: nil source")
	}
	if err := ctx.Err(); err != nil {
		return Saved{}, err
	}

	file, err := src.Download(ctx, b, progress)
	if err != nil {
		return Saved{}, fmt.Errorf("acquire: download %q: %w", b.Title, err)
	}
	// A source may return the book without suggesting a name; the search
	// result's title is the documented fallback. Fill it in before validation,
	// which requires a name.
	if strings.TrimSpace(file.Name) == "" {
		file.Name = b.Title
	}
	if err := source.ValidateFile(file); err != nil {
		return Saved{}, err
	}
	if m.opts.MaxBytes > 0 && int64(len(file.Data)) > m.opts.MaxBytes {
		return Saved{}, fmt.Errorf("%w: download is %d bytes, limit is %d",
			source.ErrBadResponse, len(file.Data), m.opts.MaxBytes)
	}
	// Download may have ignored the cancellation, or the caller may have
	// cancelled while the bytes were arriving; do not put a partial result on
	// the shelf either way.
	if err := ctx.Err(); err != nil {
		return Saved{}, err
	}

	dir, err := filepath.Abs(m.opts.Dir)
	if err != nil {
		return Saved{}, fmt.Errorf("acquire: resolve %q: %w", m.opts.Dir, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Saved{}, fmt.Errorf("acquire: create %q: %w", dir, err)
	}

	format := source.FormatOf(file.Format)
	name := m.fileName(file, b, format)

	// Write beside the destination, then rename. CreateTemp's 0600 mode is
	// tightened to 0644 before the file becomes visible.
	tmp, err := os.CreateTemp(dir, ".quire-*.part")
	if err != nil {
		return Saved{}, fmt.Errorf("acquire: create temporary file: %w", err)
	}
	tmpPath := tmp.Name()
	keep := false
	defer func() {
		if !keep {
			tmp.Close()
			os.Remove(tmpPath)
		}
	}()

	if _, err := io.Copy(tmp, bytes.NewReader(file.Data)); err != nil {
		return Saved{}, fmt.Errorf("acquire: write %q: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		return Saved{}, fmt.Errorf("acquire: sync %q: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return Saved{}, fmt.Errorf("acquire: close %q: %w", tmpPath, err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		return Saved{}, fmt.Errorf("acquire: chmod %q: %w", tmpPath, err)
	}
	if err := ctx.Err(); err != nil {
		return Saved{}, err
	}

	dst, err := m.place(tmpPath, dir, name)
	if err != nil {
		return Saved{}, err
	}
	keep = true

	return Saved{
		Path:   dst,
		Title:  b.Title,
		Author: b.Author,
		Format: format,
		Size:   int64(len(file.Data)),
	}, nil
}

// place chooses a free name and renames the temporary file to it. The name is
// rechecked immediately before each rename, and the whole loop holds the
// manager lock, so two concurrent saves cannot pick the same slot.
func (m *Manager) place(tmpPath, dir, name string) (string, error) {
	stem, ext := splitExt(name)

	m.mu.Lock()
	defer m.mu.Unlock()

	for n := 1; n <= 10000; n++ {
		candidate := filepath.Join(dir, numberedName(stem, ext, n))
		_, err := os.Lstat(candidate)
		switch {
		case err == nil:
			continue // taken; try the next suffix
		case !errors.Is(err, fs.ErrNotExist):
			return "", fmt.Errorf("acquire: inspect %q: %w", candidate, err)
		}
		if err := os.Rename(tmpPath, candidate); err != nil {
			// Another writer outside this manager may have won the race;
			// pick the next free name instead of clobbering it.
			if isExist(err) {
				continue
			}
			return "", fmt.Errorf("acquire: rename into %q: %w", candidate, err)
		}
		return candidate, nil
	}
	return "", fmt.Errorf("acquire: no free file name for %q", name)
}

func isExist(err error) bool {
	return errors.Is(err, fs.ErrExist) || os.IsExist(err)
}

// fileName builds the sanitized, extension-corrected name for a download.
func (m *Manager) fileName(file source.File, b source.Book, format string) string {
	raw := strings.TrimSpace(file.Name)
	if raw == "" {
		raw = b.Title
	}
	// A suggested name may carry a directory part, including a traversal;
	// only the last component is kept, and every remaining separator and
	// reserved character is removed.
	raw = strings.ReplaceAll(raw, `\`, "/")
	raw = path.Base(raw)
	raw = sanitizeComponent(raw)
	raw = strings.TrimFunc(raw, func(r rune) bool {
		return unicode.IsSpace(r) || r == '.'
	})

	ext := "." + format
	stem := raw
	switch {
	case strings.EqualFold(path.Ext(raw), ext):
		stem = raw[:len(raw)-len(path.Ext(raw))]
	case strings.EqualFold(path.Ext(raw), ".epub"), strings.EqualFold(path.Ext(raw), ".txt"):
		// The source guessed another book format; the bytes decide.
		stem = raw[:len(raw)-len(path.Ext(raw))]
	}
	stem = strings.TrimFunc(stem, func(r rune) bool {
		return unicode.IsSpace(r) || r == '.'
	})
	if stem == "" {
		stem = m.fallbackStem()
	}

	if prefix := sanitizeComponent(m.opts.Prefix); prefix != "" {
		stem = prefix + stem
	}
	stem = strings.TrimFunc(stem, func(r rune) bool {
		return unicode.IsSpace(r) || r == '.'
	})
	if stem == "" {
		stem = m.fallbackStem()
	}
	return truncate(stem, ext, "") + ext
}

// sanitizeComponent removes path separators, characters filesystems reserve,
// and control characters, while keeping spaces and non-Latin text.
func sanitizeComponent(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' ||
			r == '"' || r == '<' || r == '>' || r == '|':
			continue
		case unicode.IsControl(r):
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// splitExt splits a sanitized name into stem and extension.
func splitExt(name string) (string, string) {
	ext := path.Ext(name)
	return strings.TrimSuffix(name, ext), ext
}

// numberedName returns stem+ext for n == 1 and "stem (n).ext" afterwards.
func numberedName(stem, ext string, n int) string {
	if n <= 1 {
		return stem + ext
	}
	suffix := fmt.Sprintf(" (%d)", n)
	return truncate(stem, ext, suffix) + suffix + ext
}

// truncate shortens stem so that stem+suffix+ext fits maxNameBytes, cutting
// only at rune boundaries.
func truncate(stem, ext, suffix string) string {
	budget := maxNameBytes - len(ext) - len(suffix)
	if budget < 1 {
		budget = 1
	}
	if len(stem) <= budget {
		return stem
	}
	var b strings.Builder
	n := 0
	for _, r := range stem {
		size := utf8.RuneLen(r)
		if size < 0 {
			size = utf8.RuneLen(utf8.RuneError)
		}
		if n+size > budget {
			break
		}
		b.WriteRune(r)
		n += size
	}
	return b.String()
}

// fallbackStem names a download the source gave no usable name for.
func (m *Manager) fallbackStem() string {
	return "book-" + m.now().UTC().Format("20060102-150405")
}

func (m *Manager) now() time.Time {
	if m.opts.Now != nil {
		return m.opts.Now()
	}
	return time.Now()
}
