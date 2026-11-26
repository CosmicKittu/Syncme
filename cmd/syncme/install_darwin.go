package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// macOS: a LaunchAgent starts syncme at login and restarts it if it stops.

const startHint = "launchctl load -w ~/Library/LaunchAgents/com.cosmickittu.syncme.plist"

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
	<key>ProcessType</key><string>Background</string>
</dict>
</plist>
`
	_ = os.MkdirAll(filepath.Dir(agentPath()), 0o755)
	_ = os.MkdirAll(filepath.Dir(logPath()), 0o755)
	if err := os.WriteFile(agentPath(), []byte(plist), 0o644); err != nil {
		return err
	}
	fmt.Println("wrote", agentPath())
	_ = exec.Command("launchctl", "unload", agentPath()).Run()
	if out, err := exec.Command("launchctl", "load", "-w", agentPath()).CombinedOutput(); err != nil {
		fmt.Printf("could not start syncme automatically: %s\nstart it with: syncme daemon\n", strings.TrimSpace(string(out)))
	} else {
		fmt.Println("started syncme (it will also start at every login); log:", logPath())
	}
	fmt.Println("\nmacOS may ask to allow incoming connections and local network access for \"syncme\": click Allow.")
	fmt.Println("Next: run `syncme pair` and scan the QR code with the Syncme Android app.")
	return nil
}

func uninstall() error {
	_ = exec.Command("launchctl", "unload", "-w", agentPath()).Run()
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
