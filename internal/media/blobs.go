// Package media stores attachment bytes content-addressed by SHA-256 (docs/storage.md §6).
package media

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

// Blobs is a directory of files named by hash: <root>/<aa>/<sha256>.
type Blobs struct {
	root string
}

// Open prepares the directory.
func Open(root string) (*Blobs, error) {
	if err := os.MkdirAll(filepath.Join(root, "tmp"), 0o750); err != nil {
		return nil, fmt.Errorf("media dir: %w", err)
	}
	return &Blobs{root: root}, nil
}

// Put streams r to a temp file, hashes it, and moves it into place. Existing hashes are reused.
func (b *Blobs) Put(r io.Reader) (sha string, size int64, err error) {
	tmp := filepath.Join(b.root, "tmp", uuid.NewString())
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // path is generated
	if err != nil {
		return "", 0, fmt.Errorf("temp file: %w", err)
	}
	defer func() { _ = os.Remove(tmp) }()
	h := sha256.New()
	size, err = io.Copy(io.MultiWriter(f, h), r)
	if err != nil {
		_ = f.Close()
		return "", 0, fmt.Errorf("write: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return "", 0, err
	}
	if err := f.Close(); err != nil {
		return "", 0, err
	}
	sha = hex.EncodeToString(h.Sum(nil))
	dst := b.Path(sha)
	if _, err := os.Stat(dst); err == nil {
		return sha, size, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return "", 0, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", 0, fmt.Errorf("move into place: %w", err)
	}
	return sha, size, nil
}

// Path returns where a hash lives.
func (b *Blobs) Path(sha string) string {
	if len(sha) < 2 {
		return filepath.Join(b.root, "invalid", sha)
	}
	return filepath.Join(b.root, sha[:2], sha)
}

// Open opens a blob for reading.
func (b *Blobs) Open(sha string) (*os.File, error) {
	return os.Open(b.Path(sha))
}

// Remove deletes a blob; missing files are not an error.
func (b *Blobs) Remove(sha string) error {
	err := os.Remove(b.Path(sha))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
