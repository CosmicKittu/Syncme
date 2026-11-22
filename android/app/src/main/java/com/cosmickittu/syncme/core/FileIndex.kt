package com.cosmickittu.syncme.core

import org.json.JSONObject
import java.io.File
import java.io.FileInputStream
import java.nio.file.Files
import java.nio.file.LinkOption
import java.security.MessageDigest
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

data class Entry(val size: Long, val mtime: Long, val hash: String)

/** Scans the synced folder; content hashes are cached by (size, mtime). */
class FileIndex(val root: File, private val cacheFile: File) {
    private data class Cached(val size: Long, val mtime: Long, val hash: String)

    private val cache = HashMap<String, Cached>()
    private var dirty = false

    init {
        try {
            if (cacheFile.exists()) {
                val o = JSONObject(cacheFile.readText())
                for (k in o.keys()) {
                    val e = o.getJSONObject(k)
                    cache[k] = Cached(e.getLong("s"), e.getLong("m"), e.getString("h"))
                }
            }
        } catch (_: Exception) {
            cache.clear()
        }
    }

    @Synchronized
    fun scan(): Map<String, Entry> {
        val out = HashMap<String, Entry>()
        walk(root, "") { f, rel ->
            val size = f.length()
            val mtime = f.lastModified()
            val c = cache[rel]
            val h = if (c != null && c.size == size && c.mtime == mtime) c.hash else {
                val hh = try { hashFile(f) } catch (_: Exception) { null } ?: return@walk
                cache[rel] = Cached(size, mtime, hh); dirty = true
                hh
            }
            out[rel] = Entry(size, mtime, h)
        }
        if (cache.keys.retainAll(out.keys)) dirty = true
        save()
        return out
    }

    /** Hash of one file using the cache when possible; null if missing. */
    @Synchronized
    fun currentHash(rel: String): String? {
        val f = file(rel)
        if (!f.isFile) return null
        val c = cache[rel]
        if (c != null && c.size == f.length() && c.mtime == f.lastModified()) return c.hash
        val h = hashFile(f)
        cache[rel] = Cached(f.length(), f.lastModified(), h); dirty = true
        return h
    }

    @Synchronized
    fun remember(rel: String, f: File, hash: String) {
        cache[rel] = Cached(f.length(), f.lastModified(), hash); dirty = true
    }

    @Synchronized
    fun save() {
        if (!dirty) return
        val o = JSONObject()
        for ((k, v) in cache) o.put(k, JSONObject().put("s", v.size).put("m", v.mtime).put("h", v.hash))
        cacheFile.parentFile?.mkdirs()
        val tmp = File(cacheFile.path + ".tmp")
        tmp.writeText(o.toString())
        tmp.renameTo(cacheFile)
        dirty = false
    }

    fun file(rel: String) = File(root, rel)

    /** Cheap change detector: names, sizes and mtimes only. */
    fun quickSig(): String {
        val md = MessageDigest.getInstance("SHA-256")
        walk(root, "") { f, rel -> md.update("$rel\u0000${f.length()}\u0000${f.lastModified()}\n".toByteArray()) }
        return hex(md.digest())
    }

    /** Moves a file into .syncme-trash (kept 30 days) instead of deleting it. */
    fun moveToTrash(rel: String): Boolean {
        val src = file(rel)
        val day = SimpleDateFormat("yyyy-MM-dd", Locale.US).format(Date())
        var dst = File(File(File(root, TRASH), day), rel)
        if (dst.exists()) dst = File(dst.path + "." + System.currentTimeMillis())
        dst.parentFile?.mkdirs()
        val ok = src.renameTo(dst)
        if (ok) pruneEmptyDirs(src.parentFile)
        return ok
    }

    fun pruneEmptyDirs(start: File?) {
        var d = start
        val r = root.canonicalFile
        while (d != null && d.canonicalFile != r && d.canonicalPath.startsWith(r.path + File.separator)) {
            val list = d.list()
            if (list == null || list.isNotEmpty() || !d.delete()) return
            d = d.parentFile
        }
    }

    fun cleanTrash(maxAgeDays: Int = 30) {
        val cutoff = System.currentTimeMillis() - maxAgeDays * 86_400_000L
        val fmt = SimpleDateFormat("yyyy-MM-dd", Locale.US)
        File(root, TRASH).listFiles()?.forEach { d ->
            val t = try { fmt.parse(d.name)?.time } catch (_: Exception) { null }
            if (t != null && t < cutoff) d.deleteRecursively()
        }
    }

    companion object {
        const val TRASH = ".syncme-trash"

        // Keep in sync with internal/index/index.go
        fun isIgnored(name: String) =
            name.startsWith(".syncme") || name.startsWith(".pending-") || name.startsWith(".trashed-")

        /** Security boundary: rejects paths that could escape the folder. */
        fun validPath(p: String): Boolean {
            if (p.isEmpty() || p.length > 4096 || p.startsWith("/") || p.contains('\u0000') || p.contains('\\')) return false
            for (seg in p.split("/")) {
                if (seg.isEmpty() || seg == "." || seg == ".." || seg.toByteArray().size > 255 || isIgnored(seg)) return false
            }
            return true
        }

        fun hashFile(f: File): String {
            val md = MessageDigest.getInstance("SHA-256")
            FileInputStream(f).use { inp ->
                val buf = ByteArray(256 * 1024)
                while (true) {
                    val n = inp.read(buf)
                    if (n < 0) break
                    md.update(buf, 0, n)
                }
            }
            return hex(md.digest())
        }

        private fun walk(dir: File, prefix: String, fn: (File, String) -> Unit) {
            val children = dir.listFiles() ?: return
            for (c in children) {
                if (isIgnored(c.name)) continue
                val rel = if (prefix.isEmpty()) c.name else "$prefix/${c.name}"
                if (Files.isSymbolicLink(c.toPath())) continue
                if (Files.isDirectory(c.toPath(), LinkOption.NOFOLLOW_LINKS)) {
                    walk(c, rel, fn)
                } else if (Files.isRegularFile(c.toPath(), LinkOption.NOFOLLOW_LINKS) && validPath(rel)) {
                    fn(c, rel)
                }
            }
        }
    }
}
