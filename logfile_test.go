package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogGoesToTheDocumentsDirectoryWhenThereIsOne(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := nextToState("/somewhere/state.json"); got != "/somewhere/quire.log" {
		t.Fatalf("without Documents the log belongs beside the state, got %q", got)
	}
	docs := filepath.Join(home, "Documents")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := nextToState("/somewhere/state.json"); got != filepath.Join(docs, "quire.log") {
		t.Fatalf("with Documents the log belongs there, got %q", got)
	}
}

func TestLogToFileWritesAndRestores(t *testing.T) {
	path := filepath.Join(t.TempDir(), "quire.log")
	done := logToFile(path)
	writeLog("hello")
	if !strings.Contains(readFile(t, path), "hello") {
		t.Fatalf("the log did not reach %q", path)
	}
	done()
	writeLog("after")
	if strings.Contains(readFile(t, path), "after") {
		t.Fatal("the log went on writing after it was put back")
	}
}
