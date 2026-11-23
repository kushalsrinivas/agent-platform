// Package artifacts stores large checkpoint payloads outside PostgreSQL.
//
// The orchestrator is the only writer and reader: workers upload checkpoint bytes over the fenced control RPC and
// never receive object-store credentials. The filesystem backend is deliberately small; an S3/MinIO backend can
// replace it behind the same two methods without changing the lifecycle code.
package artifacts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotFound is returned when an object key has no stored payload.
var ErrNotFound = errors.New("artifacts: object not found")

// Store is a content-addressable-by-key artifact directory.
type Store struct {
	dir string
}

// New creates the artifact directory when a path is configured. An empty path disables offloading and callers
// keep small states inline in PostgreSQL.
func New(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, nil
	}
	cleaned, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("artifacts: resolve directory: %w", err)
	}
	if err := os.MkdirAll(cleaned, 0o700); err != nil {
		return nil, fmt.Errorf("artifacts: create directory: %w", err)
	}
	return &Store{dir: cleaned}, nil
}

// Put writes one object atomically. The caller supplies a safe, orchestrator-generated key.
func (s *Store) Put(_ context.Context, key string, data []byte) error {
	if s == nil {
		return errors.New("artifacts: store is not configured")
	}
	path, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("artifacts: create parent: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".artifact-*")
	if err != nil {
		return fmt.Errorf("artifacts: create temp file: %w", err)
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("artifacts: write object: %w", err)
	}
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("artifacts: protect object: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("artifacts: close object: %w", err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("artifacts: commit object: %w", err)
	}
	return nil
}

// Get reads one object.
func (s *Store) Get(_ context.Context, key string) ([]byte, error) {
	if s == nil {
		return nil, errors.New("artifacts: store is not configured")
	}
	path, err := s.path(key)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path) //nolint:gosec // path is confined to the configured artifact directory.
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("artifacts: read object: %w", err)
	}
	return data, nil
}

func (s *Store) path(key string) (string, error) {
	if key == "" || strings.ContainsRune(key, 0) || strings.HasPrefix(key, "/") ||
		strings.Contains(key, "..") || filepath.Clean(key) != key {
		return "", errors.New("artifacts: object key is not a safe relative path")
	}
	path := filepath.Join(s.dir, key)
	if !strings.HasPrefix(path, s.dir+string(os.PathSeparator)) {
		return "", errors.New("artifacts: object key escapes the store")
	}
	return path, nil
}
