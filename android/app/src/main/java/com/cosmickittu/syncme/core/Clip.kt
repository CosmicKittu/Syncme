package com.cosmickittu.syncme.core

import org.json.JSONObject

/** One clipboard item travelling between PC and devices. */
data class ClipItem(
    val id: String,
    val kind: String,          // "text" | "image" | "none" (empty PC clipboard)
    val mime: String,
    val text: String = "",
    val data: ByteArray? = null,
    val hash: String = "",
    val sensitive: Boolean = false,
    val from: String = "",
    val reply: Boolean = false, // answer to "Get PC clipboard"
) {
    val size: Int get() = if (kind == "text") text.toByteArray(Charsets.UTF_8).size else data?.size ?: 0

    companion object {
        const val MAX_TEXT = 1 shl 20
        const val MAX_IMAGE = 16 shl 20

        fun text(t: String, sensitive: Boolean = false): ClipItem {
            val b = t.toByteArray(Charsets.UTF_8)
            require(b.isNotEmpty()) { "Clipboard is empty" }
            require(b.size <= MAX_TEXT) { "Text is too large (max 1 MB)" }
            return ClipItem(Crypto.randomHex(8), "text", "text/plain", text = t, hash = Crypto.sha256Hex(b), sensitive = sensitive)
        }

        fun image(bytes: ByteArray, mime: String, sensitive: Boolean = false): ClipItem {
            require(bytes.isNotEmpty()) { "Image is empty" }
            require(bytes.size <= MAX_IMAGE) { "Image is too large (max 16 MB)" }
            require(mime.startsWith("image/")) { "Not an image" }
            return ClipItem(Crypto.randomHex(8), "image", mime, data = bytes, hash = Crypto.sha256Hex(bytes), sensitive = sensitive)
        }

        internal fun header(m: JSONObject, data: ByteArray?) = ClipItem(
            id = m.optString("id", Crypto.randomHex(8)),
            kind = m.optString("kind"),
            mime = m.optString("mime"),
            text = m.optString("text", ""),
            data = data,
            hash = m.optString("h", ""),
            sensitive = m.optBoolean("sens", false),
            from = m.optString("from", ""),
            reply = m.optBoolean("reply", false),
        )
    }
}
