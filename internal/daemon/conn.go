package daemon

import (
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/CosmicKittu/Syncme/internal/identity"
	"github.com/CosmicKittu/Syncme/internal/index"
	"github.com/CosmicKittu/Syncme/internal/proto"
)

const (
	handshakeTimeout = 20 * time.Second
	idleTimeout      = 90 * time.Second // phone must send something this often
	waitDuration     = 25 * time.Second
)

// HandleConn serves one phone connection (direct or via relay).
func (d *Daemon) HandleConn(raw net.Conn, via string) {
	defer raw.Close()
	if tc, ok := raw.(*net.TCPConn); ok {
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(30 * time.Second)
	}
	_ = raw.SetDeadline(time.Now().Add(handshakeTimeout))
	tlsConn := tls.Server(raw, d.tlsCfg)
	if err := tlsConn.Handshake(); err != nil {
		return
	}
	_ = raw.SetDeadline(time.Time{})
	c := proto.NewConn(tlsConn)
	c.SetReadTimeout(handshakeTimeout)
	m, err := c.Recv()
	if err != nil {
		return
	}
	switch m.T {
	case "pair":
		d.handlePair(c, m)
	case "hello":
		if err := d.handleSync(c, m, via); err != nil && !isClosed(err) {
			d.Log.Printf("connection %s: %v", c.RemoteAddr(), err)
		}
	default:
		_ = c.Send(&proto.Msg{T: "error", Error: "unexpected message"})
	}
}

func isClosed(err error) bool {
	if err == nil {
		return true
	}
	s := err.Error()
	return errors.Is(err, net.ErrClosed) || strings.Contains(s, "EOF") || strings.Contains(s, "reset by peer") || strings.Contains(s, "broken pipe") || strings.Contains(s, "user canceled")
}

func (d *Daemon) welcome() *proto.Msg {
	return &proto.Msg{T: "welcome", V: proto.Version, Name: d.Cfg.Name, ID: d.ID.ID, Addrs: d.Addresses(), Public: d.Public(), Relay: d.Cfg.Relay}
}

func (d *Daemon) handleSync(c *proto.Conn, hello *proto.Msg, via string) error {
	dev := d.devices.Get(hello.ID)
	nonce := proto.RandomHex(32)
	// Always send a challenge so unknown and known ids look the same.
	if err := c.Send(&proto.Msg{T: "challenge", Nonce: nonce, V: proto.Version}); err != nil {
		return err
	}
	auth, err := c.Recv()
	if err != nil {
		return err
	}
	if dev == nil || auth.T != "auth" || !proto.VerifyMAC(auth.MAC, proto.AuthMAC(dev.Secret, nonce, d.ID.ID, dev.ID)) {
		time.Sleep(500 * time.Millisecond)
		_ = c.Send(&proto.Msg{T: "error", Error: "not paired"})
		return fmt.Errorf("authentication failed for device %q", hello.ID)
	}
	if err := c.Send(d.welcome()); err != nil {
		return err
	}
	d.Log.Printf("%s connected (%s, %s)", dev.Name, via, c.RemoteAddr())
	d.devices.Touch(dev.ID, false)
	d.markConnected(dev.ID, 1)
	defer d.markConnected(dev.ID, -1)
	defer d.Log.Printf("%s disconnected", dev.Name)

	var pending <-chan recvResult // a message read during a previous wait
	lastSig := ""
	for {
		var m *proto.Msg
		if pending != nil {
			r := <-pending
			pending = nil
			if r.err != nil {
				return r.err
			}
			m = r.m
		} else {
			c.SetReadTimeout(idleTimeout)
			if m, err = c.Recv(); err != nil {
				return err
			}
		}
		switch m.T {
		case "sync":
			if err := d.runSession(c, dev, m.Files); err != nil {
				return err
			}
			lastSig = index.QuickSig(d.Cfg.Folder)
			d.devices.Touch(dev.ID, true)
		case "wait":
			if pending, err = d.wait(c, lastSig); err != nil {
				return err
			}
		case "poke", "ping":
			// stray poke after a wait already ended; nothing to do
		case "bye":
			return nil
		default:
			return fmt.Errorf("unexpected message %q", m.T)
		}
	}
}

type recvResult struct {
	m   *proto.Msg
	err error
}

// wait blocks until the folder changes, the CLI pokes, the phone pokes,
// or a timeout, then replies "wake". A read started during the wait may
// still be in flight; it is returned so the caller consumes it next.
func (d *Daemon) wait(c *proto.Conn, lastSig string) (<-chan recvResult, error) {
	ch := make(chan recvResult, 1)
	go func() {
		c.SetReadTimeout(idleTimeout)
		m, err := c.Recv()
		ch <- recvResult{m, err}
	}()
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	timer := time.NewTimer(waitDuration)
	defer timer.Stop()
	poke := d.pokeChan()
	reason := ""
	var inflight <-chan recvResult = ch
	for reason == "" {
		select {
		case r := <-ch:
			if r.err != nil {
				return nil, r.err
			}
			inflight = nil
			if r.m.T == "poke" {
				reason = "poke"
			} else {
				// Phone gave up waiting and sent a real request: answer
				// the wait and hand the request back to the main loop.
				done := make(chan recvResult, 1)
				done <- r
				inflight = done
				reason = "timeout"
			}
		case <-poke:
			reason = "poke"
		case <-tick.C:
			if lastSig != "" && index.QuickSig(d.Cfg.Folder) != lastSig {
				reason = "change"
			}
		case <-timer.C:
			reason = "timeout"
		}
	}
	if err := c.Send(&proto.Msg{T: "wake", Reason: reason}); err != nil {
		return nil, err
	}
	return inflight, nil
}

func (d *Daemon) markConnected(id string, delta int) {
	for {
		v, _ := d.connected.LoadOrStore(id, 0)
		if d.connected.CompareAndSwap(id, v, v.(int)+delta) {
			return
		}
	}
}

func (d *Daemon) IsConnected(id string) bool {
	v, ok := d.connected.Load(id)
	return ok && v.(int) > 0
}

// ---- pairing ----

type PairRequest struct {
	Name string `json:"name"`
	ID   string `json:"id"`
	Code string `json:"code"`
}

type pendingPair struct {
	token    string
	expires  time.Time
	used     bool
	request  chan PairRequest
	decision chan bool
}

// StartPairing creates a one-time token valid for 5 minutes.
func (d *Daemon) StartPairing() (string, time.Time) {
	d.pairMu.Lock()
	defer d.pairMu.Unlock()
	p := &pendingPair{token: proto.NewToken(), expires: time.Now().Add(5 * time.Minute), request: make(chan PairRequest, 1), decision: make(chan bool, 1)}
	d.pending = p
	return p.token, p.expires
}

func (d *Daemon) pendingFor(token string) *pendingPair {
	d.pairMu.Lock()
	defer d.pairMu.Unlock()
	if d.pending != nil && d.pending.token == token {
		return d.pending
	}
	return nil
}

// WaitPairRequest blocks until a phone presents the token.
func (d *Daemon) WaitPairRequest(token string, timeout time.Duration) (*PairRequest, error) {
	p := d.pendingFor(token)
	if p == nil {
		return nil, errors.New("no such pairing")
	}
	select {
	case r := <-p.request:
		return &r, nil
	case <-time.After(timeout):
		return nil, errors.New("timed out waiting for phone")
	}
}

func (d *Daemon) ConfirmPairing(token string, accept bool) error {
	p := d.pendingFor(token)
	if p == nil {
		return errors.New("no such pairing")
	}
	select {
	case p.decision <- accept:
		return nil
	default:
		return errors.New("already decided")
	}
}

func (d *Daemon) handlePair(c *proto.Conn, m *proto.Msg) {
	fail := func(msg string) { _ = c.Send(&proto.Msg{T: "error", Error: msg}) }
	if len(m.ID) < 16 || len(m.ID) > 128 || m.Name == "" {
		fail("bad request")
		return
	}
	d.pairMu.Lock()
	p := d.pending
	ok := p != nil && !p.used && time.Now().Before(p.expires)
	if ok {
		p.used = true // single attempt: a wrong guess burns the token
		if subtle.ConstantTimeCompare([]byte(strings.ToUpper(m.Token)), []byte(p.token)) != 1 {
			ok = false
			d.pending = nil
		}
	}
	d.pairMu.Unlock()
	if !ok {
		time.Sleep(time.Second)
		fail("invalid or expired pairing code; run `syncme pair` again")
		return
	}
	name := m.Name
	if len(name) > 64 {
		name = name[:64]
	}
	code := proto.PairCode(d.ID.ID, m.ID, p.token)
	if err := c.Send(&proto.Msg{T: "pair_pending", Code: code, Name: d.Cfg.Name}); err != nil {
		return
	}
	p.request <- PairRequest{Name: name, ID: m.ID, Code: code}
	var accept bool
	select {
	case accept = <-p.decision:
	case <-time.After(5 * time.Minute):
	}
	d.pairMu.Lock()
	if d.pending == p {
		d.pending = nil
	}
	d.pairMu.Unlock()
	if !accept {
		fail("pairing rejected on the PC")
		return
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		fail("internal error")
		return
	}
	dev := &Device{ID: m.ID, Name: name, Secret: secret, PairedAt: time.Now()}
	if err := d.devices.Put(dev); err != nil {
		fail("could not save device")
		return
	}
	// The phone may have been paired before: start from a clean base so
	// nothing is deleted based on stale history.
	_ = saveBase(d.Paths.DataDir, dev.ID, map[string]index.Entry{})
	w := d.welcome()
	w.T = "paired"
	w.Secret = encodeSecret(secret)
	_ = c.Send(w)
	d.Log.Printf("paired with %s (%s)", name, identity.Short(m.ID))
}
