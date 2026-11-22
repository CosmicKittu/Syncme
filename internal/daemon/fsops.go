package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/CosmicKittu/Syncme/internal/index"
)

// Deleted files are never removed outright: they go to <folder>/.syncme-trash
// (ignored by sync, same filesystem so the move is atomic) and are purged
// after 30 days.
const trashDir = ".syncme-trash"

func moveToTrash(root, rel string) error {
	dst := filepath.Join(root, trashDir, time.Now().Format("2006-01-02"), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if _, err := os.Lstat(dst); err == nil {
		dst += "." + time.Now().Format("150405.000")
	}
	return os.Rename(index.Abs(root, rel), dst)
}

func pruneEmptyDirs(root, dir string) {
	root = filepath.Clean(root)
	for {
		dir = filepath.Clean(dir)
		if dir == root || !strings.HasPrefix(dir, root+string(os.PathSeparator)) {
			return
		}
		if err := os.Remove(dir); err != nil { // fails unless empty
			return
		}
		dir = filepath.Dir(dir)
	}
}

func cleanTrash(trash string, maxAge time.Duration) {
	entries, err := os.ReadDir(trash)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	for _, e := range entries {
		t, err := time.Parse("2006-01-02", e.Name())
		if err == nil && t.Before(cutoff) {
			_ = os.RemoveAll(filepath.Join(trash, e.Name()))
		}
	}
}
