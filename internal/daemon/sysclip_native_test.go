//go:build darwin || windows

package daemon

import "testing"

// The real macOS / Windows clipboard (run on CI's macOS and Windows machines).
func TestNativeClipboard(t *testing.T) {
	sys, why := detectClipboard()
	if sys == nil {
		t.Skip(why)
	}
	clipboardRoundTrip(t, sys, func(s string) error {
		c, _ := newClip("text", "text/plain", []byte(s), false, "other")
		return sys.Write(c)
	})
}
