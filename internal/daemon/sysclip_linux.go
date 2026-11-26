//go:build !darwin && !windows

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
	// GNOME does not let background apps watch the Wayland clipboard, but it
	// mirrors it to Xwayland, where xclip can poll it.
	if wayland != "" && strings.Contains(strings.ToUpper(os.Getenv("XDG_CURRENT_DESKTOP")), "GNOME") {
		if ensureX11Env() {
			if _, err := exec.LookPath("xclip"); err == nil {
				return xClipboard{}, ""
			}
			return nil, "install xclip to share the clipboard on GNOME (Ubuntu/Debian: sudo apt install xclip)"
		}
	}
	if wayland != "" {
		if _, err := exec.LookPath("wl-paste"); err != nil {
			return nil, "install wl-clipboard to share the clipboard (Arch: sudo pacman -S wl-clipboard, Ubuntu: sudo apt install wl-clipboard)"
		}
		return wlClipboard{}, ""
	}
	if ensureX11Env() {
		if _, err := exec.LookPath("xclip"); err != nil {
			return nil, "install xclip to share the clipboard (Arch: sudo pacman -S xclip, Ubuntu: sudo apt install xclip)"
		}
		return xClipboard{}, ""
	}
	return nil, "no graphical session found (the PC clipboard is not available; devices still share clips with each other)"
}

// ensureX11Env makes sure DISPLAY (and XAUTHORITY under GNOME's Xwayland)
// are set; systemd can start us before the session exports them.
func ensureX11Env() bool {
	if os.Getenv("DISPLAY") == "" {
		if _, err := os.Stat("/tmp/.X11-unix/X0"); err != nil {
			return false
		}
		os.Setenv("DISPLAY", ":0")
	}
	if os.Getenv("XAUTHORITY") == "" {
		if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
			if m, _ := filepath.Glob(filepath.Join(rt, ".mutter-Xwaylandauth.*")); len(m) > 0 {
				os.Setenv("XAUTHORITY", m[0])
			}
		}
	}
	return true
}
