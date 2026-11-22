package daemon

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/CosmicKittu/Syncme/internal/identity"
)

// The control API is HTTP on a unix socket (mode 0600) used by the CLI.

type Status struct {
	Name    string         `json:"name"`
	ID      string         `json:"id"`
	Folder  string         `json:"folder"`
	Port    int            `json:"port"`
	Addrs   []string       `json:"addrs"`
	Public  string         `json:"public"`
	Relay   string         `json:"relay"`
	Devices []DeviceStatus `json:"devices"`
}

type DeviceStatus struct {
	Device
	Connected bool `json:"connected"`
}

type PairStart struct {
	Token   string    `json:"token"`
	URI     string    `json:"uri"`
	Expires time.Time `json:"expires"`
}

// PairURI is what the QR code contains.
func (d *Daemon) PairURI(token string) string {
	v := url.Values{}
	v.Set("id", d.ID.ID)
	v.Set("n", d.Cfg.Name)
	v.Set("t", token)
	addrs := d.Addresses()
	if p := d.Public(); p != "" {
		addrs = append(addrs, p)
	}
	v.Set("a", strings.Join(addrs, ","))
	if d.Cfg.Relay != "" {
		v.Set("r", d.Cfg.Relay)
	}
	return "syncme://pair?" + v.Encode()
}

func (d *Daemon) serveControl(ctx context.Context) (net.Listener, error) {
	sock := d.Paths.Socket
	_ = os.MkdirAll(filepath.Dir(sock), 0o700)
	if c, err := net.Dial("unix", sock); err == nil {
		c.Close()
		return nil, &os.PathError{Op: "listen", Path: sock, Err: os.ErrExist} // another daemon is running
	}
	_ = os.Remove(sock)
	old := umask(0o177)
	ln, err := net.Listen("unix", sock)
	umask(old)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(sock, 0o600)

	mux := http.NewServeMux()
	writeJSON := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	httpErr := func(w http.ResponseWriter, code int, msg string) {
		w.WriteHeader(code)
		writeJSON(w, map[string]string{"error": msg})
	}
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		st := Status{Name: d.Cfg.Name, ID: d.ID.ID, Folder: d.Cfg.Folder, Port: d.Cfg.Port, Addrs: d.Addresses(), Public: d.Public(), Relay: d.Cfg.Relay}
		for _, dv := range d.devices.List() {
			st.Devices = append(st.Devices, DeviceStatus{Device: dv, Connected: d.IsConnected(dv.ID)})
		}
		writeJSON(w, st)
	})
	mux.HandleFunc("POST /pair/start", func(w http.ResponseWriter, r *http.Request) {
		tok, exp := d.StartPairing()
		writeJSON(w, PairStart{Token: tok, URI: d.PairURI(tok), Expires: exp})
	})
	mux.HandleFunc("GET /pair/wait", func(w http.ResponseWriter, r *http.Request) {
		req, err := d.WaitPairRequest(r.URL.Query().Get("token"), 5*time.Minute)
		if err != nil {
			httpErr(w, http.StatusRequestTimeout, err.Error())
			return
		}
		writeJSON(w, req)
	})
	mux.HandleFunc("POST /pair/confirm", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Token  string `json:"token"`
			Accept bool   `json:"accept"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := d.ConfirmPairing(body.Token, body.Accept); err != nil {
			httpErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /unpair", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		for _, dv := range d.devices.List() {
			if id != "" && (dv.ID == id || strings.HasPrefix(strings.ReplaceAll(strings.ToLower(identity.Short(dv.ID)), "-", ""), strings.ToLower(strings.ReplaceAll(id, "-", ""))) || strings.EqualFold(dv.Name, id)) {
				d.devices.Remove(dv.ID)
				_ = os.Remove(basePath(d.Paths.DataDir, dv.ID))
				writeJSON(w, map[string]string{"removed": dv.Name})
				return
			}
		}
		httpErr(w, http.StatusNotFound, "no such device")
	})
	mux.HandleFunc("POST /poke", func(w http.ResponseWriter, r *http.Request) {
		d.Poke()
		writeJSON(w, map[string]bool{"ok": true})
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	go func() { <-ctx.Done(); srv.Close(); os.Remove(sock) }()
	return ln, nil
}
