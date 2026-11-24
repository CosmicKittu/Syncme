package com.cosmickittu.syncme.core

import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.io.FileInputStream
import java.io.FileOutputStream
import java.io.IOException
import java.net.SocketTimeoutException
import java.nio.file.Files
import java.nio.file.StandardCopyOption
import java.util.Base64
import java.util.concurrent.atomic.AtomicBoolean

data class Stats(
    val pulled: Int = 0, val pushed: Int = 0, val delLocal: Int = 0, val delRemote: Int = 0,
    val conflicts: Int = 0, val errors: Int = 0,
) {
    val changes get() = pulled + pushed + delLocal + delRemote
    companion object {
        // The PC's "pulled" is what the phone sent, so swap perspectives.
        fun fromServer(o: JSONObject?) = if (o == null) Stats() else Stats(
            pulled = o.optInt("pushed"), pushed = o.optInt("pulled"),
            delLocal = o.optInt("del_remote"), delRemote = o.optInt("del_local"),
            conflicts = o.optInt("conflicts"), errors = o.optInt("errors"),
        )
    }
}

interface SyncListener {
    fun onStatus(text: String) {}
    fun onConnected(peer: Peer, via: String, local: Boolean) {}
    fun onSynced(stats: Stats) {}
    fun onLog(text: String) {}
    fun onSessionStart() {}
    fun onSessionEnd() {}
    /** A clip from the PC (or relayed from another device through the PC). */
    fun onClip(clip: ClipItem) {}
}

/**
 * Phone side of the protocol. The PC decides what to transfer (it keeps the
 * merge base); the phone lists its files and executes the PC's requests,
 * verifying every path and hash itself.
 */
class SyncClient(
    root: File,
    stateDir: File,
    private val store: PeerStore,
    private val deviceId: String,
    private val deviceName: String,
    private val listener: SyncListener,
) {
    val index = FileIndex(root, File(stateDir, "hashcache.json"))
    private val root = root
    @Volatile private var conn: FrameConn? = null
    private val waiting = AtomicBoolean(false)
    private val pokeRequested = AtomicBoolean(false)
    @Volatile private var dropRequested = false
    @Volatile private var connectedLocal = true
    @Volatile private var syncedSig = ""

    /** Whether the PC should push its clipboard automatically. */
    @Volatile var clipAuto: Boolean = true
        set(v) {
            field = v
            if (pcSupportsClipboard) conn?.let { c -> try { c.send(msg("clipcfg", "auto" to v)) } catch (_: IOException) {} }
        }

    val isConnected: Boolean get() = conn != null

    /** False when the PC runs an older syncme without clipboard support. */
    @Volatile var pcSupportsClipboard: Boolean = false
        private set

    private fun clipConn(): FrameConn {
        val c = conn ?: throw IOException("Not connected to the PC")
        if (!pcSupportsClipboard) throw IOException("Update Syncme on the PC to share the clipboard")
        return c
    }

    /** Sends a clip to the PC. Throws if not connected. */
    fun sendClip(item: ClipItem) {
        val c = clipConn()
        val m = msg("clip", "id" to item.id, "kind" to item.kind, "mime" to item.mime, "h" to item.hash,
            "s" to item.size, "sens" to (if (item.sensitive) true else null))
        if (item.kind == "text") {
            m.put("text", item.text)
            c.send(m)
        } else {
            val d = item.data ?: throw IOException("no image data")
            c.sendWithData(m, d.inputStream(), d.size.toLong())
        }
    }

    /** Asks the PC for its current clipboard; the answer arrives via [SyncListener.onClip]. */
    fun requestPcClip() {
        val c = clipConn()
        c.send(msg("clipget"))
    }

    /** Reads the next message, handling clipboard pushes that can arrive at any time. */
    private fun recvHandled(fc: FrameConn): JSONObject {
        while (true) {
            val m = fc.recv()
            if (m.optString("t") != "clip") return m
            var data: ByteArray? = null
            if (m.optString("kind") == "image") {
                val buf = java.io.ByteArrayOutputStream()
                val res = fc.recvData(buf, ClipItem.MAX_IMAGE.toLong())
                if (res.hash != m.optString("h")) {
                    listener.onLog("clipboard image failed verification")
                    continue
                }
                data = buf.toByteArray()
            }
            try {
                listener.onClip(ClipItem.header(m, data))
            } catch (e: Exception) {
                listener.onLog("clipboard: $e")
            }
        }
    }

    /** Ask for a sync as soon as possible (e.g. after a share). */
    fun requestSync() {
        pokeRequested.set(true)
        val c = conn
        if (c != null && waiting.get()) {
            try { c.send(msg("poke")) } catch (_: IOException) {}
        }
    }

    /** Close the current connection, e.g. when the network changed. */
    fun dropConnection() {
        dropRequested = true
        conn?.close()
    }

    private fun connect(): Pair<FrameConn, Peer> {
        val peer = store.load() ?: throw IOException("not paired")
        listener.onStatus("Looking for ${peer.name}…")
        val known = buildList { if (peer.lastAddr.isNotEmpty()) add(peer.lastAddr); addAll(peer.addrs) }
        val c = Connector.connect(peer.id, known, peer.public, peer.relay)
        val fc = FrameConn(c.socket)
        try {
            c.socket.soTimeout = 30_000
            fc.send(msg("hello", "v" to PROTO_VERSION, "id" to deviceId, "name" to deviceName))
            val ch = fc.recv()
            if (ch.optString("t") != "challenge") throw ProtocolException("unexpected ${ch.optString("t")}")
            fc.send(msg("auth", "mac" to Crypto.authMac(peer.secret, ch.getString("nonce"), peer.id, deviceId)))
            val w = fc.recv()
            if (w.optString("t") == "error") throw RemoteException(w.optString("error"))
            if (w.optString("t") != "welcome") throw ProtocolException("unexpected ${w.optString("t")}")
            connectedLocal = c.local
            val updated = peer.withWelcome(w, c.via)
            if (updated != peer) store.save(updated)
            val caps = w.optJSONArray("caps")
            pcSupportsClipboard = caps != null && (0 until caps.length()).any { caps.optString(it) == "clip" }
            if (pcSupportsClipboard) fc.send(msg("clipcfg", "auto" to clipAuto))
            else listener.onLog("PC runs an older syncme without clipboard support")
            listener.onConnected(updated, c.via, c.local)
            return fc to updated
        } catch (e: Exception) {
            fc.close()
            throw e
        }
    }

    /** Connect, sync once, disconnect. */
    fun syncOnce(): Stats {
        val (fc, _) = connect()
        fc.use {
            conn = fc
            try {
                val st = session(fc)
                fc.send(msg("bye"))
                return st
            } finally {
                conn = null
            }
        }
    }

    /**
     * Stay connected and keep syncing until [keepRunning] returns false or
     * the connection fails (the caller reconnects with backoff).
     */
    fun runConnected(keepRunning: () -> Boolean) {
        dropRequested = false
        val (fc, peer) = connect()
        conn = fc
        val watcher = Thread {
            while (!Thread.currentThread().isInterrupted) {
                try { Thread.sleep(3000) } catch (_: InterruptedException) { break }
                val synced = syncedSig
                if (waiting.get() && synced.isNotEmpty()) {
                    val now = index.quickSig()
                    if (now != synced) { syncedSig = now; requestSync() }
                }
            }
        }.apply { isDaemon = true; start() }
        try {
            var lanCheck = 0
            while (keepRunning() && !dropRequested) {
                pokeRequested.set(false)
                session(fc)
                // Prefer local: when on an internet path, look for the PC on the LAN now and then.
                if (!connectedLocal && ++lanCheck % 10 == 0 && Connector.discover(800).any { it.id == peer.id }) {
                    listener.onLog("PC found on local network, switching")
                    return
                }
                while (keepRunning() && !dropRequested && !pokeRequested.get()) {
                    fc.socket.soTimeout = 75_000
                    waiting.set(true)
                    val w = try {
                        fc.send(msg("wait"))
                        if (pokeRequested.get()) fc.send(msg("poke")) // raced with requestSync()
                        recvHandled(fc)
                    } finally {
                        waiting.set(false)
                    }
                    if (w.optString("t") != "wake") throw ProtocolException("unexpected ${w.optString("t")}")
                    if (w.optString("reason") != "timeout") break
                }
            }
        } catch (e: IOException) {
            if (!dropRequested) throw e
        } finally {
            watcher.interrupt()
            conn = null
            try { fc.send(msg("bye")) } catch (_: Exception) {}
            fc.close()
        }
    }

    /** One sync session: send our listing, then serve the PC's requests. */
    private fun session(fc: FrameConn): Stats {
        listener.onSessionStart()
        try {
            return sessionInner(fc)
        } finally {
            listener.onSessionEnd()
        }
    }

    private fun sessionInner(fc: FrameConn): Stats {
        listener.onStatus("Syncing…")
        val files = index.scan()
        val arr = JSONArray()
        for ((p, e) in files) arr.put(JSONObject().put("p", p).put("s", e.size).put("m", e.mtime).put("h", e.hash))
        fc.socket.soTimeout = 5 * 60_000
        fc.send(msg("sync", "files" to arr))
        while (true) {
            val m = recvHandled(fc)
            when (m.optString("t")) {
                "get" -> handleGet(fc, m, files)
                "put" -> handlePut(fc, m)
                "del" -> handleDel(fc, m)
                "mv" -> handleMv(fc, m)
                "wake" -> {} // stray
                "done" -> {
                    index.save()
                    if (m.has("error")) throw RemoteException(m.getString("error"))
                    val st = Stats.fromServer(m.optJSONObject("stats"))
                    syncedSig = index.quickSig()
                    listener.onSynced(st)
                    return st
                }
                "error" -> throw RemoteException(m.optString("error"))
                else -> throw ProtocolException("unexpected ${m.optString("t")}")
            }
        }
    }

    private fun err(fc: FrameConn, text: String) {
        listener.onLog(text)
        fc.send(msg("error", "error" to text))
    }

    private fun handleGet(fc: FrameConn, m: JSONObject, files: Map<String, Entry>) {
        val p = m.optString("p")
        val e = files[p]
        if (!FileIndex.validPath(p) || e == null) return err(fc, "no such file: $p")
        val f = index.file(p)
        val input = try { FileInputStream(f) } catch (ex: IOException) { return err(fc, "cannot read $p") }
        input.use {
            fc.sendWithData(msg("file", "p" to p, "s" to e.size, "m" to e.mtime, "h" to e.hash), it, e.size)
        }
    }

    private fun handlePut(fc: FrameConn, m: JSONObject) {
        val p = m.optString("p")
        val size = m.optLong("s", 0)
        val mtime = m.optLong("m", 0)
        val hash = m.optString("h")
        val old = m.optString("old", "")
        val ok = FileIndex.validPath(p) && size >= 0
        val dest = if (ok) index.file(p) else null
        var tmp: File? = null
        var out: FileOutputStream? = null
        var prepError: String? = if (ok) null else "invalid path"
        if (dest != null) {
            try {
                val dir = dest.parentFile!!
                if (!dir.isDirectory && !dir.mkdirs()) throw IOException("cannot create folder")
                tmp = File(dir, ".syncme-tmp-" + Crypto.randomHex(6))
                out = FileOutputStream(tmp)
            } catch (ex: IOException) {
                prepError = "cannot write: ${ex.message}"
            }
        }
        // Always drain the data so the stream stays aligned.
        val res = try {
            fc.recvData(out, size)
        } catch (ex: IOException) {
            out?.close(); tmp?.delete(); throw ex
        }
        try {
            out?.fd?.sync()
        } catch (_: Exception) {}
        out?.close()
        try {
            if (prepError != null) return err(fc, prepError)
            if (res.writeError != null) return err(fc, "write failed: ${res.writeError.message}")
            if (res.hash != hash) return err(fc, "hash mismatch for $p")
            if (dest!!.exists()) {
                if (!dest.isFile) return err(fc, "$p is a folder on the phone")
                val cur = index.currentHash(p)
                if (cur != old && cur != hash) return err(fc, "$p changed on phone during sync")
            }
            tmp!!.setLastModified(mtime)
            Files.move(tmp.toPath(), dest.toPath(), StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE)
            tmp = null
            index.remember(p, dest, hash)
            fc.send(msg("ok"))
        } finally {
            tmp?.delete()
        }
    }

    private fun handleDel(fc: FrameConn, m: JSONObject) {
        val p = m.optString("p")
        if (!FileIndex.validPath(p)) return err(fc, "invalid path")
        val cur = try { index.currentHash(p) } catch (_: IOException) { null }
        if (cur == null) return fc.send(msg("ok")) // already gone
        if (cur != m.optString("h")) return err(fc, "$p changed on phone during sync")
        if (!index.moveToTrash(p)) return err(fc, "cannot delete $p")
        fc.send(msg("ok"))
    }

    private fun handleMv(fc: FrameConn, m: JSONObject) {
        val p = m.optString("p")
        val to = m.optString("to")
        if (!FileIndex.validPath(p) || !FileIndex.validPath(to)) return err(fc, "invalid path")
        val cur = try { index.currentHash(p) } catch (_: IOException) { null }
        if (cur != m.optString("h")) return err(fc, "$p changed on phone during sync")
        val dst = index.file(to)
        if (dst.exists()) return err(fc, "$to already exists")
        dst.parentFile?.mkdirs()
        if (!index.file(p).renameTo(dst)) return err(fc, "cannot rename $p")
        index.remember(to, dst, cur!!)
        fc.send(msg("ok"))
    }

    companion object {
        /**
         * Pairs with a PC. [onCode] receives the 6-digit verification code the
         * user must compare with the PC's screen. Blocks until the PC user
         * accepts or rejects. Returns the stored peer.
         */
        fun pair(target: PairTarget, token: String, deviceId: String, deviceName: String, onCode: (String) -> Unit): Peer {
            var c: Connected? = null
            val errors = ArrayList<String>()
            Connector.race(target.addrs, target.id, 4000, errors)?.let { c = Connected(it.first, it.second, true) }
            if (c == null) {
                // The QR code's addresses may be stale or on another interface: ask the LAN.
                val found = Connector.discover(2000).filter { it.id == target.id }.map { it.addr }.filter { it !in target.addrs }
                if (found.isNotEmpty()) Connector.race(found, target.id, 4000, errors)?.let { c = Connected(it.first, it.second, true) }
            }
            if (c == null && target.relay.isNotBlank()) {
                try {
                    c = Connected(Connector.viaRelay(target.relay, target.id), "relay", false)
                } catch (e: Exception) {
                    errors.add("relay ${target.relay} — ${Connector.describe(e)}")
                }
            }
            val conn = c ?: throw IOException(buildString {
                append("Could not reach ${target.name}.\n\n")
                if (target.addrs.isEmpty() && errors.isEmpty()) append("The pairing code contains no addresses.\n")
                for (e in errors) append("• ").append(e).append('\n')
                append("\nPhone and PC must be on the same Wi-Fi (not a guest network). ")
                append("On the PC, allow TCP $DEFAULT_PORT and UDP $DISCOVERY_PORT in the firewall.")
            })
            FrameConn(conn.socket).use { fc ->
                conn.socket.soTimeout = 30_000
                fc.send(msg("pair", "v" to PROTO_VERSION, "id" to deviceId, "name" to deviceName, "token" to token.trim().uppercase()))
                val r = fc.recv()
                if (r.optString("t") == "error") throw RemoteException(r.optString("error"))
                if (r.optString("t") != "pair_pending") throw ProtocolException("unexpected ${r.optString("t")}")
                // Show OUR computation; the PC shows its own. They only match if
                // both sides saw the same PC key, phone id and token.
                onCode(Crypto.pairCode(target.id, deviceId, token))
                conn.socket.soTimeout = 6 * 60_000
                val done = try { fc.recv() } catch (e: SocketTimeoutException) { throw IOException("timed out waiting for confirmation on the PC") }
                if (done.optString("t") == "error") throw RemoteException(done.optString("error"))
                if (done.optString("t") != "paired") throw ProtocolException("unexpected ${done.optString("t")}")
                val peer = Peer(
                    id = target.id,
                    name = done.optString("name", target.name),
                    secret = Base64.getDecoder().decode(done.getString("secret")),
                    addrs = emptyList(),
                ).withWelcome(done, conn.via)
                return peer
            }
        }
    }
}
