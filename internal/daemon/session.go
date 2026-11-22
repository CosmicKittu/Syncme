package daemon

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/CosmicKittu/Syncme/internal/index"
	"github.com/CosmicKittu/Syncme/internal/proto"
	"github.com/CosmicKittu/Syncme/internal/syncer"
)

func encodeSecret(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// opError is a failure of a single file operation; the session continues.
type opError struct{ msg string }

func (e *opError) Error() string     { return e.msg }
func opErr(f string, a ...any) error { return &opError{fmt.Sprintf(f, a...)} }

type session struct {
	d    *Daemon
	c    *proto.Conn
	dev  *Device
	root string
	base map[string]index.Entry
	st   proto.Stats
}

func (d *Daemon) runSession(c *proto.Conn, dev *Device, files []proto.FileInfo) error {
	d.syncMu.Lock()
	defer d.syncMu.Unlock()

	remote := make(map[string]index.Entry, len(files))
	for _, f := range files {
		remote[f.Path] = index.Entry{Size: f.Size, MTime: f.MTime, Hash: f.Hash}
	}
	if bad := syncer.ValidateRemote(remote); len(bad) > 0 {
		d.Log.Printf("%s: ignoring %d invalid paths", dev.Name, len(bad))
	}
	local, err := index.Scan(d.Cfg.Folder, d.cache)
	if err != nil {
		_ = c.Send(&proto.Msg{T: "done", Error: "PC folder unreadable"})
		return fmt.Errorf("scan: %w", err)
	}
	s := &session{d: d, c: c, dev: dev, root: d.Cfg.Folder, base: loadBase(d.Paths.DataDir, dev.ID)}
	actions := syncer.Plan(s.base, local, remote, time.Now(), d.Cfg.Name, dev.Name)

	lastSave := time.Now()
	for _, a := range actions {
		err := s.apply(a)
		var oe *opError
		if errors.As(err, &oe) {
			s.st.Errors++
			d.Log.Printf("%s: %s %s: %v", dev.Name, a.Kind, a.Path, err)
		} else if err != nil {
			_ = saveBase(d.Paths.DataDir, dev.ID, s.base)
			return err // connection problem: abort, progress so far is kept
		}
		if time.Since(lastSave) > 5*time.Second {
			_ = saveBase(d.Paths.DataDir, dev.ID, s.base)
			lastSave = time.Now()
		}
	}
	if err := saveBase(d.Paths.DataDir, dev.ID, s.base); err != nil {
		return err
	}
	_ = d.cache.Save()
	if n := s.st.Pulled + s.st.Pushed + s.st.DelLocal + s.st.DelRemote; n > 0 || s.st.Errors > 0 {
		d.Log.Printf("%s: synced (in %d, out %d, deleted here %d, deleted there %d, conflicts %d, errors %d)",
			dev.Name, s.st.Pulled, s.st.Pushed, s.st.DelLocal, s.st.DelRemote, s.st.Conflicts, s.st.Errors)
	}
	st := s.st
	return c.Send(&proto.Msg{T: "done", Stats: &st})
}

func (s *session) apply(a syncer.Action) error {
	switch a.Kind {
	case syncer.BaseSet:
		s.base[a.Path] = *a.L
	case syncer.BaseRemove:
		delete(s.base, a.Path)
	case syncer.Pull:
		return s.pull(a.Path, a.L)
	case syncer.Push:
		return s.push(a.Path, a.Path, *a.L, hashOf(a.R))
	case syncer.DelLocal:
		if err := s.delLocal(a.Path, *a.L); err != nil {
			return err
		}
		delete(s.base, a.Path)
		s.st.DelLocal++
	case syncer.DelRemote:
		if err := s.request(&proto.Msg{T: "del", Path: a.Path, Hash: a.R.Hash}); err != nil {
			return err
		}
		delete(s.base, a.Path)
		s.st.DelRemote++
	case syncer.ConflictKeepRemote:
		s.st.Conflicts++
		if err := s.mvLocal(a.Path, a.ConflictName, *a.L); err != nil {
			return err
		}
		if err := s.pull(a.Path, nil); err != nil {
			return err
		}
		return s.push(a.ConflictName, a.ConflictName, *a.L, "")
	case syncer.ConflictKeepLocal:
		s.st.Conflicts++
		if err := s.request(&proto.Msg{T: "mv", Path: a.Path, To: a.ConflictName, Hash: a.R.Hash}); err != nil {
			return err
		}
		if err := s.push(a.Path, a.Path, *a.L, ""); err != nil {
			return err
		}
		return s.pull(a.ConflictName, nil)
	}
	return nil
}

func hashOf(e *index.Entry) string {
	if e == nil {
		return ""
	}
	return e.Hash
}

// request sends a command and expects "ok"; "error" becomes an opError.
func (s *session) request(m *proto.Msg) error {
	if err := s.c.Send(m); err != nil {
		return err
	}
	return s.expectOK()
}

// recv reads the phone's reply, skipping pokes that raced with a wake.
func (s *session) recv() (*proto.Msg, error) {
	for {
		s.c.SetReadTimeout(5 * time.Minute)
		m, err := s.c.Recv()
		if err != nil || (m.T != "poke" && m.T != "ping") {
			return m, err
		}
	}
}

func (s *session) expectOK() error {
	r, err := s.recv()
	if err != nil {
		return err
	}
	switch r.T {
	case "ok":
		return nil
	case "error":
		return opErr("phone: %s", r.Error)
	}
	return fmt.Errorf("unexpected reply %q", r.T)
}

// unchanged checks that a local file still matches what the scan saw.
// want == nil means the file must not exist.
func unchanged(abs string, want *index.Entry) bool {
	info, err := os.Lstat(abs)
	if want == nil {
		return errors.Is(err, os.ErrNotExist)
	}
	return err == nil && info.Mode().IsRegular() && info.Size() == want.Size && info.ModTime().UnixMilli() == want.MTime
}

// pull downloads p from the phone. cur is the expected current local state.
func (s *session) pull(p string, cur *index.Entry) error {
	if err := s.c.Send(&proto.Msg{T: "get", Path: p}); err != nil {
		return err
	}
	r, err := s.recv()
	if err != nil {
		return err
	}
	if r.T == "error" {
		return opErr("phone: %s", r.Error)
	}
	if r.T != "file" || r.Path != p || r.Size < 0 {
		return fmt.Errorf("unexpected reply %q to get", r.T)
	}
	abs := index.Abs(s.root, p)
	dir := filepath.Dir(abs)
	var f *os.File
	var ferr error
	if ferr = os.MkdirAll(dir, 0o755); ferr == nil {
		f, ferr = os.CreateTemp(dir, ".syncme-tmp-*")
	}
	var dst io.Writer = io.Discard
	if ferr == nil {
		dst = f
	}
	s.c.SetReadTimeout(2 * time.Minute) // per frame
	_, h, werr, err := s.c.RecvData(dst, r.Size)
	if f != nil {
		if werr == nil {
			werr = f.Sync()
		}
		f.Close()
		defer os.Remove(f.Name()) // no-op after a successful rename
	}
	if err != nil {
		return err
	}
	if ferr != nil {
		return opErr("create temp file: %v", ferr)
	}
	if werr != nil {
		return opErr("write: %v", werr)
	}
	if h != r.Hash {
		return opErr("hash mismatch (file changed during transfer)")
	}
	mt := time.UnixMilli(r.MTime)
	if err := os.Chtimes(f.Name(), mt, mt); err != nil {
		return opErr("chtimes: %v", err)
	}
	if !unchanged(abs, cur) {
		return opErr("changed locally during sync")
	}
	if err := os.Rename(f.Name(), abs); err != nil {
		return opErr("rename: %v", err)
	}
	if info, err := os.Stat(abs); err == nil {
		s.d.cache.Put(p, info.Size(), info.ModTime().UnixNano(), h)
	}
	s.base[p] = index.Entry{Size: r.Size, MTime: r.MTime, Hash: h}
	s.st.Pulled++
	return nil
}

// push uploads local file src to phone path dst. old is the hash the phone
// is expected to currently have at dst ("" = nothing).
func (s *session) push(src, dst string, l index.Entry, old string) error {
	abs := index.Abs(s.root, src)
	f, err := os.Open(abs)
	if err != nil {
		return opErr("open: %v", err)
	}
	defer f.Close()
	if !unchanged(abs, &l) {
		return opErr("changed locally during sync")
	}
	if err := s.c.Send(&proto.Msg{T: "put", Path: dst, Size: l.Size, MTime: l.MTime, Hash: l.Hash, Old: old}); err != nil {
		return err
	}
	if _, _, err := s.c.SendData(io.LimitReader(f, l.Size)); err != nil {
		var pe *os.PathError
		if !errors.As(err, &pe) {
			return err
		}
		// local read error: the phone will reject the hash; read its reply
	}
	if err := s.expectOK(); err != nil {
		return err
	}
	s.base[dst] = l
	s.st.Pushed++
	return nil
}

func (s *session) delLocal(p string, l index.Entry) error {
	abs := index.Abs(s.root, p)
	if !unchanged(abs, &l) {
		return opErr("changed locally during sync")
	}
	if err := moveToTrash(s.root, p); err != nil {
		return opErr("trash: %v", err)
	}
	pruneEmptyDirs(s.root, filepath.Dir(abs))
	return nil
}

func (s *session) mvLocal(from, to string, l index.Entry) error {
	a, b := index.Abs(s.root, from), index.Abs(s.root, to)
	if !unchanged(a, &l) || !unchanged(b, nil) {
		return opErr("changed locally during sync")
	}
	if err := os.Rename(a, b); err != nil {
		return opErr("rename: %v", err)
	}
	delete(s.base, from)
	return nil
}
