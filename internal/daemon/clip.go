package daemon

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/CosmicKittu/Syncme/internal/proto"
)

// Clipboard sharing.
//
// The PC is the hub: a clip from any device is put on the PC clipboard
// (so it appears in KDE's clipboard history) and relayed to the other
// connected devices. PC copies are pushed to every device that has
// automatic receive enabled. Sensitive clips (password managers) are only
// ever sent on explicit request and never relayed.

const (
	maxClipText  = 1 << 20
	maxClipImage = 16 << 20
)

type Clip struct {
	ID        string
	Kind      string // "text" | "image"
	Mime      string
	Text      string
	Data      []byte
	Hash      string
	Sensitive bool
	From      string
}

func newClip(kind, mime string, data []byte, sensitive bool, from string) (*Clip, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty clip")
	}
	c := &Clip{ID: proto.RandomHex(8), Kind: kind, Mime: mime, Sensitive: sensitive, From: from}
	switch kind {
	case "text":
		if len(data) > maxClipText {
			return nil, fmt.Errorf("text too large (%d bytes)", len(data))
		}
		if !utf8.Valid(data) {
			return nil, fmt.Errorf("text is not UTF-8")
		}
		c.Text = string(data)
		if c.Mime == "" {
			c.Mime = "text/plain"
		}
	case "image":
		if len(data) > maxClipImage {
			return nil, fmt.Errorf("image too large (%d bytes)", len(data))
		}
		if !strings.HasPrefix(mime, "image/") {
			return nil, fmt.Errorf("bad image type %q", mime)
		}
		c.Data = data
	default:
		return nil, fmt.Errorf("unknown clip kind %q", kind)
	}
	sum := sha256.Sum256(data)
	c.Hash = hex.EncodeToString(sum[:])
	return c, nil
}

func (c *Clip) payload() []byte {
	if c.Kind == "text" {
		return []byte(c.Text)
	}
	return c.Data
}

func clipFromMsg(m *proto.Msg, from string) (*Clip, error) {
	switch m.Kind {
	case "text":
		return newClip("text", m.Mime, []byte(m.Text), m.Sens, from)
	case "image":
		if m.Data == nil {
			return nil, fmt.Errorf("image data failed verification")
		}
		return newClip("image", m.Mime, m.Data, m.Sens, from)
	}
	return nil, fmt.Errorf("unknown clip kind %q", m.Kind)
}

// recvMsg reads a message; for image clips it also reads the attached data.
// m.Data stays nil if the image's hash does not verify.
func recvMsg(c *proto.Conn) (*proto.Msg, error) {
	m, err := c.Recv()
	if err != nil || m.T != "clip" || m.Kind != "image" {
		return m, err
	}
	var buf bytes.Buffer
	_, h, _, err := c.RecvData(&buf, maxClipImage)
	if err != nil {
		return nil, err
	}
	if h == m.Hash {
		m.Data = buf.Bytes()
	}
	return m, nil
}

func sendClip(c *proto.Conn, clip *Clip, reply bool) error {
	if clip == nil {
		return c.Send(&proto.Msg{T: "clip", Kind: "none", Reply: true})
	}
	m := &proto.Msg{T: "clip", ID: clip.ID, Kind: clip.Kind, Mime: clip.Mime, Hash: clip.Hash, Size: int64(len(clip.payload())), Sens: clip.Sensitive, Reply: reply, From: clip.From}
	if clip.Kind == "text" {
		m.Text = clip.Text
		return c.Send(m)
	}
	_, _, err := c.SendWithData(m, bytes.NewReader(clip.Data))
	return err
}

// ---- per-connection peer ----

type clipOut struct {
	clip  *Clip
	reply bool
}

type clipPeer struct {
	dev  *Device
	out  chan clipOut
	mu   sync.Mutex
	auto bool
	last string // hash of the last clip exchanged with this device
}

func newClipPeer(dev *Device) *clipPeer {
	return &clipPeer{dev: dev, out: make(chan clipOut, 4), auto: true}
}

func (p *clipPeer) setAuto(v bool) {
	p.mu.Lock()
	p.auto = v
	p.mu.Unlock()
}

func (p *clipPeer) received(hash string) {
	p.mu.Lock()
	p.last = hash
	p.mu.Unlock()
}

// offer queues a clip for this device. Automatic pushes respect the
// device's setting and skip what it already has; replies always go out.
func (p *clipPeer) offer(c *Clip, reply bool) {
	p.mu.Lock()
	if !reply {
		if !p.auto || c == nil || p.last == c.Hash {
			p.mu.Unlock()
			return
		}
	}
	if c != nil {
		p.last = c.Hash
	}
	p.mu.Unlock()
	select {
	case p.out <- clipOut{c, reply}:
	default: // device is slow; drop rather than block the hub
	}
}

// ---- hub ----

type ClipHub struct {
	log     *log.Logger
	pcName  string
	sys     sysClipboard // nil when the desktop clipboard is unavailable
	mu      sync.Mutex
	peers   map[*clipPeer]struct{}
	current *Clip
	lastSet string // hash we last wrote to the PC clipboard (to ignore its echo)
}

func newClipHub(logger *log.Logger, pcName string) *ClipHub {
	return &ClipHub{log: logger, pcName: pcName, peers: map[*clipPeer]struct{}{}}
}

func (h *ClipHub) add(p *clipPeer) {
	h.mu.Lock()
	h.peers[p] = struct{}{}
	h.mu.Unlock()
}

func (h *ClipHub) remove(p *clipPeer) {
	h.mu.Lock()
	delete(h.peers, p)
	h.mu.Unlock()
}

func (h *ClipHub) snapshot() []*clipPeer {
	out := make([]*clipPeer, 0, len(h.peers))
	for p := range h.peers {
		out = append(out, p)
	}
	return out
}

// FromDevice handles a clip sent by a phone/tablet.
func (h *ClipHub) FromDevice(c *Clip, origin *clipPeer) {
	if origin != nil {
		origin.received(c.Hash)
	}
	h.mu.Lock()
	h.current = c
	sys := h.sys
	if sys != nil {
		h.lastSet = c.Hash
	}
	peers := h.snapshot()
	h.mu.Unlock()
	if sys != nil {
		if err := sys.Write(c); err != nil {
			h.log.Printf("clipboard: could not set PC clipboard: %v", err)
		}
	}
	if c.Sensitive {
		return
	}
	for _, p := range peers {
		if p != origin {
			p.offer(c, false)
		}
	}
}

// FromPC handles a change of the PC's own clipboard.
func (h *ClipHub) FromPC(c *Clip) {
	h.mu.Lock()
	if c.Hash == h.lastSet || (h.current != nil && h.current.Hash == c.Hash) {
		h.mu.Unlock()
		return // our own write echoing back, or nothing new
	}
	h.current = c
	peers := h.snapshot()
	h.mu.Unlock()
	if c.Sensitive {
		return
	}
	for _, p := range peers {
		p.offer(c, false)
	}
}

// Get returns the PC clipboard right now (for "Get PC clipboard").
func (h *ClipHub) Get() *Clip {
	h.mu.Lock()
	sys := h.sys
	cur := h.current
	h.mu.Unlock()
	if sys != nil {
		if c, err := sys.Read(h.pcName); err == nil && c != nil {
			return c
		}
	}
	return cur
}

// ---- daemon glue ----

func (d *Daemon) handleClipMsg(p *clipPeer, m *proto.Msg) {
	switch m.T {
	case "clip":
		c, err := clipFromMsg(m, p.dev.Name)
		if err != nil {
			d.Log.Printf("%s: clipboard: %v", p.dev.Name, err)
			return
		}
		d.clips.FromDevice(c, p)
	case "clipget":
		p.offer(d.clips.Get(), true)
	case "clipcfg":
		if m.Auto != nil {
			p.setAuto(*m.Auto)
		}
	}
}
