// syncme keeps ~/syncme in sync with Download/syncme on paired phones.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mdp/qrterminal/v3"

	"github.com/CosmicKittu/Syncme/internal/config"
	"github.com/CosmicKittu/Syncme/internal/daemon"
	"github.com/CosmicKittu/Syncme/internal/identity"
	"github.com/CosmicKittu/Syncme/internal/index"
)

var version = "dev"

const usage = `syncme - sync ~/syncme with your phone

Usage:
  syncme                   show status, or run the service if it isn't running
  syncme daemon            run the sync service (normally started by systemd)
  syncme pair              pair a new phone (shows a QR code)
  syncme status            show this PC's id, addresses and paired phones
  syncme unpair <name|id>  remove a paired phone
  syncme send <files...>   copy files into the synced folder and sync now
  syncme doctor            check why the phone can't connect (firewall, network)
  syncme install           install the systemd user service and right-click menus
  syncme uninstall         remove what "install" added
  syncme version
`

func main() {
	paths := config.DefaultPaths()
	if len(os.Args) < 2 {
		// Running the binary directly: show status if the service is up,
		// otherwise run the service in the foreground.
		if call(paths, "GET", "/status", nil, nil) == nil {
			if err := status(paths); err != nil {
				fmt.Fprintln(os.Stderr, "syncme:", err)
				os.Exit(1)
			}
			fmt.Print("\nRun `syncme help` for all commands.\n")
			return
		}
		fmt.Println("Starting Syncme in the foreground (Ctrl+C to stop). In another terminal run `syncme pair`.")
		os.Args = append(os.Args, "daemon")
	}
	var err error
	switch os.Args[1] {
	case "daemon":
		err = runDaemon(paths, os.Args[2:])
	case "pair":
		err = pair(paths)
	case "status", "devices":
		err = status(paths)
	case "unpair":
		if len(os.Args) != 3 {
			err = errors.New("usage: syncme unpair <name|id>")
			break
		}
		var out map[string]string
		err = call(paths, "POST", "/unpair?id="+urlEscape(os.Args[2]), nil, &out)
		if err == nil {
			fmt.Printf("Removed %s\n", out["removed"])
		}
	case "send":
		err = send(paths, os.Args[2:])
	case "doctor":
		err = doctor(paths)
	case "install":
		err = install()
	case "uninstall":
		err = uninstall()
	case "version", "--version":
		fmt.Println("syncme", version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Print(usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "syncme:", err)
		os.Exit(1)
	}
}

func runDaemon(paths config.Paths, args []string) error {
	cfg, err := config.Load(paths)
	if err != nil {
		return err
	}
	var out io.Writer = os.Stderr
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--log" { // used when started in the background (macOS / Windows)
			f, err := os.OpenFile(args[i+1], os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			if err != nil {
				return err
			}
			defer f.Close()
			if st, _ := f.Stat(); st != nil && st.Size() > 5<<20 { // keep it small
				_ = f.Truncate(0)
			}
			out = f
		}
	}
	logger := log.New(out, "", log.LstdFlags)
	d, err := daemon.New(cfg, paths, logger)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return d.Run(ctx)
}

// ---- control API client ----

func client(paths config.Paths, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", paths.Socket)
		}},
	}
}

func call(paths config.Paths, method, path string, body any, out any) error {
	return callTimeout(paths, method, path, body, out, 15*time.Second)
}

func callTimeout(paths config.Paths, method, path string, body any, out any, timeout time.Duration) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, "http://syncme"+path, rd)
	resp, err := client(paths, timeout).Do(req)
	if err != nil {
		return fmt.Errorf("syncme is not running — start it with `%s` or `syncme daemon` (%v)", startHint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		var e struct{ Error string }
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return errors.New(e.Error)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func urlEscape(s string) string {
	r := strings.NewReplacer("%", "%25", "&", "%26", "+", "%2B", " ", "%20", "#", "%23", "?", "%3F")
	return r.Replace(s)
}

func status(paths config.Paths) error {
	var st daemon.Status
	if err := call(paths, "GET", "/status", nil, &st); err != nil {
		return err
	}
	fmt.Printf("PC:       %s\nID:       %s\nFolder:   %s\n", st.Name, identity.Short(st.ID), st.Folder)
	fmt.Printf("LAN:      %s\n", strings.Join(st.Addrs, ", "))
	pub := st.Public
	if pub == "" {
		pub = "(none — enable UPnP, set public_address, or use a relay/VPN)"
	}
	fmt.Printf("Internet: %s\n", pub)
	if st.Relay != "" {
		fmt.Printf("Relay:    %s\n", st.Relay)
	}
	fmt.Printf("Clipboard: %s\n", st.Clipboard)
	fmt.Println()
	if len(st.Devices) == 0 {
		fmt.Println("No paired phones. Run `syncme pair`.")
		return nil
	}
	fmt.Println("Paired phones:")
	for _, d := range st.Devices {
		state := "offline"
		if d.Connected {
			state = "connected"
		}
		last := "never"
		if !d.LastSync.IsZero() {
			last = d.LastSync.Local().Format("2006-01-02 15:04:05")
		}
		fmt.Printf("  %-20s %s  %-9s last sync %s\n", d.Name, identity.Short(d.ID), state, last)
	}
	return nil
}

func pair(paths config.Paths) error {
	var ps daemon.PairStart
	if err := call(paths, "POST", "/pair/start", nil, &ps); err != nil {
		return err
	}
	var st daemon.Status
	_ = call(paths, "GET", "/status", nil, &st)
	fmt.Println("Open Syncme on your phone, tap \"Pair with PC\" and scan this code:")
	fmt.Println()
	qrterminal.GenerateWithConfig(ps.URI, qrterminal.Config{Level: qrterminal.L, Writer: os.Stdout, HalfBlocks: true,
		BlackChar: qrterminal.BLACK_BLACK, WhiteChar: qrterminal.WHITE_WHITE, WhiteBlackChar: qrterminal.WHITE_BLACK, BlackWhiteChar: qrterminal.BLACK_WHITE, QuietZone: 2})
	fmt.Println()
	fmt.Printf("Or pick \"%s\" from the list on the phone (same Wi-Fi) and enter code:  %s\n", st.Name, ps.Token)
	fmt.Printf("PC fingerprint: %s\n", identity.Short(st.ID))
	fmt.Printf("Code expires at %s.\n\nWaiting for phone...\n", ps.Expires.Local().Format("15:04:05"))

	var req daemon.PairRequest
	if err := callTimeout(paths, "GET", "/pair/wait?token="+ps.Token, nil, &req, 6*time.Minute); err != nil {
		return err
	}
	fmt.Printf("\nPhone \"%s\" wants to pair.\n\n    Verification code:  %s %s\n\n", req.Name, req.Code[:3], req.Code[3:])
	fmt.Print("Does the phone show the SAME code? [y/N] ")
	ans, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	accept := strings.EqualFold(strings.TrimSpace(ans), "y") || strings.EqualFold(strings.TrimSpace(ans), "yes")
	if err := call(paths, "POST", "/pair/confirm", map[string]any{"token": ps.Token, "accept": accept}, nil); err != nil {
		return err
	}
	if !accept {
		return errors.New("pairing rejected")
	}
	fmt.Printf("Paired with %s. Files in %s will now sync.\n", req.Name, st.Folder)
	return nil
}

// ---- send (right-click "Send to phone") ----

func send(paths config.Paths, files []string) error {
	notify := false
	var list []string
	for _, f := range files {
		if f == "--notify" {
			notify = true
			continue
		}
		list = append(list, f)
	}
	if len(list) == 0 {
		return errors.New("usage: syncme send <files...>")
	}
	cfg, err := config.Load(paths)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.Folder, 0o755); err != nil {
		return err
	}
	var sent []string
	for _, src := range list {
		src = strings.TrimPrefix(src, "file://")
		abs, err := filepath.Abs(src)
		if err != nil {
			return err
		}
		if rel, ok := index.Rel(cfg.Folder, abs); ok && !strings.HasPrefix(rel, "..") {
			sent = append(sent, filepath.Base(abs)) // already inside the folder
			continue
		}
		dst := uniqueName(filepath.Join(cfg.Folder, filepath.Base(abs)))
		if err := copyTree(abs, dst); err != nil {
			if notify {
				desktopNotify("Syncme: could not send "+filepath.Base(abs), err.Error())
			}
			return err
		}
		sent = append(sent, filepath.Base(dst))
	}
	pokeErr := call(paths, "POST", "/poke", nil, nil)
	msg := fmt.Sprintf("%d item(s) queued for your phone: %s", len(sent), strings.Join(sent, ", "))
	if pokeErr != nil {
		msg += "\n(daemon is not running; they will sync once it starts)"
	}
	fmt.Println(msg)
	if notify {
		desktopNotify("Syncme", msg)
	}
	return nil
}

func uniqueName(p string) string {
	if _, err := os.Lstat(p); errors.Is(err, os.ErrNotExist) {
		return p
	}
	ext := filepath.Ext(p)
	stem := strings.TrimSuffix(p, ext)
	for i := 1; ; i++ {
		c := fmt.Sprintf("%s (%d)%s", stem, i, ext)
		if _, err := os.Lstat(c); errors.Is(err, os.ErrNotExist) {
			return c
		}
	}
}

func copyTree(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(src, p)
			target := filepath.Join(dst, rel)
			if d.IsDir() {
				return os.MkdirAll(target, 0o755)
			}
			if !d.Type().IsRegular() {
				return nil
			}
			return copyFile(p, target)
		})
	}
	return copyFile(src, dst)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".syncme-tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	_ = os.Chtimes(tmp.Name(), info.ModTime(), info.ModTime())
	_ = os.Chmod(tmp.Name(), info.Mode().Perm())
	return os.Rename(tmp.Name(), dst)
}
