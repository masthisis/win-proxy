// Package xjson provides crash-safe JSON persistence for small documents
// (settings, profile registry, test history).
//
// DESIGN: Repository pattern over the OS filesystem. Writes are atomic:
// the document is serialized into a temp file in the same directory and then
// renamed over the target, so a crash mid-write can never produce a truncated
// JSON file. A lost write at worst loses the most recent change.
//
// AI-DEVELOPER NOTES:
//   - Never read-modify-write JSON files directly; always go through Store
//     to keep atomicity guarantees.
//   - Documents must be JSON-serializable structs with a sane zero value;
//     Load returns the zero value when the file is missing.
package xjson

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrNotFound is returned by Load when the file does not exist.
var ErrNotFound = errors.New("xjson: document not found")

// Store persists one JSON document at a fixed path.
type Store[T any] struct {
	path string
}

// NewStore creates a store for the given file path. The parent directory is
// created lazily on first Save.
func NewStore[T any](path string) *Store[T] {
	return &Store[T]{path: path}
}

// Path returns the backing file path.
func (s *Store[T]) Path() string { return s.path }

// Load reads the document. Returns ErrNotFound when missing.
func (s *Store[T]) Load() (T, error) {
	var zero T
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return zero, ErrNotFound
		}
		return zero, fmt.Errorf("xjson: read %s: %w", s.path, err)
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return zero, fmt.Errorf("xjson: decode %s: %w", s.path, err)
	}
	return v, nil
}

// LoadOrDefault reads the document, returning def when missing or corrupted.
// Corrupted files are renamed to "<path>.corrupt" so the app can self-heal.
func (s *Store[T]) LoadOrDefault(def T) T {
	v, err := s.Load()
	if err == nil {
		return v
	}
	if !errors.Is(err, ErrNotFound) {
		_ = os.Rename(s.path, s.path+".corrupt")
	}
	return def
}

// Save atomically writes the document.
func (s *Store[T]) Save(v T) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("xjson: encode: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("xjson: mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".nr-*")
	if err != nil {
		return fmt.Errorf("xjson: temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op after successful rename

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("xjson: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("xjson: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("xjson: close: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("xjson: rename: %w", err)
	}
	return nil
}
