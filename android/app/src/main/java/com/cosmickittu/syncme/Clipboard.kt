package com.cosmickittu.syncme

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.net.Uri
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.os.PersistableBundle
import android.widget.Toast
import androidx.core.content.FileProvider
import com.cosmickittu.syncme.core.ClipItem
import org.json.JSONArray
import org.json.JSONObject
import java.io.ByteArrayOutputStream
import java.io.File
import java.io.IOException
import java.util.concurrent.CopyOnWriteArraySet
import kotlin.concurrent.thread

private const val EXTRA_IS_SENSITIVE = "android.content.extra.IS_SENSITIVE" // ClipDescription.EXTRA_IS_SENSITIVE (API 33)

fun toast(ctx: Context, msg: String) {
    val app = ctx.applicationContext
    Handler(Looper.getMainLooper()).post { Toast.makeText(app, msg, Toast.LENGTH_SHORT).show() }
}

fun extForMime(mime: String) = when (mime) {
    "image/jpeg" -> "jpg"
    "image/webp" -> "webp"
    "image/gif" -> "gif"
    "image/bmp" -> "bmp"
    else -> "png"
}

/** Reads and writes the Android clipboard. */
object ClipBridge {
    fun authority(ctx: Context) = "${ctx.packageName}.clipprovider"

    /** Puts a clip on the phone's clipboard. Call on the main thread. */
    fun setClipboard(ctx: Context, item: ClipItem) {
        val cm = ctx.getSystemService(ClipboardManager::class.java)
        val clip = when (item.kind) {
            "text" -> ClipData.newPlainText("Syncme", item.text)
            "image" -> {
                val dir = File(ctx.cacheDir, "clipimg").apply { mkdirs() }
                // keep only the most recent few images for pasting
                dir.listFiles()?.sortedByDescending { it.lastModified() }?.drop(5)?.forEach { it.delete() }
                val f = File(dir, "clip-${item.id}.${extForMime(item.mime)}")
                f.writeBytes(item.data ?: return)
                val uri = FileProvider.getUriForFile(ctx, authority(ctx), f)
                ClipData.newUri(ctx.contentResolver, "Syncme image", uri)
            }
            else -> return
        }
        if (item.sensitive) clip.description.extras = PersistableBundle().apply { putBoolean(EXTRA_IS_SENSITIVE, true) }
        cm.setPrimaryClip(clip)
    }

    /** Reads the phone's clipboard. Only works while one of our windows has focus. */
    fun readClipboard(ctx: Context): ClipItem {
        val cm = ctx.getSystemService(ClipboardManager::class.java)
        val clip = cm.primaryClip
        if (clip == null || clip.itemCount == 0) throw IOException("The clipboard is empty")
        val sensitive = clip.description.extras?.getBoolean(EXTRA_IS_SENSITIVE, false) ?: false
        val item = clip.getItemAt(0)
        val uri = item.uri
        if (uri != null) {
            val type = ctx.contentResolver.getType(uri) ?: clip.description.getMimeType(0)
            if (type != null && type.startsWith("image/")) return ClipItem.image(readUri(ctx, uri), type, sensitive)
        }
        val text = item.text?.toString() ?: item.coerceToText(ctx)?.toString()
        if (text.isNullOrEmpty()) throw IOException("Nothing shareable on the clipboard")
        return ClipItem.text(text, sensitive)
    }

    fun readUri(ctx: Context, uri: Uri): ByteArray {
        val out = ByteArrayOutputStream()
        ctx.contentResolver.openInputStream(uri)?.use { inp ->
            val buf = ByteArray(64 * 1024)
            while (true) {
                val n = inp.read(buf)
                if (n < 0) break
                out.write(buf, 0, n)
                if (out.size() > ClipItem.MAX_IMAGE) throw IOException("Image is too large (max 16 MB)")
            }
        } ?: throw IOException("Cannot open the image")
        return out.toByteArray()
    }
}

/** Sending and requesting clips through the running sync service. */
object ClipActions {
    fun send(ctx: Context, item: ClipItem) {
        val app = ctx.applicationContext
        thread(name = "syncme-clip") {
            val c = SyncService.activeClient
            if (c == null || !c.isConnected) {
                toast(app, app.getString(R.string.clip_not_connected))
                return@thread
            }
            try {
                c.sendClip(item)
                ClipStore.get(app).add(item, ClipStore.OUT)
                toast(app, app.getString(if (item.kind == "image") R.string.clip_sent_image else R.string.clip_sent))
            } catch (e: Exception) {
                toast(app, app.getString(R.string.clip_failed, e.message ?: e.javaClass.simpleName))
            }
        }
    }

    fun requestFromPc(ctx: Context) {
        val app = ctx.applicationContext
        thread(name = "syncme-clipget") {
            val c = SyncService.activeClient
            if (c == null || !c.isConnected) {
                toast(app, app.getString(R.string.clip_not_connected))
                return@thread
            }
            try { c.requestPcClip() } catch (e: Exception) { toast(app, app.getString(R.string.clip_failed, e.message ?: "")) }
        }
    }

    fun setAuto(ctx: Context, auto: Boolean) {
        Prefs(ctx).clipAuto = auto
        thread { SyncService.activeClient?.clipAuto = auto }
    }
}

/** History of clips sent and received, kept in app-private storage. */
class ClipStore private constructor(ctx: Context) {
    data class Entry(
        val id: String, val time: Long, val dir: String, val kind: String, val mime: String,
        val preview: String, val from: String, val hash: String, val file: String,
    )

    private val dir = File(ctx.filesDir, "clips").apply { mkdirs() }
    private val index = File(dir, "index.json")
    private val entries = ArrayList<Entry>()
    private val listeners = CopyOnWriteArraySet<() -> Unit>()

    init {
        try {
            if (index.exists()) {
                val a = JSONArray(index.readText())
                for (i in 0 until a.length()) {
                    val o = a.getJSONObject(i)
                    entries.add(Entry(o.getString("id"), o.getLong("time"), o.getString("dir"), o.getString("kind"),
                        o.optString("mime"), o.optString("preview"), o.optString("from"), o.optString("hash"), o.getString("file")))
                }
            }
        } catch (_: Exception) {
            entries.clear()
        }
    }

    @Synchronized fun list(): List<Entry> = ArrayList(entries)

    fun file(e: Entry) = File(dir, e.file)

    /** Sensitive clips (passwords) are never stored. */
    fun add(item: ClipItem, direction: String) {
        if (item.sensitive || (item.kind != "text" && item.kind != "image")) return
        synchronized(this) {
            val first = entries.firstOrNull()
            if (first != null && first.hash == item.hash && first.dir == direction) return
            val name = if (item.kind == "text") "${item.id}.txt" else "${item.id}.${extForMime(item.mime)}"
            try {
                if (item.kind == "text") File(dir, name).writeText(item.text) else File(dir, name).writeBytes(item.data ?: return)
            } catch (_: IOException) {
                return
            }
            val preview = if (item.kind == "text") item.text.take(300) else ""
            entries.add(0, Entry(item.id, System.currentTimeMillis(), direction, item.kind, item.mime, preview, item.from, item.hash, name))
            while (entries.size > MAX) File(dir, entries.removeAt(entries.size - 1).file).delete()
            saveLocked()
        }
        changed()
    }

    fun toItem(e: Entry): ClipItem? = try {
        if (e.kind == "text") ClipItem.text(file(e).readText()) else ClipItem.image(file(e).readBytes(), e.mime)
    } catch (_: Exception) { null }

    fun delete(e: Entry) {
        synchronized(this) {
            entries.removeAll { it.id == e.id }
            file(e).delete()
            saveLocked()
        }
        changed()
    }

    fun clear() {
        synchronized(this) {
            entries.forEach { File(dir, it.file).delete() }
            entries.clear()
            saveLocked()
        }
        changed()
    }

    private fun saveLocked() {
        val a = JSONArray()
        for (e in entries) a.put(JSONObject().put("id", e.id).put("time", e.time).put("dir", e.dir).put("kind", e.kind)
            .put("mime", e.mime).put("preview", e.preview).put("from", e.from).put("hash", e.hash).put("file", e.file))
        val tmp = File(dir, "index.json.tmp")
        tmp.writeText(a.toString())
        tmp.renameTo(index)
    }

    fun addListener(l: () -> Unit) = listeners.add(l)
    fun removeListener(l: () -> Unit) = listeners.remove(l)
    private fun changed() = listeners.forEach { it() }

    companion object {
        const val IN = "in"
        const val OUT = "out"
        const val MAX = 200
        @Volatile private var inst: ClipStore? = null
        fun get(ctx: Context): ClipStore = inst ?: synchronized(this) {
            inst ?: ClipStore(ctx.applicationContext).also { inst = it }
        }
    }
}
