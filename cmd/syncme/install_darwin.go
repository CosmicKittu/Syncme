package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// macOS: a LaunchAgent starts syncme at login and restarts it if it stops.

const startHint = "syncme install"

const agentLabel = "com.cosmickittu.syncme"

func agentPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", agentLabel+".plist")
}

func logPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Logs", "syncme.log")
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func install() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.EvalSymlinks(exe)
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>` + agentLabel + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + xmlEscape(exe) + `</string>
		<string>daemon</string>
		<string>--log</string>
		<string>` + xmlEscape(logPath()) + `</string>
	</array>
	<key>RunAtLoad</key><true/>
	<key>KeepAlive</key><true/>
	<key>ProcessType</key><string>Interactive</string>
</dict>
</plist>
`
	_ = os.MkdirAll(filepath.Dir(agentPath()), 0o755)
	_ = os.MkdirAll(filepath.Dir(logPath()), 0o755)
	if err := os.WriteFile(agentPath(), []byte(plist), 0o644); err != nil {
		return err
	}
	fmt.Println("wrote", agentPath())
	stopAgent()
	// Start inside the logged-in desktop session (gui/<uid>): only there may
	// it use the clipboard. Fall back to the legacy command (e.g. over SSH).
	gui := fmt.Sprintf("gui/%d", os.Getuid())
	out, err := exec.Command("launchctl", "bootstrap", gui, agentPath()).CombinedOutput()
	if err != nil {
		out, err = exec.Command("launchctl", "load", "-w", agentPath()).CombinedOutput()
	}
	if err != nil {
		fmt.Printf("could not start syncme automatically: %s\nstart it with: syncme daemon\n", strings.TrimSpace(string(out)))
	} else {
		fmt.Println("started syncme (it will also start at every login); log:", logPath())
	}
	fmt.Println("\nmacOS may ask to allow incoming connections and local network access for \"syncme\": click Allow.")
	fmt.Println("Next: run `syncme pair` and scan the QR code with the Syncme Android app.")
	return nil
}

// stopAgent stops a running agent, whichever way it was started.
func stopAgent() {
	_ = exec.Command("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), agentLabel)).Run()
	_ = exec.Command("launchctl", "unload", agentPath()).Run()
}

func uninstall() error {
	stopAgent()
	if os.Remove(agentPath()) == nil {
		fmt.Println("removed", agentPath())
	}
	fmt.Println("Your synced files and pairing keys were kept (~/syncme, ~/Library/Application Support/syncme).")
	return nil
}

func desktopNotify(title, body string) {
	q := func(s string) string { return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"` }
	_ = exec.Command("osascript", "-e", "display notification "+q(body)+" with title "+q(title)).Run()
}

func firewallAdvice() []string {
	return []string{
		"  i macOS firewall: if it is on, allow incoming connections for \"syncme\"",
		"      (System Settings → Network → Firewall → Options).",
		"  i macOS 15+: allow \"syncme\" under System Settings → Privacy & Security → Local Network.",
	}
}
