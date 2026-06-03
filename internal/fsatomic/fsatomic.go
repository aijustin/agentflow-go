// Package fsatomic provides crash-safe atomic file writes shared by the
// file-backed adapters.
package fsatomic

import (
	"os"
	"path/filepath"
)

// WriteFile atomically writes data to path with the given permissions. It
// writes to a temporary file in the same directory, fsyncs it, renames it into
// place, then best-effort fsyncs the parent directory so the rename survives a
// crash. The parent directory is created with 0o700 if it does not exist.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	syncDir(dir)
	return nil
}

// syncDir best-effort fsyncs a directory so a rename survives a crash. Errors
// are ignored because some filesystems/platforms do not support directory
// fsync; the file contents are already durable via the tmp file Sync above.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
