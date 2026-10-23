package book

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Open opens an EPUB or a plain text book. The extension decides; with an
// unknown extension the file is an EPUB when it starts like a zip archive and
// a text book otherwise.
func Open(file string) (*Book, error) {
	switch strings.ToLower(filepath.Ext(file)) {
	case ".txt", ".text":
		return openText(file)
	case ".epub":
		return openEPUB(file)
	}
	if isZipArchive(file) {
		return openEPUB(file)
	}
	return openText(file)
}

func isZipArchive(file string) bool {
	f, err := os.Open(file)
	if err != nil {
		return false
	}
	defer f.Close()
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return false
	}
	return magic[0] == 'P' && magic[1] == 'K'
}
