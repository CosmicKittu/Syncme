# Syncme

**Syncme keeps a folder on your computer and a folder on your Android phone identical, and shares your clipboard between them. It works directly over your own Wi-Fi, with no cloud and no account.**

```
   Computer                                   Android phone / tablet
   ~/syncme   ◀────────  your Wi-Fi  ────────▶  Download/syncme
   clipboard  ◀──── encrypted, direct ───────▶  clipboard
```

Put a file in `syncme` on one device and it appears on the other within seconds. Edit it, rename it or delete it, and the change follows. Copy text or a screenshot on the computer and it's on your phone's clipboard. Send your phone's clipboard to the computer with one tap.

Works on **Windows 10/11, macOS, Ubuntu/Debian, Arch and any other Linux**, with **Android 8+** phones and tablets.

---

## Contents

- [What it does](#what-it-does)
- [Download](#download)
- [Install on your computer](#1-install-on-your-computer): [Windows](#windows) · [macOS](#macos) · [Ubuntu / Debian](#ubuntu--debian--linux-mint--popos) · [Arch](#arch-linux) · [Other Linux](#other-linux)
- [Install on your phone or tablet](#2-install-on-your-android-phone-or-tablet)
- [Pair them](#3-pair-your-phone-with-the-computer)
- [Everyday use](#everyday-use)
- [Troubleshooting](#troubleshooting)
- [Uninstall](#uninstall)
- [Advanced](#advanced): syncing over the internet, settings, security, building

---

## What it does

| | |
|---|---|
| 📁 **Folder sync** | `syncme` in your home folder on the computer ⇄ `Download/syncme` on the phone. Two-way: new files, edits, renames and deletes all sync. |
| 📋 **Clipboard sharing** | Copy on the computer → it's on the phone's clipboard (text and images). Phone → computer takes one tap: Android doesn't let apps read the clipboard in the background. |
| 📤 **Send from anywhere** | Phone: *Share → Syncme* in any app. Computer: right-click a file → *Send to Phone*. |
| 📱📱 **Several devices** | Pair a phone *and* a tablet with the same computer. Files and clipboard reach all of them, passing through the computer. |
| 🏠 **Local first** | Finds the computer on your Wi-Fi automatically. Over the internet only if you set that up ([details](#syncing-over-the-internet)). |
| 🔒 **Private and secure** | Peer-to-peer and encrypted, with no server in the middle. Pairing is confirmed by matching a 6-digit code on both screens. |
| 🛟 **Safe** | Deleted files go to a `.syncme-trash` folder for 30 days. If a file was edited on both sides at once, both versions are kept. Transfers are checked, so no half-written files. |

What it doesn't do: sync more than one folder, or sync two computers with each other. The computer is the hub, so phones and tablets sync through it.

---

## Download

Everything is on the **[Releases page](https://github.com/CosmicKittu/Syncme/releases/latest)**. The installers below download the right file for you. These are the direct links:

| Device | File |
|---|---|
| Android phone / tablet | [`syncme-android.apk`](https://github.com/CosmicKittu/Syncme/releases/latest/download/syncme-android.apk) |
| Windows (most PCs) | [`syncme-windows-amd64.exe`](https://github.com/CosmicKittu/Syncme/releases/latest/download/syncme-windows-amd64.exe) |
| Windows on ARM (Surface Pro X, Snapdragon laptops) | [`syncme-windows-arm64.exe`](https://github.com/CosmicKittu/Syncme/releases/latest/download/syncme-windows-arm64.exe) |
| Mac with Apple Silicon (M1/M2/M3/M4) | [`syncme-macos-arm64`](https://github.com/CosmicKittu/Syncme/releases/latest/download/syncme-macos-arm64) |
| Mac with Intel CPU | [`syncme-macos-amd64`](https://github.com/CosmicKittu/Syncme/releases/latest/download/syncme-macos-amd64) |
| Ubuntu / Debian / Mint / Pop!_OS | [`syncme-ubuntu-amd64.deb`](https://github.com/CosmicKittu/Syncme/releases/latest/download/syncme-ubuntu-amd64.deb) · [`arm64`](https://github.com/CosmicKittu/Syncme/releases/latest/download/syncme-ubuntu-arm64.deb) |
| Arch Linux | [`syncme-arch-x86_64.pkg.tar.zst`](https://github.com/CosmicKittu/Syncme/releases/latest/download/syncme-arch-x86_64.pkg.tar.zst) |
| Any other Linux | [`syncme-linux-amd64`](https://github.com/CosmicKittu/Syncme/releases/latest/download/syncme-linux-amd64) · [`arm64`](https://github.com/CosmicKittu/Syncme/releases/latest/download/syncme-linux-arm64) |

---

## 1. Install on your computer

Pick your system. Each method installs Syncme, starts it, and makes it start automatically when you log in.

### Windows

Open **PowerShell** (Start menu → type *PowerShell*) and paste:

```powershell
irm https://raw.githubusercontent.com/CosmicKittu/Syncme/main/scripts/install-windows.ps1 | iex
```

What this does:
- installs `syncme.exe` into `%LOCALAPPDATA%\Programs\Syncme` and adds it to your PATH
- starts Syncme in the background, and again at every login
- adds **Send to → Phone (Syncme)** to the right-click menu in Explorer
- asks once for admin rights to allow Syncme through Windows Firewall on private networks (you can say no)

Then **open a new PowerShell window** and continue with [step 2](#2-install-on-your-android-phone-or-tablet).

> **Important:** set your Wi-Fi to *Private*, or Windows will block other devices: *Settings → Network & internet → Wi-Fi → (your network) → Network profile type → Private*.
>
> If you download the `.exe` in a browser instead, Windows SmartScreen may warn about an unknown app. Click *More info → Run anyway*, then run `syncme-windows-amd64.exe install` in PowerShell from the download folder.

### macOS

Open **Terminal** (Spotlight → type *Terminal*) and paste:

```bash
curl -fsSL https://raw.githubusercontent.com/CosmicKittu/Syncme/main/scripts/install.sh | bash
```

This installs `syncme` into `~/.local/bin` and starts it, and it starts again at every login. Open a new Terminal window afterwards so the `syncme` command is found.

> The first time, macOS may ask whether *syncme* may **accept incoming connections** and **find devices on your local network**. Click **Allow** both times. If the phone can't find your Mac later, check *System Settings → Privacy & Security → Local Network*.

### Ubuntu / Debian / Linux Mint / Pop!_OS

```bash
wget https://github.com/CosmicKittu/Syncme/releases/latest/download/syncme-ubuntu-amd64.deb
sudo apt install ./syncme-ubuntu-amd64.deb
systemctl --user enable --now syncme
syncme install        # adds "Send to Phone" to the right-click menu in Files
```

(On ARM computers such as a Raspberry Pi, use `syncme-ubuntu-arm64.deb`.)

If your firewall is on (`sudo ufw status`), allow Syncme: `sudo ufw allow syncme`.

### Arch Linux

```bash
wget https://github.com/CosmicKittu/Syncme/releases/latest/download/syncme-arch-x86_64.pkg.tar.zst
sudo pacman -U syncme-arch-x86_64.pkg.tar.zst
systemctl --user enable --now syncme
syncme install
```

Or build it yourself: `git clone https://github.com/CosmicKittu/Syncme && cd Syncme/packaging/arch && makepkg -si`.

### Other Linux

```bash
curl -fsSL https://raw.githubusercontent.com/CosmicKittu/Syncme/main/scripts/install.sh | bash
```

For clipboard sharing, also install `wl-clipboard` (Wayland: KDE, Sway, Hyprland) or `xclip` (X11 and GNOME).

---

## 2. Install on your Android phone or tablet

1. On the phone, download **[syncme-android.apk](https://github.com/CosmicKittu/Syncme/releases/latest/download/syncme-android.apk)** and open it. If Android asks, allow your browser to *install unknown apps*.
2. Open **Syncme** and tap:
   - **Allow file access**, so it can use `Download/syncme`
   - **Keep syncing in background (battery)**, so Android doesn't put it to sleep
3. Optional: pull down Quick Settings → ✏️ edit → drag in the **Send clipboard** tile.

## 3. Pair your phone with the computer

Phone and computer must be on the **same Wi-Fi**. On the computer, run:

```
syncme pair
```

It shows a QR code. On the phone, tap **Scan QR code** and scan it. You can also tap **Find PC on this Wi-Fi** and type the 8-letter code.

Both screens now show a **6-digit code**. If they match, type `y` on the computer. That's it: syncing starts. Repeat for a tablet or another phone.

---

## Everyday use

### Files

| On the computer | On the phone |
|---|---|
| Put files in the **`syncme`** folder in your home folder | Files appear in **Downloads → syncme** |
| Right-click → **Send to Phone (Syncme)** (Windows: *Send to → Phone (Syncme)*; Windows 11: under *Show more options*) | **Share → Syncme** in any app |
| Terminal: `syncme send photo.jpg report.pdf` | |

Changes sync within seconds while both are on the same Wi-Fi. When the phone comes back home, it catches up automatically.

### Clipboard

- **Computer → phone:** just copy. It's on the phone's clipboard about a second later. To turn this off: app → *Clipboard* → *Receive PC clipboard automatically*.
- **Phone → computer:** tap **Send clipboard** in the Syncme notification, the Quick Settings tile, or *Share → Syncme clipboard*.
- **Get PC clipboard** (in the notification or the app) fetches the computer's clipboard on demand.
- The app's **Clipboard** screen keeps a history of the last 200 clips. Tap one to copy it again.
- Passwords copied from password managers (KeePassXC, Bitwarden, 1Password, KeePass…) are never sent automatically and never stored.

### What happens when…

| Situation | Result |
|---|---|
| A file is changed on one side | It's copied to the other side |
| A file is deleted on one side | It's deleted on the other too (kept in `.syncme-trash/` for 30 days) |
| The same file is edited on both sides | The newer edit keeps the name; the other is saved as `name.sync-conflict-<date>-<device>.ext` |
| Deleted on one side, edited on the other | The edit wins and the file comes back |
| Wi-Fi drops in the middle of a transfer | Nothing is half-written; it continues next time |

### Useful commands (computer)

```
syncme status        which devices are paired / connected, last sync, clipboard status
syncme doctor        checks why a phone can't connect (firewall, network) and tells you the fix
syncme pair          pair another phone or tablet
syncme unpair <id>   remove a device (use the ID shown by `syncme status`)
syncme send <files>  send files to your devices
```

---

## Troubleshooting

**The phone says "Waiting for PC…" or "Could not reach".** Run `syncme doctor` on the computer; it checks everything and prints the fix. The usual causes:

| Cause | Fix |
|---|---|
| Different networks | Phone and computer on the same Wi-Fi; not a guest network; mobile data doesn't count |
| **Windows**: Wi-Fi set to *Public* | *Settings → Network & internet → Wi-Fi → your network → Private* |
| **Windows**: firewall | Run in an *Administrator* PowerShell: `netsh advfirewall firewall add rule name="Syncme" dir=in action=allow program="$env:LOCALAPPDATA\Programs\Syncme\syncme.exe" profile=private` |
| **macOS**: blocked | *System Settings → Privacy & Security → Local Network* → allow syncme; *Network → Firewall → Options* → allow syncme |
| **Linux**: firewall | `sudo ufw allow syncme` (Ubuntu), or open TCP 47810 + UDP 47811 |
| Syncme isn't running | Windows: log out and back in, or run `syncme install`. macOS: `syncme install`. Linux: `systemctl --user restart syncme` |
| Router isolates devices | Some routers ("AP isolation", guest or office Wi-Fi) block devices from seeing each other. Use your normal home network. |

**Clipboard doesn't sync from the computer.** `syncme status` shows a `Clipboard:` line.
- Linux: if it says *install wl-clipboard* or *xclip*, install that package.
- KDE: copied images appear in the history only if Klipper's *Ignore images* is off.

**After updating the app, the phone can't connect.** Update Syncme on the computer too. If the app was reinstalled, pair again (`syncme pair`), and remove the old entry with `syncme unpair <old-id>`.

**Logs:**

| System | Where |
|---|---|
| Linux | `journalctl --user -u syncme -f` |
| macOS | `~/Library/Logs/syncme.log` |
| Windows | `%APPDATA%\syncme\syncme.log` |

---

## Uninstall

| System | Command |
|---|---|
| Windows | `syncme uninstall`, then delete `%LOCALAPPDATA%\Programs\Syncme` |
| macOS / other Linux | `syncme uninstall && rm ~/.local/bin/syncme` |
| Ubuntu / Debian | `systemctl --user disable --now syncme && sudo apt remove syncme` |
| Arch | `systemctl --user disable --now syncme && sudo pacman -R syncme-git` |
| Android | Uninstall the app as usual |

Your files in `syncme` stay where they are.

---

## Advanced

### Syncing over the internet

Syncme always tries your local network first. To also sync when you're away from home, pick **one** of these:

1. **Tailscale (easiest).** Install [Tailscale](https://tailscale.com) on the computer and the phone with the same account. Then connect at home once so the phone learns the new address. It works through any network, including mobile data.
2. **UPnP** (on by default). If your router supports it and your internet provider gives you a real public IP, Syncme opens the port by itself. `syncme status` shows `Internet:`. This doesn't work behind carrier-grade NAT, which is common on mobile and many broadband connections.
3. **Port forwarding.** Forward TCP 47810 to the computer, then set `"public_address": "your-ddns-name:47810"` in the config.
4. **Relay.** Run `syncme-relay` on any server with a public IP, then set `"relay": "host:47812"`. It only forwards encrypted data and can't read it.

### Settings

The config file is created on first run. Restart Syncme after editing it.

| System | Config folder |
|---|---|
| Linux | `~/.config/syncme/` |
| macOS | `~/Library/Application Support/syncme/` |
| Windows | `%APPDATA%\syncme\` |

```json
{
  "name": "my-laptop",          // shown on the phone
  "folder": "/home/me/syncme",  // the synced folder
  "port": 47810,
  "discovery": true,            // answer "Find PC on this Wi-Fi"
  "upnp": true,
  "public_address": "",
  "relay": "",
  "clipboard": true             // set false to turn off clipboard sharing
}
```

The config folder also holds the computer's private key (`key.pem`) and the paired devices (`devices.json`). Keep it private.

### Security

- **Identity.** Each computer has its own key. Its *fingerprint* (shown by `syncme status` and on the phone) is a hash of that key.
- **Pinning.** The phone remembers the fingerprint when you pair and checks it on every connection, so nobody else can pretend to be your computer, not even on the same Wi-Fi.
- **Pairing.** The 8-letter pairing code works once and expires after 5 minutes. A wrong guess cancels it. Both screens show a 6-digit code computed from both devices' identities. If they match, nobody is in between.
- **Encryption.** Every connection uses TLS. Each phone proves itself with a 256-bit secret that never crosses the network.
- **File safety.** Every received file is verified before it's saved. Paths from the other side can't escape the `syncme` folder. A file is only overwritten or deleted if it hasn't changed since the last check.

Full details: [docs/PROTOCOL.md](docs/PROTOCOL.md).

### Building from source

Needs [Go](https://go.dev/dl/) 1.22+. The Android app needs JDK 17+; the build script downloads the Android SDK itself.

```bash
git clone https://github.com/CosmicKittu/Syncme && cd Syncme
make            # this computer's binary  -> dist/syncme
make dist       # Linux, macOS, Windows binaries + Ubuntu .deb packages -> dist/
make android    # Android APK -> dist/syncme.apk
make test       # unit + end-to-end tests
```

Each push is tested on Linux, macOS and Windows by [CI](.github/workflows/ci.yml): unit tests, the real system clipboard, the installers, and a full end-to-end test of the phone's sync code against the computer.

**Signing the APK.** So that app updates install over the old version, create a key once with `keytool -genkey -v -keystore release.jks -keyalg RSA -keysize 2048 -validity 10000 -alias syncme`. Then add these repository secrets: `SYNCME_KEYSTORE_B64` (`base64 -w0 release.jks`), `SYNCME_KEYSTORE_PASSWORD`, `SYNCME_KEY_ALIAS` and `SYNCME_KEY_PASSWORD`. Without them, every release is signed with a new temporary key, so updating means uninstalling first and pairing again.

### Limitations

- One synced folder per computer. Empty folders aren't synced.
- Each phone or tablet pairs with one computer; a computer can have several phones and tablets.
- Windows doesn't allow some file names (`a:b.txt`, `CON`…). Such files stay on the phone and aren't copied to a Windows PC.
- On Windows and macOS, two phone files whose names differ only in capital letters (`Photo.jpg` / `photo.jpg`) clash.
- The Android app needs *All files access* to use `Download/syncme`, so it's installed from the APK rather than the Play Store.
