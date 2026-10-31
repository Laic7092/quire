// Package sourceconf persists per-source configuration and imported
// definitions separately from the reader's state. A source's settings live
// under its stable ID, so adding or removing one source never touches another.
package sourceconf

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// Store holds configuration for every registered source. It is safe for
// concurrent use.
type Store struct {
	path string

	mu     sync.RWMutex
	values map[string]map[string]string

	writeMu sync.Mutex
}

// Open reads path into a Store. A missing file yields an empty store; a
// damaged file is backed up to path+".bak" and the store starts empty.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("sourceconf: empty path")
	}
	s := &Store{
		path:   path,
		values: make(map[string]map[string]string),
	}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return s, nil
	case err != nil:
		return nil, fmt.Errorf("sourceconf: read %s: %w", path, err)
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(trimmed, &s.values); err != nil {
		if berr := backup(path, data); berr != nil {
			return nil, fmt.Errorf("sourceconf: %s is not valid JSON (%v) and backup failed: %w", path, err, berr)
		}
		s.values = make(map[string]map[string]string)
		return s, nil
	}
	return s, nil
}

// Path returns the file the store writes to.
func (s *Store) Path() string { return s.path }

// Get returns a copy of the values stored for id, or nil when none exist.
func (s *Store) Get(id string) map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.values[id]
	if !ok {
		return nil
	}
	out := make(map[string]string, len(v))
	for k, val := range v {
		out[k] = val
	}
	return out
}

// Set merges values into id's configuration and persists the store. Existing
// keys not present in values are kept; passing an empty map creates the id
// entry if it did not exist.
func (s *Store) Set(id string, values map[string]string) error {
	if id == "" {
		return errors.New("sourceconf: empty id")
	}
	s.mu.Lock()
	cur, ok := s.values[id]
	if !ok {
		cur = make(map[string]string)
		s.values[id] = cur
	}
	for k, v := range values {
		cur[k] = v
	}
	s.mu.Unlock()
	return s.Save()
}

// Delete removes id's configuration and persists the store.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	delete(s.values, id)
	s.mu.Unlock()
	return s.Save()
}

// Save writes the store atomically: a temp file in the same directory, fsync,
// then rename. The directory is created with 0700 and the file is written
// with 0600.
func (s *Store) Save() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.mu.RLock()
	snap := make(map[string]map[string]string, len(s.values))
	for id, vals := range s.values {
		copyVals := make(map[string]string, len(vals))
		for k, v := range vals {
			copyVals[k] = v
		}
		snap[id] = copyVals
	}
	s.mu.RUnlock()

	data, err := json.MarshalIndent(&snap, "", "  ")
	if err != nil {
		return fmt.Errorf("sourceconf: marshal: %w", err)
	}
	data = append(data, '\n')
	return writeAtomic(s.path, data)
}

func backup(path string, data []byte) error {
	return writeAtomic(path+".bak", data)
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if tmp != "" {
			_ = os.Remove(tmp)
		}
	}()
	if err := os.Chmod(tmp, 0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	tmp = ""
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}
