package index

import "testing"

func TestWindowsNameOK(t *testing.T) {
	for _, n := range []string{"a.txt", "photo 2026.jpg", "ünïcode", ".hidden", "CONSOLE.txt", "COM10"} {
		if !windowsNameOK(n) {
			t.Errorf("%q should be allowed", n)
		}
	}
	for _, n := range []string{"a:b", "what?", "x*", "trail.", "trail ", "CON", "nul.txt", "Com1.log", "LPT9", "tab\tname"} {
		if windowsNameOK(n) {
			t.Errorf("%q should be rejected", n)
		}
	}
}
