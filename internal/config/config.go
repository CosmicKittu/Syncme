// Package config holds file locations and user settings.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/CosmicKittu/Syncme/internal/proto"
)

type Config struct {
	Name          string `json:"name"`           // shown on the phone
	Folder        string `json:"folder"`         // synced folder, default ~/syncme
	Port          int    `json:"port"`           // TCP listen port
	Discovery     bool   `json:"discovery"`      // answer LAN discovery broadcasts
	UPnP          bool   `json:"upnp"`           // try to open the port on the router
	PublicAddress string `json:"public_address"` // optional host:port reachable from internet (port forward / DDNS)
	Relay         string `json:"relay"`          // optional host:port of a syncme-relay
	Clipboard     bool   `json:"clipboard"`      // share the clipboard with paired devices
}

type Paths struct {
	ConfigDir string
	DataDir   string
	Socket    string
}

func home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

func DefaultPaths() Paths {
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(home(), ".config")
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(home(), ".local", "share")
	}
	p := Paths{ConfigDir: filepath.Join(cfg, "syncme"), DataDir: filepath.Join(data, "syncme")}
	if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
		p.Socket = filepath.Join(rt, "syncme.sock")
	} else {
		p.Socket = filepath.Join(p.ConfigDir, "control.sock")
	}
	if v := os.Getenv("SYNCME_HOME"); v != "" { // used by tests and multi-instance setups
		p = Paths{ConfigDir: filepath.Join(v, "config"), DataDir: filepath.Join(v, "data"), Socket: filepath.Join(v, "control.sock")}
	}
	return p
}

func Default() *Config {
	name, _ := os.Hostname()
	if name == "" {
		name = "Linux PC"
	}
	return &Config{
		Name:      name,
		Folder:    filepath.Join(home(), "syncme"),
		Port:      proto.DefaultPort,
		Discovery: true,
		UPnP:      true,
		Clipboard: true,
	}
}

func Load(p Paths) (*Config, error) {
	c := Default()
	path := filepath.Join(p.ConfigDir, "config.json")
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(p.ConfigDir, 0o700); err != nil {
			return nil, err
		}
		out, _ := json.MarshalIndent(c, "", "  ")
		return c, os.WriteFile(path, out, 0o600)
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, c); err != nil {
		return nil, err
	}
	if c.Port == 0 {
		c.Port = proto.DefaultPort
	}
	return c, nil
}

// WriteJSONAtomic writes v to path via a temp file and rename.
func WriteJSONAtomic(path string, v any, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
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
	return os.Rename(tmp, path)
}
