# Syncme protocol (v1)

Two implementations must agree on this file: `internal/proto` + `internal/daemon` (Go, PC)
and `android/app/src/main/java/com/cosmickittu/syncme/core` (Kotlin, phone).

## Transport

- The PC listens on **TCP 47810**. All traffic runs over **TLS 1.2+**. The PC is the TLS server and uses a self-signed ECDSA P-256 certificate.
- The phone accepts the certificate only if `hex(sha256(SubjectPublicKeyInfo DER)) == pinned PC id`.
- **Framing** inside TLS works like this: `type:u8 | length:u32be | payload`.
  - `J` frames carry one UTF-8 JSON object (at most 64 MiB).
  - `D` frames carry file bytes (at most 1 MiB each). A file body is a run of `D` frames followed by an empty `D` frame.

## Discovery (LAN)

The phone sends the UDP datagram `SYNCME?1` to 255.255.255.255 and to each interface's broadcast address, on port 47811. The PC answers with:

```json
{"syncme":1,"id":"<pc id>","name":"<pc name>","port":47810}
```

Answers aren't trusted by themselves. They only suggest an address, and TLS pinning decides whether it's really the paired PC.

## Connection order (phone)

1. LAN: discovery results, the last good address, and every address the PC reported (including VPN addresses). The phone tries them all in parallel and the first successful TLS handshake wins.
2. The PC's public address (from UPnP or `public_address`).
3. The relay (if one is configured).

When connected over 2 or 3, the phone rechecks the LAN every ~10 sync cycles and reconnects locally if it finds the PC. It also reconnects whenever Android reports a network change.

## Pairing

```
phone -> {"t":"pair","v":1,"id":<phone id>,"name":<phone name>,"token":<8 chars>}
PC    -> {"t":"pair_pending","code":<6 digits>}      (or {"t":"error"})
         ... the user compares the codes and confirms on the PC ...
PC    -> {"t":"paired","secret":<base64 32 bytes>,"name","id","addrs","public","relay"}
```

`code = u32be(sha256("syncme-pair-v1\n" + pcId + "\n" + phoneId + "\n" + TOKEN)[0:4]) mod 1e6`, shown as 6 digits.
Each device computes the code from what it saw itself. The token can be used only once (one attempt) and expires after 5 minutes.

## Sync connection

```
phone -> {"t":"hello","v":1,"id":<phone id>,"name"}
PC    -> {"t":"challenge","nonce":<hex 32 bytes>}
phone -> {"t":"auth","mac":hex(HMAC-SHA256(secret, "syncme-auth-v1\n"+nonce+"\n"+pcId+"\n"+phoneId))}
PC    -> {"t":"welcome","name","id","addrs":[...],"public","relay"}   or {"t":"error"}
```

Then the phone repeats this cycle:

```
phone -> {"t":"sync","files":[{"p":path,"s":size,"m":mtime_ms,"h":sha256hex}, ...]}
         PC computes the plan and drives the session:
PC    -> {"t":"get","p"}                       phone -> {"t":"file","p","s","m","h"} + data   | {"t":"error"}
PC    -> {"t":"put","p","s","m","h","old"} + data   phone -> {"t":"ok"} | {"t":"error"}
PC    -> {"t":"del","p","h"}                   phone -> ok | error   (only if the current hash == h)
PC    -> {"t":"mv","p","to","h"}               phone -> ok | error   (conflict copy)
PC    -> {"t":"done","stats":{...}}
phone -> {"t":"wait"}                          (long poll, about 25 s)
phone -> {"t":"poke"}                          (optional: "I have changes, wake now")
PC    -> {"t":"wake","reason":"change"|"poke"|"timeout"}
```

`put.old` is the hash the PC expects the phone to have at that path right now (empty means none). If the phone's file differs from both `old` and the new hash, the phone refuses the write, and the next cycle handles it as a conflict.

## Clipboard

Clipboard messages can be sent by either side at any time on the sync connection, including during a sync session or a wait. Receivers handle them wherever they read and then carry on. A message plus its data frames is always written atomically.

```
either -> {"t":"clip","id","kind":"text","mime":"text/plain","text":...,"h":sha256,"s":size,"sens":bool,"from":device,"reply":bool}
either -> {"t":"clip","id","kind":"image","mime":"image/png","h","s",...} + data frames
PC     -> {"t":"clip","kind":"none","reply":true}      (answer to clipget when the PC clipboard is empty)
phone  -> {"t":"clipget"}                              (ask for the PC clipboard now)
phone  -> {"t":"clipcfg","auto":bool}                  (push PC copies automatically? sent after welcome)
```

The PC is the hub. A clip from a device is written to the PC clipboard and offered to the other devices. PC copies are offered to every device with `auto` on. A device is never sent a clip whose hash it already has. Sensitive clips (`sens`) are only delivered as replies to `clipget`, never relayed automatically. Limits: text 1 MiB, images 16 MiB.

## Merge (PC side, `internal/syncer`)

For each path, the PC compares three versions by content hash:

- **L**: the PC's file now
- **R**: the phone's file now
- **B**: the base, which is the state both sides agreed on at the end of the last sync with this phone

| Case | Action |
|---|---|
| L = R | record it in the base |
| L = B, R ≠ B | pull R from the phone (or delete locally if R is gone) |
| R = B, L ≠ B | push L to the phone (or delete on the phone if L is gone) |
| L gone, R changed | pull R (the edit wins over the delete) |
| R gone, L changed | push L |
| both changed | the newer mtime keeps the name; the other version becomes `*.sync-conflict-*` on both sides |

The base is updated after each successful operation, so an interrupted session loses nothing.
Paths are relative and `/`-separated. They're rejected if they're empty, absolute, contain `.`, `..`, `\`, NUL, or have a segment starting with `.syncme`, `.pending-` or `.trashed-`.

## Relay

This is a plain line protocol on TCP 47812. The relay speaks first.

```
relay -> NONCE <hex>
PC    -> LISTEN <pc id> <spki base64> <ecdsa-sig base64 over sha256("syncme-relay-listen-v1:"+nonce)>
relay -> OK            ... later: INCOMING <sid>   (and PING every 30 s)
phone -> CONNECT <pc id>              relay -> OK   (after the PC accepts)
PC    -> ACCEPT <sid>  (new conn)     relay -> OK
```

After both `OK`s, the relay just copies bytes in both directions, and the phone and PC run TLS through it. The relay never holds any keys.
