package com.cosmickittu.syncme.core

import org.json.JSONArray
import org.json.JSONObject
import java.net.URI
import java.net.URLDecoder
import java.util.Base64

/** The paired PC as stored on the phone. */
data class Peer(
    val id: String,
    val name: String,
    val secret: ByteArray,
    val addrs: List<String>,
    val public: String = "",
    val relay: String = "",
    val lastAddr: String = "",
) {
    fun toJson(): JSONObject = JSONObject()
        .put("id", id).put("name", name)
        .put("secret", Base64.getEncoder().encodeToString(secret))
        .put("addrs", JSONArray(addrs))
        .put("public", public).put("relay", relay).put("lastAddr", lastAddr)

    /** Merge the latest addresses the PC reported in its welcome message. */
    fun withWelcome(w: JSONObject, connectedVia: String): Peer = copy(
        name = w.optString("name", name).ifEmpty { name },
        addrs = w.optJSONArray("addrs")?.let { a -> (0 until a.length()).map { a.getString(it) } } ?: addrs,
        public = w.optString("public", ""),
        relay = w.optString("relay", ""),
        lastAddr = if (connectedVia.startsWith("relay")) lastAddr else connectedVia,
    )

    override fun equals(other: Any?) = other is Peer && other.toJson().toString() == toJson().toString()
    override fun hashCode() = id.hashCode()

    companion object {
        fun fromJson(o: JSONObject) = Peer(
            id = o.getString("id"),
            name = o.optString("name", "PC"),
            secret = Base64.getDecoder().decode(o.getString("secret")),
            addrs = o.optJSONArray("addrs")?.let { a -> (0 until a.length()).map { a.getString(it) } } ?: emptyList(),
            public = o.optString("public", ""),
            relay = o.optString("relay", ""),
            lastAddr = o.optString("lastAddr", ""),
        )
    }
}

/** Where to find a PC to pair with: from a QR code / link, or LAN discovery. */
data class PairTarget(val id: String, val name: String, val addrs: List<String>, val relay: String = "", val token: String = "") {
    companion object {
        /** Parses syncme://pair?id=..&n=..&a=ip:port,..&t=TOKEN&r=relay */
        fun parse(link: String): PairTarget {
            val uri = URI(link.trim())
            require(uri.scheme == "syncme" && (uri.host == "pair" || uri.schemeSpecificPart.startsWith("//pair"))) { "Not a Syncme pairing code" }
            val q = HashMap<String, String>()
            for (part in (uri.rawQuery ?: "").split("&")) {
                if (part.isEmpty()) continue
                val i = part.indexOf('=')
                val k = URLDecoder.decode(if (i < 0) part else part.substring(0, i), "UTF-8")
                val v = if (i < 0) "" else URLDecoder.decode(part.substring(i + 1), "UTF-8")
                q[k] = v
            }
            val id = q["id"]?.lowercase() ?: ""
            require(id.length == 64 && id.all { it in "0123456789abcdef" }) { "Pairing code has no valid PC fingerprint" }
            return PairTarget(
                id = id,
                name = q["n"] ?: "PC",
                addrs = (q["a"] ?: "").split(",").map { it.trim() }.filter { it.isNotEmpty() },
                relay = q["r"] ?: "",
                token = q["t"] ?: "",
            )
        }
    }
}

/** Persistence for the paired PC (SharedPreferences on Android, files in tests). */
interface PeerStore {
    fun load(): Peer?
    fun save(p: Peer?)
}
