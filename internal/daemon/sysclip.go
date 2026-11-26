package daemon

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// sysClipboard is the desktop clipboard. Backends (see sysclip_<os>.go):
//   - Linux Wayland (KDE Plasma, wlroots): wl-clipboard; GNOME and X11: xclip
//   - macOS: NSPasteboard through osascript (JavaScript for Automation)
//   - Windows: the Win32 clipboard API
//
// Duplicate change notifications are harmless: clips are compared by hash.
type sysClipboard interface {
	Name() string
	Read(from string) (*Clip, error) // nil, nil when empty or not shareable (e.g. copied files)
	Write(c *Clip) error
	Watch(ctx context.Context, changed func()) error // blocks until ctx ends or watching fails
}

// chooseClipType picks what to share from the offered MIME types.
func chooseClipType(types []string) (kind, mime string, sensitive bool) {
	set := map[string]bool{}
	for _, t := range types {
		set[strings.TrimSpace(t)] = true
	}
	sensitive = set["x-kde-passwordManagerHint"]
	if set["text/uri-list"] || set["x-special/gnome-copied-files"] || set["application/x-kde-cutselection"] {
		return "", "", sensitive // copied files: use file sync instead
	}
	for _, t := range []string{"image/png", "image/jpeg", "image/webp", "image/gif", "image/bmp"} {
		if set[t] {
			return "image", t, sensitive
		}
	}
	for _, t := range []string{"text/plain;charset=utf-8", "text/plain;charset=UTF-8", "text/plain", "UTF8_STRING", "STRING", "TEXT"} {
		if set[t] {
			return "text", t, sensitive
		}
	}
	return "", "", sensitive
}

func run(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %v %s", name, err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

// runDetached is for wl-copy/xclip, which fork a child that keeps serving
// the clipboard: its stdout/stderr must not be pipes we wait on.
func runDetached(stdin []byte, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	return cmd.Run()
}

func readByTypes(types []string, from string, read func(mime string) ([]byte, error)) (*Clip, error) {
	kind, mime, sens := chooseClipType(types)
	if kind == "" {
		return nil, nil
	}
	data, err := read(mime)
	if err != nil {
		return nil, err
	}
	if kind == "text" && len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	if kind == "text" {
		mime = "text/plain"
	}
	return newClip(kind, mime, data, sens, from)
}

// clipLoop finds the desktop clipboard and feeds its changes to the hub,
// retrying while the graphical session is not up yet.
func (d *Daemon) clipLoop(ctx context.Context) {
	logged := ""
	for ctx.Err() == nil {
		sys, why := detectClipboard()
		if sys == nil {
			d.setClipStatus("off: " + why)
			if why != logged {
				d.Log.Printf("clipboard: %s", why)
				logged = why
			}
		} else {
			d.clips.mu.Lock()
			d.clips.sys = sys
			d.clips.mu.Unlock()
			d.setClipStatus("on: " + sys.Name())
			d.Log.Printf("clipboard: using %s", sys.Name())
			err := sys.Watch(ctx, func() {
				if c, err := sys.Read(d.Cfg.Name); err == nil && c != nil {
					d.clips.FromPC(c)
				}
			})
			d.clips.mu.Lock()
			d.clips.sys = nil
			d.clips.mu.Unlock()
			if ctx.Err() != nil {
				return
			}
			d.setClipStatus(fmt.Sprintf("retrying: %v", err))
			d.Log.Printf("clipboard: %v (retrying)", err)
			logged = ""
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(15 * time.Second):
		}
	}
}

func (d *Daemon) setClipStatus(s string) { d.clipStatus.Store(s) }

func (d *Daemon) ClipStatus() string {
	s, _ := d.clipStatus.Load().(string)
	if s == "" {
		return "off"
	}
	return s
}
