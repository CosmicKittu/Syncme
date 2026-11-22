// Package relay is a tiny rendezvous/forwarding server for when the phone
// cannot reach the PC directly (different networks, no port forwarding).
//
// It never sees plaintext: the phone runs TLS end-to-end with the PC and
// pins the PC's key. A PC must prove it owns its id (by signing a nonce
// with its key) before it can receive connections for that id.
//
// Line protocol (each line ends with \n), relay speaks first:
//
//	relay  -> "NONCE <hex>"
//	PC     -> "LISTEN <id> <spki-base64> <sig-base64>"   relay -> "OK" | "ERR ..."
//	relay  -> "INCOMING <sid>" / "PING"                    (on the LISTEN connection)
//	phone  -> "CONNECT <id>"                               relay -> "OK" | "ERR ..."
//	PC     -> "ACCEPT <sid>"                               relay -> "OK"
//
// After both OKs the relay copies bytes between phone and PC.
package relay

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/CosmicKittu/Syncme/internal/identity"
	"github.com/CosmicKittu/Syncme/internal/proto"
)

type Server struct {
	Log      *log.Logger
	MaxConns int64

	mu        sync.Mutex
	listeners map[string]*listener
	pending   map[string]chan *peer
	conns     atomic.Int64
}

type listener struct {
	c   net.Conn
	wmu sync.Mutex
}

func (l *listener) send(s string) error {
	l.wmu.Lock()
	defer l.wmu.Unlock()
	_ = l.c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := io.WriteString(l.c, s+"\n")
	return err
}

type peer struct {
	c    net.Conn
	r    *bufio.Reader
	done chan struct{}
}

func (s *Server) Serve(ln net.Listener) error {
	if s.listeners == nil {
		s.listeners = map[string]*listener{}
		s.pending = map[string]chan *peer{}
	}
	if s.MaxConns == 0 {
		s.MaxConns = 2000
	}
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		if s.conns.Add(1) > s.MaxConns {
			s.conns.Add(-1)
			c.Close()
			continue
		}
		go func() {
			defer s.conns.Add(-1)
			s.handle(c)
		}()
	}
}

func (s *Server) handle(c net.Conn) {
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(30 * time.Second)
	}
	closeConn := true
	defer func() {
		if closeConn {
			c.Close()
		}
	}()
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	nonce := proto.RandomHex(16)
	if _, err := fmt.Fprintf(c, "NONCE %s\n", nonce); err != nil {
		return
	}
	r := bufio.NewReaderSize(c, 4096)
	line, err := readLimited(r)
	if err != nil {
		return
	}
	f := strings.Fields(line)
	if len(f) == 0 {
		return
	}
	switch {
	case f[0] == "LISTEN" && len(f) == 4:
		s.handleListen(c, r, nonce, f[1], f[2], f[3])
	case f[0] == "CONNECT" && len(f) == 2:
		s.handleConnect(c, r, f[1])
	case f[0] == "ACCEPT" && len(f) == 2:
		if s.handleAccept(c, r, f[1]) {
			closeConn = false // ownership moved to the phone side's splice
		}
	default:
		fmt.Fprintf(c, "ERR bad command\n")
	}
}

func readLimited(r *bufio.Reader) (string, error) {
	var sb strings.Builder
	for sb.Len() < 1024 {
		b, err := r.ReadByte()
		if err != nil {
			return "", err
		}
		if b == '\n' {
			return strings.TrimRight(sb.String(), "\r"), nil
		}
		sb.WriteByte(b)
	}
	return "", errors.New("line too long")
}

func verifyListen(nonce, id, spkiB64, sigB64 string) error {
	spki, err := base64.StdEncoding.DecodeString(spkiB64)
	if err != nil {
		return err
	}
	if identity.IDFromSPKI(spki) != id {
		return errors.New("id does not match key")
	}
	pub, err := x509.ParsePKIXPublicKey(spki)
	if err != nil {
		return err
	}
	ek, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return errors.New("unsupported key")
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return err
	}
	h := sha256.Sum256(proto.RelayListenMessage(nonce))
	if !ecdsa.VerifyASN1(ek, h[:], sig) {
		return errors.New("bad signature")
	}
	return nil
}

func (s *Server) handleListen(c net.Conn, r *bufio.Reader, nonce, id, spki, sig string) {
	if err := verifyListen(nonce, id, spki, sig); err != nil {
		fmt.Fprintf(c, "ERR %v\n", err)
		return
	}
	l := &listener{c: c}
	s.mu.Lock()
	if old := s.listeners[id]; old != nil {
		old.c.Close() // newest registration wins (e.g. PC changed network)
	}
	s.listeners[id] = l
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.listeners[id] == l {
			delete(s.listeners, id)
		}
		s.mu.Unlock()
	}()
	_ = c.SetDeadline(time.Time{})
	if l.send("OK") != nil {
		return
	}
	s.logf("pc %s online", identity.Short(id))
	go func() {
		for {
			time.Sleep(30 * time.Second)
			if l.send("PING") != nil {
				c.Close()
				return
			}
		}
	}()
	// The PC sends nothing on this connection; reading detects closure.
	_, _ = io.Copy(io.Discard, r)
	s.logf("pc %s offline", identity.Short(id))
}

func (s *Server) handleConnect(c net.Conn, r *bufio.Reader, id string) {
	s.mu.Lock()
	l := s.listeners[id]
	if l == nil || len(s.pending) > 10000 {
		s.mu.Unlock()
		fmt.Fprintf(c, "ERR offline\n")
		return
	}
	sid := proto.RandomHex(16)
	ch := make(chan *peer, 1)
	s.pending[sid] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pending, sid)
		s.mu.Unlock()
	}()
	if l.send("INCOMING "+sid) != nil {
		fmt.Fprintf(c, "ERR offline\n")
		return
	}
	var pc *peer
	select {
	case pc = <-ch:
	case <-time.After(15 * time.Second):
		fmt.Fprintf(c, "ERR timeout\n")
		return
	}
	defer close(pc.done)
	defer pc.c.Close()
	_ = c.SetDeadline(time.Time{})
	_ = pc.c.SetDeadline(time.Time{})
	if _, err := io.WriteString(c, "OK\n"); err != nil {
		return
	}
	if _, err := io.WriteString(pc.c, "OK\n"); err != nil {
		return
	}
	s.logf("relaying session to %s", identity.Short(id))
	splice(c, r, pc.c, pc.r)
}

func (s *Server) handleAccept(c net.Conn, r *bufio.Reader, sid string) bool {
	s.mu.Lock()
	ch := s.pending[sid]
	delete(s.pending, sid)
	s.mu.Unlock()
	if ch == nil {
		fmt.Fprintf(c, "ERR unknown session\n")
		return false
	}
	p := &peer{c: c, r: r, done: make(chan struct{})}
	ch <- p
	<-p.done
	return true
}

// splice copies both ways; when either direction ends, both are closed.
func splice(a net.Conn, ar io.Reader, b net.Conn, br io.Reader) {
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(b, ar); done <- struct{}{} }()
	go func() { _, _ = io.Copy(a, br); done <- struct{}{} }()
	<-done
	a.Close()
	b.Close()
	<-done
}

func (s *Server) logf(f string, a ...any) {
	if s.Log != nil {
		s.Log.Printf(f, a...)
	}
}
