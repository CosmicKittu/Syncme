package daemon

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

// Runs only inside a Wayland session (CI/dev: a headless sway).
func TestWaylandClipboard(t *testing.T) {
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		t.Skip("no Wayland session")
	}
	sys, why := detectClipboard()
	if sys == nil {
		t.Skip(why)
	}
	// text round trip
	c, _ := newClip("text", "text/plain", []byte("héllo wayland"), false, "test")
	if err := sys.Write(c); err != nil {
		t.Fatal(err)
	}
	got, err := sys.Read("pc")
	if err != nil || got == nil || got.Text != "héllo wayland" {
		t.Fatalf("text read back: %+v %v", got, err)
	}
	// image round trip
	png := []byte("\x89PNG\r\n\x1a\nfake-image-bytes\x00\x01\x02\n\n")
	img, _ := newClip("image", "image/png", png, false, "test")
	if err := sys.Write(img); err != nil {
		t.Fatal(err)
	}
	got, err = sys.Read("pc")
	if err != nil || got == nil || got.Kind != "image" || !bytes.Equal(got.Data, png) {
		t.Fatalf("image read back: %+v %v", got, err)
	}
	// change notification from another app
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	changed := make(chan struct{}, 10)
	go sys.Watch(ctx, func() { changed <- struct{}{} })
	time.Sleep(500 * time.Millisecond)
	for len(changed) > 0 {
		<-changed
	}
	cmd := exec.Command("wl-copy", "from another app")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	case <-time.After(5 * time.Second):
		t.Fatal("watch did not notice the copy")
	}
	got, _ = sys.Read("pc")
	if got == nil || got.Text != "from another app" {
		t.Fatalf("after external copy: %+v", got)
	}
	// copied files are not shared as text
	cmd = exec.Command("wl-copy", "--type", "text/uri-list", "file:///etc/hosts")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if got, _ := sys.Read("pc"); got != nil {
		t.Fatalf("file copy should be ignored, got %+v", got)
	}
}
