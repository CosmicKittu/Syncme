//go:build linux

package daemon

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Runs only with an X server (CI/dev: Xvfb) and xclip.
func TestX11Clipboard(t *testing.T) {
	if os.Getenv("DISPLAY") == "" {
		t.Skip("no X display")
	}
	if _, err := exec.LookPath("xclip"); err != nil {
		t.Skip("xclip not installed")
	}
	clipboardRoundTrip(t, xClipboard{}, func(s string) error {
		cmd := exec.Command("xclip", "-selection", "clipboard", "-i")
		cmd.Stdin = strings.NewReader(s)
		return cmd.Run()
	})
}
