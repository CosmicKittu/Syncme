package daemon

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// sysClipboard is the desktop clipboard. Backends:
//   - Wayland (KDE Plasma, GNOME, wlroots): wl-clipboard (wl-paste / wl-copy)
//   - X11: xclip
//
// Changes are detected with `wl-paste --watch` and, on KDE, Klipper's
// clipboardHistoryUpdated D-Bus signal (both may fire; duplicates are
// ignored by hash). Clips written by us land in KDE's clipboard history
// like any other copy.
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

// ---- Wayland ----

type wlClipboard struct{}

func (wlClipboard) Name() string { return "Wayland (wl-clipboard)" }

func (wlClipboard) Read(from string) (*Clip, error) {
	out, err := run(context.Background(), nil, "wl-paste", "--list-types")
	if err != nil {
		if strings.Contains(err.Error(), "Nothing is copied") || strings.Contains(err.Error(), "No selection") {
			return nil, nil
		}
		return nil, err
	}
	return readByTypes(strings.Split(strings.TrimSpace(string(out)), "\n"), from, func(mime string) ([]byte, error) {
		return run(context.Background(), nil, "wl-paste", "--no-newline", "--type", mime)
	})
}

func (wlClipboard) Write(c *Clip) error {
	if c.Kind == "text" {
		return runDetached([]byte(c.Text), "wl-copy") // offers all common text types
	}
	return runDetached(c.Data, "wl-copy", "--type", c.Mime)
}

func (wlClipboard) Watch(ctx context.Context, changed func()) error {
	errs := make(chan error, 2)
	watchers := 0
	if _, err := exec.LookPath("dbus-monitor"); err == nil {
		watchers++
		go func() {
			errs <- watchLines(ctx, changed, "dbus-monitor", "--session", "type='signal',interface='org.kde.klipper.klipper',member='clipboardHistoryUpdated'")
		}()
	}
	watchers++
	go func() { errs <- watchLines(ctx, changed, "wl-paste", "--watch", "echo", "changed") }()
	// Keep going while at least one watcher works.
	var last error
	for i := 0; i < watchers; i++ {
		if last = <-errs; ctx.Err() != nil {
			return nil
		}
	}
	return last
}

func watchLines(ctx context.Context, changed func(), name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		line := sc.Text()
		if name == "dbus-monitor" && !strings.Contains(line, "clipboardHistoryUpdated") {
			continue
		}
		changed()
	}
	err = cmd.Wait()
	return fmt.Errorf("%s stopped: %v %s", name, err, strings.TrimSpace(errb.String()))
}

// ---- X11 ----

type xClipboard struct{}

func (xClipboard) Name() string { return "X11 (xclip)" }

func (xClipboard) Read(from string) (*Clip, error) {
	out, err := run(context.Background(), nil, "xclip", "-selection", "clipboard", "-o", "-t", "TARGETS")
	if err != nil {
		return nil, nil // usually: nothing copied
	}
	return readByTypes(strings.Split(strings.TrimSpace(string(out)), "\n"), from, func(mime string) ([]byte, error) {
		return run(context.Background(), nil, "xclip", "-selection", "clipboard", "-o", "-t", mime)
	})
}

func (xClipboard) Write(c *Clip) error {
	mime := c.Mime
	if c.Kind == "text" {
		mime = "UTF8_STRING"
	}
	return runDetached(c.payload(), "xclip", "-selection", "clipboard", "-t", mime, "-i")
}

func (x xClipboard) Watch(ctx context.Context, changed func()) error {
	// X11 has no simple change notification for scripts: poll a cheap fingerprint.
	prev := ""
	t := time.NewTicker(1500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		targets, _ := run(ctx, nil, "xclip", "-selection", "clipboard", "-o", "-t", "TARGETS")
		text, _ := run(ctx, nil, "xclip", "-selection", "clipboard", "-o", "-t", "UTF8_STRING")
		sum := sha256.Sum256(append(targets, text...))
		fp := string(sum[:])
		if prev != "" && fp != prev {
			changed()
		}
		prev = fp
	}
}

// ---- detection ----

// detectClipboard picks a backend, or explains why there is none.
func detectClipboard() (sysClipboard, string) {
	wayland := os.Getenv("WAYLAND_DISPLAY")
	if wayland == "" {
		// systemd may start us before Plasma exports its environment.
		if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
			if m, _ := filepath.Glob(filepath.Join(rt, "wayland-[0-9]")); len(m) > 0 {
				wayland = filepath.Base(m[0])
				os.Setenv("WAYLAND_DISPLAY", wayland)
			}
		}
	}
	if wayland != "" {
		if _, err := exec.LookPath("wl-paste"); err != nil {
			return nil, "install wl-clipboard to share the clipboard (Arch: sudo pacman -S wl-clipboard)"
		}
		return wlClipboard{}, ""
	}
	if os.Getenv("DISPLAY") != "" {
		if _, err := exec.LookPath("xclip"); err != nil {
			return nil, "install xclip to share the clipboard (Arch: sudo pacman -S xclip)"
		}
		return xClipboard{}, ""
	}
	return nil, "no graphical session found (the PC clipboard is not available; devices still share clips with each other)"
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
