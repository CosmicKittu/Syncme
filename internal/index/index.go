// Package index scans the synced folder and caches content hashes.
package index

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"hash/fnv"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/CosmicKittu/Syncme/internal/config"
)

// Entry is the state of one file.
type Entry struct {
	Size  int64  `json:"s"`
	MTime int64  `json:"m"` // unix ms
	Hash  string `json:"h"`
}

// IsIgnored reports whether a path component is internal to syncme
// (trash, temp files) and must never be synced. Keep in sync with FileIndex.kt.
func IsIgnored(name string) bool {
	// .pending-/.trashed- are Android MediaStore placeholders
	return strings.HasPrefix(name, ".syncme") || strings.HasPrefix(name, ".pending-") || strings.HasPrefix(name, ".trashed-")
}

// ValidPath checks a relative, slash separated path received from a peer.
// This is a security boundary: it prevents escaping the synced folder.
func ValidPath(p string) bool {
	if p == "" || len(p) > 4096 || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\x00\\") {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." || len(seg) > 255 || IsIgnored(seg) {
			return false
		}
	}
	return true
}

type cacheEntry struct {
	Size  int64  `json:"s"`
	MNano int64  `json:"n"`
	Hash  string `json:"h"`
}

// HashCache avoids re-hashing files whose size and mtime are unchanged.
type HashCache struct {
	mu    sync.Mutex
	path  string
	m     map[string]cacheEntry
	dirty bool
}

func LoadCache(path string) *HashCache {
	c := &HashCache{path: path, m: map[string]cacheEntry{}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &c.m)
	}
	return c
}

func (c *HashCache) get(rel string, size, mnano int64) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[rel]
	if ok && e.Size == size && e.MNano == mnano {
		return e.Hash, true
	}
	return "", false
}

func (c *HashCache) Put(rel string, size, mnano int64, hash string) {
	c.mu.Lock()
	c.m[rel] = cacheEntry{size, mnano, hash}
	c.dirty = true
	c.mu.Unlock()
}

func (c *HashCache) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.dirty || c.path == "" {
		return nil
	}
	c.dirty = false
	return config.WriteJSONAtomic(c.path, c.m, 0o600)
}

func (c *HashCache) prune(seen map[string]Entry) {
	c.mu.Lock()
	for k := range c.m {
		if _, ok := seen[k]; !ok {
			delete(c.m, k)
			c.dirty = true
		}
	}
	c.mu.Unlock()
}

func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Rel converts an absolute path below root into a sync path.
func Rel(root, abs string) (string, bool) {
	r, err := filepath.Rel(root, abs)
	if err != nil {
		return "", false
	}
	r = filepath.ToSlash(r)
	return r, ValidPath(r)
}

// Abs converts a validated sync path into an absolute path below root.
func Abs(root, rel string) string {
	return filepath.Join(root, filepath.FromSlash(rel))
}

// Scan walks root and returns all regular files with their hashes.
// Files that disappear or cannot be read while scanning are skipped.
func Scan(root string, cache *HashCache) (map[string]Entry, error) {
	out := map[string]Entry{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			return nil
		}
		if p == root {
			return nil
		}
		if IsIgnored(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil // directories are walked, symlinks/devices skipped
		}
		rel, ok := Rel(root, p)
		if !ok {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		mnano := info.ModTime().UnixNano()
		h, ok := cache.get(rel, info.Size(), mnano)
		if !ok {
			h, err = HashFile(p)
			if err != nil {
				return nil
			}
			cache.Put(rel, info.Size(), mnano, h)
		}
		out[rel] = Entry{Size: info.Size(), MTime: info.ModTime().UnixMilli(), Hash: h}
		return nil
	})
	if err != nil {
		return nil, err
	}
	cache.prune(out)
	_ = cache.Save()
	return out, nil
}

// QuickSig is a cheap fingerprint of the folder (names, sizes, mtimes)
// used to detect changes without hashing content.
func QuickSig(root string) string {
	h := fnv.New64a()
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == root {
			return nil
		}
		if IsIgnored(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		io.WriteString(h, p+"\x00"+strconv.FormatInt(info.Size(), 10)+"\x00"+strconv.FormatInt(info.ModTime().UnixNano(), 10)+"\n")
		return nil
	})
	return strconv.FormatUint(h.Sum64(), 16)
}
