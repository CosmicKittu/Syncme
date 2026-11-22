// Package daemon is the long running Syncme service on the PC.
package daemon

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/CosmicKittu/Syncme/internal/config"
	"github.com/CosmicKittu/Syncme/internal/identity"
	"github.com/CosmicKittu/Syncme/internal/index"
)

type Daemon struct {
	Cfg   *config.Config
	Paths config.Paths
	ID    *identity.Identity
	Log   *log.Logger

	tlsCfg  *tls.Config
	devices *DeviceStore
	cache   *index.HashCache

	syncMu sync.Mutex // one sync session touches the folder at a time

	pairMu  sync.Mutex
	pending *pendingPair

	pokeMu sync.Mutex
	pokeCh chan struct{}

	public    atomic.Value // string
	connected sync.Map     // device id -> int (connection count)
}

func New(cfg *config.Config, paths config.Paths, logger *log.Logger) (*Daemon, error) {
	id, err := identity.LoadOrCreate(paths.ConfigDir)
	if err != nil {
		return nil, fmt.Errorf("identity: %w", err)
	}
	devs, err := LoadDevices(filepath.Join(paths.ConfigDir, "devices.json"))
	if err != nil {
		return nil, fmt.Errorf("devices: %w", err)
	}
	d := &Daemon{
		Cfg: cfg, Paths: paths, ID: id, Log: logger, devices: devs,
		cache:  index.LoadCache(filepath.Join(paths.DataDir, "hashcache.json")),
		pokeCh: make(chan struct{}),
	}
	d.tlsCfg = &tls.Config{Certificates: []tls.Certificate{id.Cert}, MinVersion: tls.VersionTLS12}
	d.public.Store(cfg.PublicAddress)
	return d, nil
}

func (d *Daemon) Devices() *DeviceStore { return d.devices }

// Poke wakes every waiting phone so it syncs now.
func (d *Daemon) Poke() {
	d.pokeMu.Lock()
	close(d.pokeCh)
	d.pokeCh = make(chan struct{})
	d.pokeMu.Unlock()
}

func (d *Daemon) pokeChan() <-chan struct{} {
	d.pokeMu.Lock()
	defer d.pokeMu.Unlock()
	return d.pokeCh
}

func (d *Daemon) Run(ctx context.Context) error {
	if err := os.MkdirAll(d.Cfg.Folder, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(d.Paths.DataDir, 0o700); err != nil {
		return err
	}
	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", fmt.Sprintf(":%d", d.Cfg.Port))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	d.Log.Printf("syncme %s (%s) syncing %s on port %d", d.Cfg.Name, identity.Short(d.ID.ID), d.Cfg.Folder, d.Cfg.Port)

	ctl, err := d.serveControl(ctx)
	if err != nil {
		ln.Close()
		return fmt.Errorf("control socket: %w", err)
	}
	defer ctl.Close()

	if d.Cfg.Discovery {
		go d.discoveryLoop(ctx)
	}
	if d.Cfg.UPnP && d.Cfg.PublicAddress == "" {
		go d.upnpLoop(ctx)
	}
	if d.Cfg.Relay != "" {
		go d.relayLoop(ctx)
	}
	go d.trashCleanupLoop(ctx)

	go func() { <-ctx.Done(); ln.Close() }()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				return err
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		go d.HandleConn(c, "lan")
	}
}

// Addresses returns host:port candidates for the phone, LAN first.
func (d *Daemon) Addresses() []string {
	var out []string
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.IsLinkLocalUnicast() || ipn.IP.IsLoopback() {
				continue
			}
			out = append(out, net.JoinHostPort(ipn.IP.String(), fmt.Sprint(d.Cfg.Port)))
		}
	}
	return out
}

func (d *Daemon) Public() string { s, _ := d.public.Load().(string); return s }

func (d *Daemon) trashCleanupLoop(ctx context.Context) {
	t := time.NewTicker(6 * time.Hour)
	defer t.Stop()
	for {
		cleanTrash(filepath.Join(d.Cfg.Folder, trashDir), 30*24*time.Hour)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
