package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/CosmicKittu/Syncme/internal/config"
	"github.com/CosmicKittu/Syncme/internal/index"
)

// Device is a paired phone.
type Device struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Secret   []byte    `json:"secret"` // 32 random bytes shared at pairing
	PairedAt time.Time `json:"paired_at"`
	LastSeen time.Time `json:"last_seen"`
	LastSync time.Time `json:"last_sync"`
}

type DeviceStore struct {
	mu   sync.Mutex
	path string
	m    map[string]*Device
}

func LoadDevices(path string) (*DeviceStore, error) {
	s := &DeviceStore{path: path, m: map[string]*Device{}}
	b, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(b, &s.m); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return s, nil
}

func (s *DeviceStore) saveLocked() error { return config.WriteJSONAtomic(s.path, s.m, 0o600) }

func (s *DeviceStore) Get(id string) *Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.m[id]; ok {
		c := *d
		return &c
	}
	return nil
}

func (s *DeviceStore) Put(d *Device) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := *d
	s.m[d.ID] = &c
	return s.saveLocked()
}

func (s *DeviceStore) Touch(id string, synced bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.m[id]; ok {
		d.LastSeen = time.Now()
		if synced {
			d.LastSync = d.LastSeen
		}
		_ = s.saveLocked()
	}
}

func (s *DeviceStore) Remove(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[id]; !ok {
		return false
	}
	delete(s.m, id)
	_ = s.saveLocked()
	return true
}

func (s *DeviceStore) List() []Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Device, 0, len(s.m))
	for _, d := range s.m {
		c := *d
		c.Secret = nil
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PairedAt.Before(out[j].PairedAt) })
	return out
}

// Base state per device: what both sides agreed on after the last sync.

func basePath(dataDir, id string) string { return filepath.Join(dataDir, "peers", id+".json") }

func loadBase(dataDir, id string) map[string]index.Entry {
	m := map[string]index.Entry{}
	if b, err := os.ReadFile(basePath(dataDir, id)); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func saveBase(dataDir, id string, m map[string]index.Entry) error {
	return config.WriteJSONAtomic(basePath(dataDir, id), m, 0o600)
}
