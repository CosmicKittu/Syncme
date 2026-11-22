package daemon

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/huin/goupnp/dcps/internetgateway2"

	"github.com/CosmicKittu/Syncme/internal/proto"
)

// ---- LAN discovery ----
// The phone broadcasts "SYNCME?1" to UDP 47811; we answer with our id and
// port. The phone only trusts the answer if the id matches the pinned key
// (and TLS proves it), so spoofed answers can only cause a failed connect.

func (d *Daemon) discoveryLoop(ctx context.Context) {
	for ctx.Err() == nil {
		pc, err := net.ListenPacket("udp", fmt.Sprintf(":%d", proto.DiscoveryPort))
		if err != nil {
			d.Log.Printf("discovery disabled: %v", err)
			return
		}
		go func() { <-ctx.Done(); pc.Close() }()
		reply, _ := json.Marshal(map[string]any{"syncme": proto.Version, "id": d.ID.ID, "name": d.Cfg.Name, "port": d.Cfg.Port})
		buf := make([]byte, 512)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				break
			}
			if strings.HasPrefix(string(buf[:n]), "SYNCME?") {
				_, _ = pc.WriteTo(reply, addr)
			}
		}
		pc.Close()
		time.Sleep(time.Second)
	}
}

// ---- UPnP: open our port on the home router for internet access ----

type igdClient interface {
	AddPortMapping(remoteHost string, extPort uint16, proto string, intPort uint16, intClient string, enabled bool, desc string, lease uint32) error
	GetExternalIPAddress() (string, error)
}

func (d *Daemon) upnpLoop(ctx context.Context) {
	for {
		if addr, err := d.mapPort(ctx); err != nil {
			d.Log.Printf("upnp: %v (internet access needs a relay, a port forward or a VPN like Tailscale)", err)
			d.public.Store("")
		} else if addr != d.Public() {
			d.Log.Printf("upnp: reachable from the internet at %s", addr)
			d.public.Store(addr)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(20 * time.Minute):
		}
	}
}

func (d *Daemon) mapPort(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var clients []igdClient
	var hosts []string
	if cs, _, err := internetgateway2.NewWANIPConnection2ClientsCtx(ctx); err == nil {
		for _, c := range cs {
			clients, hosts = append(clients, c), append(hosts, c.Location.Host)
		}
	}
	if cs, _, err := internetgateway2.NewWANIPConnection1ClientsCtx(ctx); err == nil {
		for _, c := range cs {
			clients, hosts = append(clients, c), append(hosts, c.Location.Host)
		}
	}
	if cs, _, err := internetgateway2.NewWANPPPConnection1ClientsCtx(ctx); err == nil {
		for _, c := range cs {
			clients, hosts = append(clients, c), append(hosts, c.Location.Host)
		}
	}
	if len(clients) == 0 {
		return "", errors.New("no UPnP router found")
	}
	var lastErr error
	for i, c := range clients {
		host := hosts[i]
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		u, err := net.Dial("udp", net.JoinHostPort(host, "1900"))
		if err != nil {
			lastErr = err
			continue
		}
		local := u.LocalAddr().(*net.UDPAddr).IP.String()
		u.Close()
		port := uint16(d.Cfg.Port)
		if err := c.AddPortMapping("", port, "TCP", port, local, true, "syncme", 3600); err != nil {
			lastErr = err
			continue
		}
		ip, err := c.GetExternalIPAddress()
		if err != nil {
			lastErr = err
			continue
		}
		pip := net.ParseIP(ip)
		if pip == nil || pip.IsPrivate() || isCGNAT(pip) {
			return "", fmt.Errorf("router has no public address (%s); carrier-grade NAT?", ip)
		}
		return net.JoinHostPort(ip, fmt.Sprint(port)), nil
	}
	return "", lastErr
}

func isCGNAT(ip net.IP) bool {
	_, n, _ := net.ParseCIDR("100.64.0.0/10")
	return n.Contains(ip)
}

// ---- Relay: last resort when no direct path exists ----
// The relay only forwards bytes. The phone still runs TLS end-to-end with
// this PC and pins its key, so the relay cannot read or alter anything.

func readLine(c net.Conn) (string, error) {
	// byte at a time so we never consume data that belongs to TLS
	var b [1]byte
	var sb strings.Builder
	for sb.Len() < 1024 {
		if _, err := c.Read(b[:]); err != nil {
			return "", err
		}
		if b[0] == '\n' {
			return strings.TrimRight(sb.String(), "\r"), nil
		}
		sb.WriteByte(b[0])
	}
	return "", errors.New("relay line too long")
}

func signNonce(key *ecdsa.PrivateKey, nonce string) (string, error) {
	h := sha256.Sum256(proto.RelayListenMessage(nonce))
	sig, err := ecdsa.SignASN1(rand.Reader, key, h[:])
	return base64.StdEncoding.EncodeToString(sig), err
}

func (d *Daemon) relayLoop(ctx context.Context) {
	backoff := 5 * time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := d.relayListen(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > time.Minute {
			backoff = 5 * time.Second
		}
		d.Log.Printf("relay %s: %v (retrying in %s)", d.Cfg.Relay, err, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 2*time.Minute {
			backoff *= 2
		}
	}
}

func (d *Daemon) relayListen(ctx context.Context) error {
	var dialer net.Dialer
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	c, err := dialer.DialContext(dctx, "tcp", d.Cfg.Relay)
	cancel()
	if err != nil {
		return err
	}
	defer c.Close()
	go func() { <-ctx.Done(); c.Close() }()
	_ = c.SetDeadline(time.Now().Add(15 * time.Second))
	line, err := readLine(c)
	if err != nil {
		return err
	}
	nonce, ok := strings.CutPrefix(line, "NONCE ")
	if !ok {
		return fmt.Errorf("unexpected greeting %q", line)
	}
	sig, err := signNonce(d.ID.Key, nonce)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(c, "LISTEN %s %s %s\n", d.ID.ID, base64.StdEncoding.EncodeToString(d.ID.SPKI), sig); err != nil {
		return err
	}
	if line, err = readLine(c); err != nil {
		return err
	}
	if line != "OK" {
		return fmt.Errorf("relay refused: %s", line)
	}
	d.Log.Printf("relay %s: registered", d.Cfg.Relay)
	r := bufio.NewReader(c) // control connection carries only lines
	for {
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Minute)) // relay pings every 30s
		line, err := r.ReadString('\n')
		if err != nil {
			return err
		}
		line = strings.TrimSpace(line)
		if sid, ok := strings.CutPrefix(line, "INCOMING "); ok {
			go d.relayAccept(ctx, sid)
		}
	}
}

func (d *Daemon) relayAccept(ctx context.Context, sid string) {
	var dialer net.Dialer
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	c, err := dialer.DialContext(dctx, "tcp", d.Cfg.Relay)
	cancel()
	if err != nil {
		return
	}
	_ = c.SetDeadline(time.Now().Add(15 * time.Second))
	if _, err := readLine(c); err != nil { // NONCE, unused here
		c.Close()
		return
	}
	if _, err := fmt.Fprintf(c, "ACCEPT %s\n", sid); err != nil {
		c.Close()
		return
	}
	if line, err := readLine(c); err != nil || line != "OK" {
		c.Close()
		return
	}
	_ = c.SetDeadline(time.Time{})
	d.HandleConn(c, "relay")
}
