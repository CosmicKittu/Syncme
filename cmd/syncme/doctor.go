package main

import (
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"

	"github.com/CosmicKittu/Syncme/internal/config"
	"github.com/CosmicKittu/Syncme/internal/daemon"
)

// doctor checks the usual reasons a phone cannot reach the PC.
func doctor(paths config.Paths) error {
	ok := func(s string) { fmt.Println("  ✓", s) }
	bad := func(s string) { fmt.Println("  ✗", s) }

	var st daemon.Status
	if err := call(paths, "GET", "/status", nil, &st); err != nil {
		bad("syncme service is not running")
		fmt.Println("      start it: systemctl --user enable --now syncme")
		return nil
	}
	ok("syncme service is running (" + st.Name + ")")

	port := st.Port
	if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second); err == nil {
		c.Close()
		ok(fmt.Sprintf("listening on TCP %d", port))
	} else {
		bad(fmt.Sprintf("nothing answers on TCP %d: %v", port, err))
	}

	var lan []string
	for _, a := range st.Addrs {
		h, _, _ := net.SplitHostPort(a)
		ip := net.ParseIP(h)
		if ip != nil && ip.To4() != nil && ip.IsPrivate() && !strings.HasPrefix(h, "172.17.") {
			lan = append(lan, h)
		}
	}
	if len(lan) == 0 {
		bad("no Wi-Fi/Ethernet LAN address found — is the PC connected to the network?")
	} else {
		ok("LAN address(es): " + strings.Join(lan, ", ") + "  (the phone must be on the same network)")
	}

	if strings.HasPrefix(st.Clipboard, "on") {
		ok("clipboard sharing " + st.Clipboard)
	} else {
		bad("clipboard sharing " + st.Clipboard)
	}

	fw := detectFirewall()
	if fw == "" {
		ok("no active firewall detected")
	} else {
		fmt.Printf("  ! %s firewall is active — make sure syncme is allowed:\n", fw)
		switch fw {
		case "ufw":
			fmt.Println("      sudo ufw allow 47810/tcp && sudo ufw allow 47811/udp")
		case "firewalld":
			fmt.Println("      sudo firewall-cmd --permanent --add-port=47810/tcp --add-port=47811/udp && sudo firewall-cmd --reload")
		default:
			fmt.Println("      sudo nft insert rule inet filter input tcp dport 47810 accept")
			fmt.Println("      sudo nft insert rule inet filter input udp dport 47811 accept")
			fmt.Println("      (or the equivalent for your firewall; make it permanent in /etc/nftables.conf)")
		}
	}
	where := "the PC"
	if len(lan) > 0 {
		where += " (" + strings.Join(lan, ", ") + ")"
	}
	fmt.Printf("\nOn the phone: use the same Wi-Fi as %s — not a guest network — and turn mobile data off for the test,\n", where)
	fmt.Printf("then tap \"Find PC on this Wi-Fi\". If it finds the PC, pairing will work.\n")
	return nil
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
