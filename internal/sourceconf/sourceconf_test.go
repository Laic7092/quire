package sourceconf

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenMissingStartsEmpty(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "sources.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Get("zlib"); got != nil {
		t.Fatalf("missing file should start empty, got %v", got)
	}
}

func TestRoundTripAndPartialUpdate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sources.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set("zlib", map[string]string{"site": "example.com", "email": "a@b"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("wenku8", map[string]string{"timeout": "30s"}); err != nil {
		t.Fatal(err)
	}
	// Partial update keeps other keys.
	if err := s.Set("zlib", map[string]string{"email": "c@d"}); err != nil {
		t.Fatal(err)
	}

	got := s.Get("zlib")
	if got["site"] != "example.com" || got["email"] != "c@d" {
		t.Fatalf("partial update lost keys: %v", got)
	}

	// Re-open and check persistence.
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.Get("wenku8"); got["timeout"] != "30s" {
		t.Fatalf("wenku8 not persisted: %v", got)
	}
	if got := s2.Get("zlib"); got["site"] != "example.com" || got["email"] != "c@d" {
		t.Fatalf("zlib not persisted after re-open: %v", got)
	}
}

func TestDelete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sources.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Set("legado", map[string]string{"definitions": "[]"})
	if s.Get("legado") == nil {
		t.Fatal("legado should exist")
	}
	if err := s.Delete("legado"); err != nil {
		t.Fatal(err)
	}
	if s.Get("legado") != nil {
		t.Fatal("legado should be deleted")
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Get("legado") != nil {
		t.Fatal("legado should stay deleted after re-open")
	}
}

func TestCorruptedFileBackedUp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sources.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Get("any") != nil {
		t.Fatal("corrupted file should start empty")
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatalf("expected backup file: %v", err)
	}
}

func TestPermissions(t *testing.T) {
	// Use a nested directory so sourceconf creates it with 0700.
	dir := filepath.Join(t.TempDir(), "quire")
	path := filepath.Join(dir, "sources.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set("zlib", map[string]string{"site": "x"}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode want 0600, got %o", info.Mode().Perm())
	}
	if info, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode want 0700, got %o", info.Mode().Perm())
	}
}

func TestGetReturnsCopy(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "sources.json"))
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Set("zlib", map[string]string{"site": "x"})
	got := s.Get("zlib")
	got["site"] = "mutated"
	if s.Get("zlib")["site"] != "x" {
		t.Fatal("Get returned a live map")
	}
}
