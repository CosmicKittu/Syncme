package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Windows: a hidden launcher in the Startup folder runs syncme at login, and
// a "Phone (Syncme)" entry in Explorer's right-click "Send to" menu sends
// files. Both are small VBScript files so no console window appears.

const startHint = `wscript "%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup\Syncme.vbs"`

func appData() string {
	if v := os.Getenv("APPDATA"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "AppData", "Roaming")
}

func startupVBS() string {
	return filepath.Join(appData(), "Microsoft", "Windows", "Start Menu", "Programs", "Startup", "Syncme.vbs")
}

func sendToVBS() string {
	return filepath.Join(appData(), "Microsoft", "Windows", "SendTo", "Phone (Syncme).vbs")
}

func logPath() string {
	return filepath.Join(appData(), "syncme", "syncme.log")
}

// vbsQuote quotes a string for VBScript, doubling embedded quotes.
func vbsQuote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func install() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.Abs(exe)
	// The command line VBScript runs: "C:\...\syncme.exe" daemon --log "...log"
	cmdline := `"` + exe + `" daemon --log "` + logPath() + `"`
	startup := "' Syncme: start file/clipboard sync with your phone at login (hidden)\r\n" +
		"CreateObject(\"WScript.Shell\").Run " + vbsQuote(cmdline) + ", 0, False\r\n"
	sendto := "' Syncme: Explorer > right-click > Send to > Phone (Syncme)\r\n" +
		"Dim cmd, a\r\n" +
		"cmd = " + vbsQuote(`"`+exe+`" send --notify`) + "\r\n" +
		"For Each a In WScript.Arguments\r\n" +
		"  cmd = cmd & \" \"\"\" & a & \"\"\"\"\r\n" +
		"Next\r\n" +
		"CreateObject(\"WScript.Shell\").Run cmd, 0, False\r\n"
	for path, body := range map[string]string{startupVBS(): startup, sendToVBS(): sendto} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return err
		}
		fmt.Println("wrote", path)
	}
	_ = os.MkdirAll(filepath.Dir(logPath()), 0o755)
	stopOthers()
	if err := exec.Command("wscript.exe", startupVBS()).Start(); err != nil {
		fmt.Println("could not start syncme now:", err, "— it will start at next login, or run `syncme daemon`")
	} else {
		fmt.Println("started syncme in the background (it also starts at every login); log:", logPath())
	}
	fmt.Println("\nIf Windows Firewall asks, allow syncme on Private networks, and make sure your Wi-Fi")
	fmt.Println("is set to \"Private\" (Settings > Network & internet > Wi-Fi > your network).")
	fmt.Println("Right-click any file > Send to > Phone (Syncme) to send it. (Windows 11: Show more options.)")
	fmt.Println("Next: run `syncme pair` and scan the QR code with the Syncme Android app.")
	return nil
}

// stopOthers ends running syncme processes other than this one.
func stopOthers() {
	_ = exec.Command("taskkill", "/F", "/IM", "syncme.exe", "/FI", "PID ne "+strconv.Itoa(os.Getpid())).Run()
}

func uninstall() error {
	stopOthers()
	for _, p := range []string{startupVBS(), sendToVBS()} {
		if os.Remove(p) == nil {
			fmt.Println("removed", p)
		}
	}
	fmt.Print("Your synced files and pairing keys were kept (" + os.Getenv("USERPROFILE") + `\syncme, ` + appData() + `\syncme).` + "\n")
	return nil
}

func desktopNotify(title, body string) {
	esc := func(s string) string { return strings.ReplaceAll(s, "'", "''") }
	ps := "Add-Type -AssemblyName System.Windows.Forms; $n = New-Object System.Windows.Forms.NotifyIcon; " +
		"$n.Icon = [System.Drawing.SystemIcons]::Information; $n.Visible = $true; " +
		"$n.ShowBalloonTip(4000, '" + esc(title) + "', '" + esc(body) + "', 'Info'); Start-Sleep 5; $n.Dispose()"
	_ = exec.Command("powershell", "-NoProfile", "-WindowStyle", "Hidden", "-Command", ps).Run()
}

func firewallAdvice() []string {
	return []string{
		"  i Windows Firewall: syncme must be allowed on Private networks, and your Wi-Fi must be",
		"      set to Private. To add the rule (run in an Administrator PowerShell):",
		`      netsh advfirewall firewall add rule name="Syncme" dir=in action=allow program="` + exePath() + `" profile=private enable=yes`,
	}
}

func exePath() string {
	exe, _ := os.Executable()
	return exe
}
