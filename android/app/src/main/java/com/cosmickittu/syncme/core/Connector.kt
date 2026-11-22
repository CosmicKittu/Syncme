package com.cosmickittu.syncme.core

import org.json.JSONObject
import java.io.IOException
import java.io.InputStream
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.Inet4Address
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.NetworkInterface
import java.net.Socket
import java.net.SocketTimeoutException
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLSocket

data class DiscoveredPc(val id: String, val name: String, val host: String, val port: Int) {
    val addr get() = hostPort(host, port)
}

fun hostPort(host: String, port: Int) = if (host.contains(':')) "[$host]:$port" else "$host:$port"

fun splitHostPort(a: String): Pair<String, Int> {
    if (a.startsWith("[")) {
        val end = a.indexOf(']')
        return a.substring(1, end) to a.substring(end + 2).toInt()
    }
    val i = a.lastIndexOf(':')
    if (i < 0) return a to DEFAULT_PORT
    return a.substring(0, i) to a.substring(i + 1).toInt()
}

class Connected(val socket: SSLSocket, val via: String, val local: Boolean)

/**
 * Finds the PC. Order of preference:
 *  1. LAN: UDP broadcast discovery + addresses the PC reported (incl. VPNs like Tailscale)
 *  2. Internet direct: the PC's public address (UPnP / port forward)
 *  3. Relay: end-to-end encrypted tunnel through a syncme-relay
 * TLS with the pinned PC key is required on every path.
 */
object Connector {
    var log: (String) -> Unit = {}
    @Volatile var discoveryEnabled = true

    fun discover(timeoutMs: Int = 1500, port: Int = DISCOVERY_PORT): List<DiscoveredPc> {
        val found = LinkedHashMap<String, DiscoveredPc>()
        try {
            DatagramSocket().use { sock ->
                sock.broadcast = true
                sock.soTimeout = 250
                val probe = "SYNCME?$PROTO_VERSION".toByteArray()
                val targets = broadcastAddresses()
                fun sendProbes() {
                    for (t in targets) {
                        try { sock.send(DatagramPacket(probe, probe.size, t, port)) } catch (_: IOException) {}
                    }
                }
                sendProbes()
                val deadline = System.currentTimeMillis() + timeoutMs
                var resent = false
                val buf = ByteArray(2048)
                while (System.currentTimeMillis() < deadline) {
                    if (!resent && System.currentTimeMillis() > deadline - timeoutMs / 2) { sendProbes(); resent = true }
                    val p = DatagramPacket(buf, buf.size)
                    try { sock.receive(p) } catch (_: SocketTimeoutException) { continue }
                    try {
                        val o = JSONObject(String(p.data, 0, p.length, Charsets.UTF_8))
                        if (o.optInt("syncme") < 1) continue
                        val id = o.getString("id")
                        val host = p.address.hostAddress ?: continue
                        found.putIfAbsent(id, DiscoveredPc(id, o.optString("name", "PC"), host.substringBefore('%'), o.optInt("port", DEFAULT_PORT)))
                    } catch (_: Exception) {}
                }
            }
        } catch (e: IOException) {
            log("discovery failed: ${e.message}")
        }
        return found.values.toList()
    }

    private fun broadcastAddresses(): List<InetAddress> {
        val out = LinkedHashSet<InetAddress>()
        out.add(InetAddress.getByName("255.255.255.255"))
        try {
            for (ni in NetworkInterface.getNetworkInterfaces()) {
                if (!ni.isUp || ni.isLoopback) continue
                for (ia in ni.interfaceAddresses) {
                    if (ia.address is Inet4Address) ia.broadcast?.let { out.add(it) }
                }
            }
        } catch (_: Exception) {}
        if (System.getProperty("syncme.test") != null) out.add(InetAddress.getLoopbackAddress())
        return out.toList()
    }

    /** Opens a pinned TLS connection to the PC using the best available path. */
    fun connect(id: String, knownAddrs: List<String>, public: String, relay: String): Connected {
        val lan = LinkedHashSet<String>()
        if (discoveryEnabled) discover().filter { it.id == id }.forEach { lan.add(it.addr) }
        lan.addAll(knownAddrs.filter { it.isNotBlank() })
        lan.remove(public)
        race(lan.toList(), id, 4000)?.let { return Connected(it.first, it.second, true) }
        if (public.isNotBlank()) {
            race(listOf(public), id, 8000)?.let { return Connected(it.first, it.second, false) }
        }
        if (relay.isNotBlank()) {
            log("trying relay $relay")
            return Connected(viaRelay(relay, id), "relay $relay", false)
        }
        throw IOException("PC not reachable (not on this network, and no internet route configured)")
    }

    /** Tries all addresses at once; the first successful TLS handshake wins. */
    fun race(addrs: List<String>, id: String, timeoutMs: Int): Pair<SSLSocket, String>? {
        if (addrs.isEmpty()) return null
        val lock = Object()
        var winner: Pair<SSLSocket, String>? = null
        var closed = false
        val done = CountDownLatch(addrs.size)
        val first = CountDownLatch(1)
        for (a in addrs) {
            Thread {
                try {
                    val (h, p) = splitHostPort(a)
                    val s = Socket()
                    s.connect(InetSocketAddress(h, p), timeoutMs)
                    s.soTimeout = timeoutMs * 2
                    s.tcpNoDelay = true
                    s.keepAlive = true
                    val tls = Crypto.tlsWrap(s, h, p, id)
                    val keep = synchronized(lock) {
                        if (!closed && winner == null) { winner = tls to a; true } else false
                    }
                    if (keep) first.countDown() else tls.close()
                } catch (e: Exception) {
                    log("$a: ${e.message}")
                } finally {
                    done.countDown()
                    if (done.count == 0L) first.countDown()
                }
            }.apply { isDaemon = true }.start()
        }
        first.await(timeoutMs * 3L, TimeUnit.MILLISECONDS)
        return synchronized(lock) { closed = true; winner }
    }

    fun viaRelay(relay: String, id: String): SSLSocket {
        val (h, p) = splitHostPort(relay)
        val s = Socket()
        s.connect(InetSocketAddress(h, p), 10_000)
        s.soTimeout = 20_000
        s.keepAlive = true
        val inp = s.getInputStream()
        val greet = readLine(inp)
        if (!greet.startsWith("NONCE ")) throw IOException("not a syncme relay")
        s.getOutputStream().write("CONNECT $id\n".toByteArray())
        s.getOutputStream().flush()
        val r = readLine(inp)
        if (r != "OK") throw IOException("relay: $r")
        return Crypto.tlsWrap(s, h, p, id)
    }

    /** Byte-at-a-time so no TLS bytes are swallowed. */
    private fun readLine(inp: InputStream): String {
        val sb = StringBuilder()
        while (sb.length < 1024) {
            val c = inp.read()
            if (c < 0) throw IOException("relay closed connection")
            if (c == '\n'.code) return sb.toString().trimEnd('\r')
            sb.append(c.toChar())
        }
        throw IOException("relay line too long")
    }
}
