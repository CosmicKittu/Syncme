# Syncme

Two-way, peer-to-peer sync between a folder on your Linux PC and your Android phone:

```
~/syncme   (Linux)   <──────────>   Download/syncme   (Android)
```

- **Peer-to-peer.** No cloud account and no central server. The phone talks straight to your PC.
- **Local first, internet as fallback.** The phone looks for the PC on your Wi-Fi first. If it can't find it there, it tries the internet (UPnP, a port forward, or a VPN like Tailscale), and then an optional self-hosted relay. It switches back to the local network as soon as it can.
- **Secure pairing.** You scan a QR code (or type an 8-letter code) and check that both screens show the same 6-digit code, which is derived from a hash of both devices' identities. After that every connection uses TLS, and the phone pins the PC's key hash.
- **Reliable.** Every transfer is checked against its SHA-256 hash and written atomically, so a crash never leaves a half-written file. A three-way merge means deletions sync correctly, and edits made on both sides at once keep both versions. Deleted files go to a trash folder for 30 days.
- **Share from anywhere.** On Android, *Share → Syncme* in any app puts the file in the folder. On Linux, right-click → *Send to Phone (Syncme)* works in GNOME Files (Nautilus), Nemo, Caja, Dolphin and Thunar.

| Part | Tech | Where |
|---|---|---|
| PC app (daemon + CLI) | Go, single static binary | `cmd/syncme`, `internal/` |
| Relay (optional) | Go | `cmd/syncme-relay` |
| Android app | Kotlin, plain Android views | `android/` |

---

## Build

```bash
make linux      # dist/syncme-linux-amd64, dist/syncme-linux-arm64 (static, run directly)
make android    # dist/syncme.apk (needs only JDK 17+; downloads the Android SDK itself)
make all        # both
make install    # build + install for your user (~/.local/bin) + start the service
```

**Arch Linux** (package with systemd unit, menus and firewall profile):

```bash
sudo pacman -S --needed base-devel git go
cd packaging/arch && makepkg -si        # builds from GitHub main
# or from this checkout:   make arch && sudo pacman -U dist/*.pkg.tar.zst
systemctl --user enable --now syncme
syncme install && syncme pair
```

For the APK on Arch: `sudo pacman -S --needed jdk17-openjdk unzip && make android`. Then copy
`dist/syncme.apk` to the phone and open it, or use `adb install -r dist/syncme.apk`.

Running `syncme` with no arguments shows the status if the service is running. Otherwise it starts the
service in the foreground, so you can run the binary straight from `dist/`.

## 1. Install on Linux

**Download** `syncme-linux-amd64` (or `-arm64`) from [Releases](../../releases), or build it yourself (Go 1.22 or newer):

```bash
go build -o syncme ./cmd/syncme
```

Then:

```bash
mkdir -p ~/.local/bin && cp syncme ~/.local/bin/ && chmod +x ~/.local/bin/syncme
syncme install        # systemd user service + right-click menus, starts the daemon
syncme status
```

Open the firewall if you use one (needed for the LAN connection):

```bash
sudo ufw allow 47810/tcp   # sync connections
sudo ufw allow 47811/udp   # LAN discovery
```

## 2. Install on Android

Download `syncme-android.apk` from [Releases](../../releases). The latest CI build is also available as the `syncme-android-debug` artifact on the [Actions](../../actions) tab. You can also open `android/` in Android Studio and press *Run*.

On first launch, tap **Allow file access** so Syncme can read and write `Download/syncme`. Then tap **Keep syncing in background** so Android doesn't put the app to sleep.

## 3. Pair

On the PC:

```bash
syncme pair
```

It shows a QR code and an 8-letter code. On the phone, do one of these:

- **Scan QR code** (any camera app also works, because the link opens Syncme), or
- **Find PC on this Wi-Fi**, pick your PC, and type the 8-letter code.

Both screens now show a **6-digit verification code**. If the codes match, type `y` on the PC. Syncing starts right away.

## Everyday use

- Put files in `~/syncme` on the PC or `Download/syncme` on the phone. They show up on the other side within seconds while both are connected.
- **PC:** right-click a file → *Send to Phone (Syncme)*, or run `syncme send file1 file2 ...`.
- **Phone:** in any app, choose *Share → Syncme (send to PC)*.
- `syncme status` shows which phones are paired and connected, and when they last synced.
- `syncme unpair <name>` removes a phone. On the phone, tap **Unpair**.

### Clipboard sharing

- **PC → phone/tablet:** whatever you copy on the PC appears on the phone's clipboard within a second. This includes images such as screenshots. You can turn it off in the app (*Clipboard → Receive PC clipboard automatically*).
- **Phone → PC:** Android doesn't let background apps read the clipboard, so sending takes one tap. Use any of these:
  - the **Send clipboard** button on the Syncme notification
  - the **Send clipboard** Quick Settings tile (add it from the edit-tiles screen)
  - **Share → Syncme clipboard** from any app; on Android 13+ the copy pop-up has a Share button
- **Get PC clipboard** (notification or app) fetches the PC's current clipboard on demand.
- **Phone ⇄ tablet:** a clip from one device is relayed to the others through the PC. The PC must be on.
- What you send lands in KDE's clipboard history (Klipper) like a normal copy. The app keeps its own history of the last 200 clips; tap one to copy it again, or long-press to send, share or delete it.
- **Passwords:** clips that password managers mark as secret (KeePassXC etc. on the PC, "sensitive" clips on Android 13+) are never pushed automatically, never relayed, and never stored in history.
- Copied *files* are not sent as clipboard text. Use file sync (`~/syncme`) for files.
- The PC needs `wl-clipboard` on Wayland (the Arch package pulls it in) or `xclip` on X11. `syncme status` shows whether clipboard sharing is on. Set `"clipboard": false` in the config to disable it.

### What happens when…

| Situation | Result |
|---|---|
| File changed on one side | Copied to the other side |
| File deleted on one side | Deleted on the other side (moved to `.syncme-trash/`, kept for 30 days) |
| Same file edited on both sides | The newer edit keeps the name; the other one is saved as `name.sync-conflict-<date>-<device>.ext` on both sides |
| Deleted on one side, edited on the other | The edit wins and the file comes back |
| Connection drops mid-transfer | Nothing is half-written; the next sync picks up where it stopped |

## Syncing over the internet

On the local network everything works with no setup. To sync from anywhere, use **one** of these options (they're listed from easiest to most work). The phone always tries the LAN first.

1. **Tailscale / ZeroTier / WireGuard.** Install it on both devices. The PC reports its VPN address to the phone automatically, so you don't need to set anything else.
2. **UPnP** (on by default). If your router supports it, the daemon opens port 47810 and tells the phone your public IP. `syncme status` shows the result. This doesn't work behind carrier-grade NAT.
3. **Port forward.** Forward TCP 47810 on your router to the PC, then set `"public_address": "your.ddns.name:47810"` in `~/.config/syncme/config.json`.
4. **Relay.** Run `syncme-relay` on any machine with a public IP, such as a small VPS (`./syncme-relay -listen :47812`). Then set `"relay": "relay.example.com:47812"` in the PC config. The relay only forwards encrypted bytes. It can't read or change your files, because the phone checks the PC's pinned key end to end.

After you change the config, restart the daemon (`systemctl --user restart syncme`). If you changed `relay`, pair again or wait for the next LAN connection so the phone learns the new setting.

## Configuration

`~/.config/syncme/config.json` (created on first run):

```json
{
  "name": "my-laptop",
  "folder": "/home/me/syncme",
  "port": 47810,
  "discovery": true,
  "upnp": true,
  "public_address": "",
  "relay": ""
}
```

Files:

| Path | Contents |
|---|---|
| `~/.config/syncme/key.pem`, `cert.pem` | The PC's identity key (keep it private) |
| `~/.config/syncme/devices.json` | Paired phones and their secrets (mode 0600) |
| `~/.local/share/syncme/peers/*.json` | Last agreed state per phone (used for the merge) |
| `$XDG_RUNTIME_DIR/syncme.sock` | Local control socket for the CLI (mode 0600) |

## Security model

- **Identity.** The PC has an ECDSA P-256 key. Its ID is `sha256(public key)`, and you see the first 16 hex digits as the *fingerprint*.
- **Pinning.** The phone stores that ID when you pair. On every connection, whether over LAN, internet or relay, it checks the TLS certificate's key hash against the stored ID. Certificate authorities aren't involved at all.
- **Pairing.** The 8-letter token works only once and expires after 5 minutes. A wrong guess burns it. Both devices show `code = sha256(PC id, phone id, token)` reduced to 6 digits. Someone in the middle would see a different PC key, so the two codes wouldn't match. The PC user must confirm the match before the phone gets its 256-bit secret.
- **Authentication.** On each connection the PC sends a random nonce. The phone answers with an HMAC-SHA256 of that nonce made with its secret, so the secret itself never crosses the network.
- **File safety.** Every path from the other side is checked so it can't use `..`, absolute paths or syncme's internal names. Symlinks are never followed. Each received file is verified against its SHA-256 before it's renamed into place, and a file is only overwritten or deleted if it hasn't changed since the scan.
- **Phone storage.** The pairing secret lives in the app's private storage, and app backup is turned off.

The full wire protocol is in [docs/PROTOCOL.md](docs/PROTOCOL.md).

## Development

```bash
go test ./...                 # unit tests (merge logic, path validation)
scripts/e2e-test.sh           # runs the Go daemon + relay against the Android app's
                              # Kotlin sync core (compiled on the JVM): pairing, both-way
                              # sync, deletes, conflicts, big files, auth failures,
                              # live updates and relay fallback
cd android && ./gradlew assembleDebug   # needs the Android SDK
```

The Android app's protocol code is in `android/app/src/main/java/com/cosmickittu/syncme/core/`. It's plain Kotlin with no Android imports, which is why it can run in the end-to-end test.

### Signing release APKs

Android won't install an update over an app signed with a different key. To make updates install cleanly, create a keystore once:

```bash
keytool -genkey -v -keystore release.jks -keyalg RSA -keysize 2048 -validity 10000 -alias syncme
```

Then add these repository secrets: `SYNCME_KEYSTORE_B64` (`base64 -w0 release.jks`), `SYNCME_KEYSTORE_PASSWORD`, `SYNCME_KEY_ALIAS` and `SYNCME_KEY_PASSWORD`. Tagging `v1.0.0` builds a release with the Linux binaries and a signed APK. Without these secrets the APK is signed with a debug key.

## Limitations

- Only one phone folder (`Download/syncme`) and one PC folder are synced. Empty folders aren't synced.
- The phone app syncs with one PC; a PC can have several phones.
- Android needs *All files access* to use `Download/syncme` as a normal folder. Google Play restricts that permission, so the app is meant to be installed directly from the APK.
