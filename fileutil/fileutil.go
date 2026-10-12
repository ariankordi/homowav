// Package fileutil holds the one file operation every stateful package here
// needs: replacing a private file without ever leaving it half-written.
package fileutil

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// PrivateFileMode is for files holding credentials or private keys.
const PrivateFileMode fs.FileMode = 0o600

// PrivateDirMode is for directories holding such files.
const PrivateDirMode fs.FileMode = 0o700

// WriteAtomic replaces path with data using a temp file plus rename, so a
// crash never leaves a truncated file. The file gets mode.
func WriteAtomic(path string, data []byte, mode fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	defer os.Remove(tmp.Name()) // No-op after a successful rename.
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
