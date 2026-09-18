// Package atomicfile writes files via a temporary sibling and rename.
package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFile writes data to path so readers never observe a partial file.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("atomicfile: create temp: %w", err)
	}
	name := tmp.Name()
	cleanup := func() { _ = os.Remove(name) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("atomicfile: write: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("atomicfile: chmod: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("atomicfile: close: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		cleanup()
		return fmt.Errorf("atomicfile: rename: %w", err)
	}
	return nil
}
