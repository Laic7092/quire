package book

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every committed sample must open and build. A truncated EPUB is not a valid
// zip, so this catches a broken download before it confuses a manual test.
func TestSamplesOpen(t *testing.T) {
	entries, err := os.ReadDir("testdata/samples")
	if err != nil {
		t.Fatal(err)
	}
	tested := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || name == "README.md" {
			continue
		}
		switch strings.ToLower(filepath.Ext(name)) {
		case ".epub", ".txt", ".text":
		default:
			continue
		}
		tested++
		t.Run(name, func(t *testing.T) {
			b, err := Open(filepath.Join("testdata/samples", name))
			if err != nil {
				t.Fatal(err)
			}
			if len(b.Chapters) == 0 {
				t.Fatal("no chapters")
			}
			if _, _, err := b.Build(18); err != nil {
				t.Fatal(err)
			}
		})
	}
	if tested == 0 {
		t.Skip("no samples")
	}
}
