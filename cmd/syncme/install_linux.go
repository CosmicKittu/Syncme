//go:build !darwin && !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// install writes a systemd user unit and "Send to phone" entries for the
// common Linux file managers (Nautilus/GNOME Files, Nemo, Caja, Dolphin,
// Thunar) plus a desktop entry that shows up under "Open With".

type integrationFile struct {
	path    string
	content string
	mode    os.FileMode
}

func integrationFiles() ([]integrationFile, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	exe, _ = filepath.EvalSymlinks(exe)
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(home, ".config")
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(home, ".local", "share")
	}
	q := shellQuote(exe)
	script := "#!/bin/sh\n# Syncme: send selected files to your phone\nexec " + q + " send --notify \"$@\"\n"
	return []integrationFile{
		{filepath.Join(cfg, "systemd", "user", "syncme.service"), `[Unit]
Description=Syncme folder sync with phone
After=network-online.target

[Service]
ExecStart=` + exe + ` daemon
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`, 0o644},
		{filepath.Join(data, "nautilus", "scripts", "Send to Phone (Syncme)"), script, 0o755},
		{filepath.Join(cfg, "caja", "scripts", "Send to Phone (Syncme)"), script, 0o755},
		{filepath.Join(data, "nemo", "actions", "syncme.nemo_action"), `[Nemo Action]
Name=Send to Phone (Syncme)
Comment=Copy to ~/syncme and sync to your phone
Exec=` + exe + ` send --notify %F
Icon-Name=phone
Selection=notnone
Extensions=any;
`, 0o644},
		{filepath.Join(data, "kio", "servicemenus", "syncme.desktop"), `[Desktop Entry]
Type=Service
MimeType=all/all;
Actions=sendToPhone
X-KDE-Priority=TopLevel

[Desktop Action sendToPhone]
Name=Send to Phone (Syncme)
Icon=phone
Exec=` + exe + ` send --notify %F
`, 0o755},
		{filepath.Join(data, "applications", "syncme-send.desktop"), `[Desktop Entry]
Type=Application
Name=Send to Phone (Syncme)
Comment=Copy to ~/syncme and sync to your phone
Exec=` + exe + ` send --notify %F
Icon=phone
Terminal=false
NoDisplay=true
MimeType=application/octet-stream;application/pdf;image/*;video/*;audio/*;text/*;application/zip;
`, 0o644},
	}, nil
}

const thunarAction = `<action>
	<icon>phone</icon>
	<name>Send to Phone (Syncme)</name>
	<unique-id>syncme-send-1</unique-id>
	<command>%s send --notify %%F</command>
	<description>Copy to ~/syncme and sync to your phone</description>
	<patterns>*</patterns>
	<directories/>
	<audio-files/>
	<image-files/>
	<other-files/>
	<text-files/>
	<video-files/>
</action>
`

func thunarPath() string {
	home, _ := os.UserHomeDir()
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(home, ".config")
	}
	return filepath.Join(cfg, "Thunar", "uca.xml")
}

// systemFiles maps per-user files to the copies a distro package (e.g. the
// Arch PKGBUILD) installs system-wide; when present we don't duplicate them.
var systemFiles = map[string]string{
	"syncme.service":      "/usr/lib/systemd/user/syncme.service",
	"syncme.nemo_action":  "/usr/share/nemo/actions/syncme.nemo_action",
	"syncme.desktop":      "/usr/share/kio/servicemenus/syncme.desktop",
	"syncme-send.desktop": "/usr/share/applications/syncme-send.desktop",
}

func install() error {
	files, err := integrationFiles()
	if err != nil {
		return err
	}
	for _, f := range files {
		if sys, ok := systemFiles[filepath.Base(f.path)]; ok {
			if _, err := os.Stat(sys); err == nil {
				fmt.Println("using system", sys)
				continue
			}
		}
		if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(f.path, []byte(f.content), f.mode); err != nil {
			return err
		}
		_ = os.Chmod(f.path, f.mode)
		fmt.Println("wrote", f.path)
	}
	// Thunar keeps all custom actions in one XML file; add ours if missing.
	exe, _ := os.Executable()
	exe, _ = filepath.EvalSymlinks(exe)
	tp := thunarPath()
	if b, err := os.ReadFile(tp); err == nil {
		s := string(b)
		if !strings.Contains(s, "syncme-send-1") && strings.Contains(s, "</actions>") {
			s = strings.Replace(s, "</actions>", fmt.Sprintf(thunarAction, xmlEscape(exe))+"</actions>", 1)
			if os.WriteFile(tp, []byte(s), 0o644) == nil {
				fmt.Println("updated", tp)
			}
		}
	} else if _, err := exec.LookPath("thunar"); err == nil {
		_ = os.MkdirAll(filepath.Dir(tp), 0o755)
		s := "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<actions>\n" + fmt.Sprintf(thunarAction, xmlEscape(exe)) + "</actions>\n"
		if os.WriteFile(tp, []byte(s), 0o644) == nil {
			fmt.Println("wrote", tp)
		}
	}
	if p, err := exec.LookPath("update-desktop-database"); err == nil {
		home, _ := os.UserHomeDir()
		_ = exec.Command(p, filepath.Join(home, ".local", "share", "applications")).Run()
	}
	if p, err := exec.LookPath("systemctl"); err == nil {
		_ = exec.Command(p, "--user", "daemon-reload").Run()
		if out, err := exec.Command(p, "--user", "enable", "--now", "syncme.service").CombinedOutput(); err != nil {
			fmt.Printf("could not start the service automatically: %s\nstart it with: syncme daemon\n", strings.TrimSpace(string(out)))
		} else {
			fmt.Println("started syncme.service (systemctl --user status syncme)")
		}
	}
	fmt.Println("\nDone. Restart your file manager (e.g. `nautilus -q`) to see \"Send to Phone (Syncme)\".")
	fmt.Println("Next: run `syncme pair` and scan the QR code with the Syncme Android app.")
	return nil
}

func uninstall() error {
	if p, err := exec.LookPath("systemctl"); err == nil {
		_ = exec.Command(p, "--user", "disable", "--now", "syncme.service").Run()
	}
	files, err := integrationFiles()
	if err != nil {
		return err
	}
	for _, f := range files {
		if os.Remove(f.path) == nil {
			fmt.Println("removed", f.path)
		}
	}
	tp := thunarPath()
	if b, err := os.ReadFile(tp); err == nil {
		s := string(b)
		if i := strings.Index(s, "<action>\n\t<icon>phone</icon>\n\t<name>Send to Phone (Syncme)"); i >= 0 {
			if j := strings.Index(s[i:], "</action>\n"); j >= 0 {
				s = s[:i] + s[i+j+len("</action>\n"):]
				_ = os.WriteFile(tp, []byte(s), 0o644)
				fmt.Println("updated", tp)
			}
		}
	}
	fmt.Println("Your synced files and pairing keys were kept (~/syncme, ~/.config/syncme).")
	return nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

const startHint = "systemctl --user enable --now syncme"

func desktopNotify(title, body string) {
	if p, err := exec.LookPath("notify-send"); err == nil {
		_ = exec.Command(p, "-i", "phone", "-a", "Syncme", title, body).Run()
	}
}

func firewallAdvice() []string {
	switch detectFirewall() {
	case "":
		return []string{"  ✓ no active firewall detected"}
	case "ufw":
		return []string{"  ! ufw firewall is active — allow syncme:", "      sudo ufw allow 47810/tcp && sudo ufw allow 47811/udp"}
	case "firewalld":
		return []string{"  ! firewalld is active — allow syncme:", "      sudo firewall-cmd --permanent --add-port=47810/tcp --add-port=47811/udp && sudo firewall-cmd --reload"}
	default:
		return []string{"  ! a firewall (nftables/iptables) is active — allow syncme:",
			"      sudo nft insert rule inet filter input tcp dport 47810 accept",
			"      sudo nft insert rule inet filter input udp dport 47811 accept",
			"      (or the equivalent for your firewall; make it permanent in /etc/nftables.conf)"}
	}
}

func detectFirewall() string {
	if out, err := exec.Command("systemctl", "is-active", "ufw").Output(); err == nil && strings.TrimSpace(string(out)) == "active" {
		return "ufw"
	}
	if out, err := exec.Command("systemctl", "is-active", "firewalld").Output(); err == nil && strings.TrimSpace(string(out)) == "active" {
		return "firewalld"
	}
	for _, svc := range []string{"nftables", "iptables"} {
		if out, err := exec.Command("systemctl", "is-active", svc).Output(); err == nil && strings.TrimSpace(string(out)) == "active" {
			return svc
		}
	}
	return ""
}
