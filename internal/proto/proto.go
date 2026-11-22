// Package proto implements the Syncme wire protocol framing.
//
// Every frame is: 1 byte type | 4 bytes big-endian length | payload.
// Type 'J' carries one JSON message, type 'D' carries a chunk of file data.
// A file body is a sequence of 'D' frames terminated by an empty 'D' frame.
// See docs/PROTOCOL.md for the message flow.
package proto

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

const (
	Version       = 1
	DefaultPort   = 47810
	DiscoveryPort = 47811
	RelayPort     = 47812

	frameJSON byte = 'J'
	frameData byte = 'D'

	maxJSON   = 64 << 20
	maxChunk  = 1 << 20
	chunkSize = 256 << 10
)

// FileInfo describes one file in a folder listing.
type FileInfo struct {
	Path  string `json:"p"`
	Size  int64  `json:"s"`
	MTime int64  `json:"m"` // unix milliseconds
	Hash  string `json:"h"` // lowercase hex sha256
}

// Stats summarises one sync session.
type Stats struct {
	Pulled    int `json:"pulled"`
	Pushed    int `json:"pushed"`
	DelLocal  int `json:"del_local"`
	DelRemote int `json:"del_remote"`
	Conflicts int `json:"conflicts"`
	Errors    int `json:"errors"`
}

// Msg is the single JSON message type; T selects the meaning.
type Msg struct {
	T      string     `json:"t"`
	V      int        `json:"v,omitempty"`
	ID     string     `json:"id,omitempty"`
	Name   string     `json:"name,omitempty"`
	Nonce  string     `json:"nonce,omitempty"`
	MAC    string     `json:"mac,omitempty"`
	Token  string     `json:"token,omitempty"`
	Code   string     `json:"code,omitempty"`
	Secret string     `json:"secret,omitempty"`
	Error  string     `json:"error,omitempty"`
	Reason string     `json:"reason,omitempty"`
	Addrs  []string   `json:"addrs,omitempty"`
	Public string     `json:"public,omitempty"`
	Relay  string     `json:"relay,omitempty"`
	Files  []FileInfo `json:"files,omitempty"`
	Path   string     `json:"p,omitempty"`
	To     string     `json:"to,omitempty"`
	Size   int64      `json:"s,omitempty"`
	MTime  int64      `json:"m,omitempty"`
	Hash   string     `json:"h,omitempty"`
	Old    string     `json:"old,omitempty"`
	Stats  *Stats     `json:"stats,omitempty"`
}

// Conn wraps a stream (normally TLS) with frame reading and writing.
type Conn struct {
	raw net.Conn
	r   *bufio.Reader
	w   *bufio.Writer
	wmu sync.Mutex
}

func NewConn(c net.Conn) *Conn {
	return &Conn{raw: c, r: bufio.NewReaderSize(c, 64<<10), w: bufio.NewWriterSize(c, 64<<10)}
}

func (c *Conn) Close() error         { return c.raw.Close() }
func (c *Conn) RemoteAddr() net.Addr { return c.raw.RemoteAddr() }
func (c *Conn) SetReadTimeout(d time.Duration) {
	if d == 0 {
		_ = c.raw.SetReadDeadline(time.Time{})
		return
	}
	_ = c.raw.SetReadDeadline(time.Now().Add(d))
}

func (c *Conn) writeFrame(t byte, p []byte) error {
	var hdr [5]byte
	hdr[0] = t
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(p)))
	if _, err := c.w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := c.w.Write(p)
	return err
}

func (c *Conn) readFrame(max int) (byte, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(c.r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if int64(n) > int64(max) {
		return 0, nil, fmt.Errorf("frame too large: %d", n)
	}
	p := make([]byte, n)
	if _, err := io.ReadFull(c.r, p); err != nil {
		return 0, nil, err
	}
	return hdr[0], p, nil
}

// Send writes one JSON message and flushes.
func (c *Conn) Send(m *Msg) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if err := c.writeFrame(frameJSON, b); err != nil {
		return err
	}
	return c.w.Flush()
}

// Recv reads one JSON message.
func (c *Conn) Recv() (*Msg, error) {
	t, p, err := c.readFrame(maxJSON)
	if err != nil {
		return nil, err
	}
	if t != frameJSON {
		return nil, fmt.Errorf("expected message frame, got %q", t)
	}
	var m Msg
	if err := json.Unmarshal(p, &m); err != nil {
		return nil, fmt.Errorf("bad message: %w", err)
	}
	return &m, nil
}

// SendData streams src as data frames followed by the terminator.
// It returns the number of bytes sent and their sha256. If reading src
// fails, the terminator is still written so the stream stays in sync
// (the receiver will see a hash mismatch) and the read error is returned.
func (c *Conn) SendData(src io.Reader) (int64, string, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	h := sha256.New()
	buf := make([]byte, chunkSize)
	var total int64
	var readErr error
	for {
		n, err := src.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
			total += int64(n)
			if werr := c.writeFrame(frameData, buf[:n]); werr != nil {
				return total, "", werr
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			readErr = err
			break
		}
	}
	if err := c.writeFrame(frameData, nil); err != nil {
		return total, "", err
	}
	if err := c.w.Flush(); err != nil {
		return total, "", err
	}
	return total, hex.EncodeToString(h.Sum(nil)), readErr
}

// ErrTooLong is returned when a data stream exceeds its declared size.
var ErrTooLong = errors.New("data stream longer than declared size")

// RecvData reads data frames into dst until the terminator.
// limit is the declared size; exceeding it is a protocol error.
// A write error on dst is reported via dstErr while the stream is still
// drained, so the connection remains usable.
func (c *Conn) RecvData(dst io.Writer, limit int64) (n int64, hash string, dstErr error, err error) {
	h := sha256.New()
	for {
		t, p, rerr := c.readFrame(maxChunk)
		if rerr != nil {
			return n, "", dstErr, rerr
		}
		if t != frameData {
			return n, "", dstErr, fmt.Errorf("expected data frame, got %q", t)
		}
		if len(p) == 0 {
			return n, hex.EncodeToString(h.Sum(nil)), dstErr, nil
		}
		n += int64(len(p))
		if n > limit {
			return n, "", dstErr, ErrTooLong
		}
		h.Write(p)
		if dstErr == nil {
			if _, werr := dst.Write(p); werr != nil {
				dstErr = werr
			}
		}
	}
}
