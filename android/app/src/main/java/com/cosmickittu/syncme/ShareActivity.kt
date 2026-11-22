package com.cosmickittu.syncme

import android.app.Activity
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.provider.OpenableColumns
import android.widget.Toast
import androidx.core.content.IntentCompat
import com.cosmickittu.syncme.core.FileIndex
import java.io.File
import java.io.FileOutputStream
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import kotlin.concurrent.thread

/**
 * Target of Android's share sheet: files (or text) shared from any app are
 * copied into Download/syncme and a sync is triggered right away.
 */
class ShareActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (!Storage.hasAccess(this)) {
            Toast.makeText(this, R.string.need_access_first, Toast.LENGTH_LONG).show()
            startActivity(Intent(this, MainActivity::class.java))
            finish()
            return
        }
        val uris: List<Uri> = when (intent.action) {
            Intent.ACTION_SEND -> listOfNotNull(IntentCompat.getParcelableExtra(intent, Intent.EXTRA_STREAM, Uri::class.java))
            Intent.ACTION_SEND_MULTIPLE -> IntentCompat.getParcelableArrayListExtra(intent, Intent.EXTRA_STREAM, Uri::class.java) ?: emptyList()
            else -> emptyList()
        }
        val text = if (uris.isEmpty()) intent.getStringExtra(Intent.EXTRA_TEXT) else null
        if (uris.isEmpty() && text.isNullOrEmpty()) {
            Toast.makeText(this, R.string.nothing_to_share, Toast.LENGTH_SHORT).show()
            finish()
            return
        }
        Toast.makeText(this, getString(R.string.copying, maxOf(uris.size, 1)), Toast.LENGTH_SHORT).show()
        val app = applicationContext
        thread(name = "syncme-share") {
            val folder = Storage.folder().apply { mkdirs() }
            var ok = 0
            var failed = 0
            for (u in uris) {
                try {
                    val name = uniqueFile(folder, sanitize(displayName(u)))
                    val tmp = File(folder, ".syncme-tmp-share-" + System.nanoTime())
                    try {
                        val input = contentResolver.openInputStream(u) ?: throw java.io.IOException("cannot open")
                        input.use { inp -> FileOutputStream(tmp).use { out -> inp.copyTo(out, 256 * 1024); out.fd.sync() } }
                        if (!tmp.renameTo(name)) throw java.io.IOException("rename failed")
                        ok++
                    } finally {
                        tmp.delete()
                    }
                } catch (e: Exception) {
                    failed++
                }
            }
            if (text != null) {
                val stamp = SimpleDateFormat("yyyyMMdd-HHmmss", Locale.US).format(Date())
                try {
                    uniqueFile(folder, "shared-text-$stamp.txt").writeText(text); ok++
                } catch (_: Exception) { failed++ }
            }
            runOnUiThread {
                val msg = if (failed == 0) getString(R.string.shared_ok, ok) else getString(R.string.shared_partial, ok, failed)
                Toast.makeText(app, msg, Toast.LENGTH_LONG).show()
                if (Prefs(app).load() != null) SyncService.syncNow(app)
                finish()
            }
        }
    }

    private fun displayName(u: Uri): String {
        try {
            contentResolver.query(u, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { c ->
                if (c.moveToFirst()) c.getString(0)?.let { if (it.isNotBlank()) return it }
            }
        } catch (_: Exception) {}
        return u.lastPathSegment?.substringAfterLast('/') ?: "shared-file"
    }

    companion object {
        fun sanitize(name: String): String {
            var n = name.replace(Regex("[/\\\\\u0000-\u001f]"), "_").trim()
            while (n.startsWith(".")) n = n.substring(1) // no hidden / reserved names
            if (n.isEmpty() || FileIndex.isIgnored(n)) n = "shared-file"
            while (n.toByteArray().size > 200) n = n.substring(1)
            return n
        }

        fun uniqueFile(dir: File, name: String): File {
            var f = File(dir, name)
            if (!f.exists()) return f
            val dot = name.lastIndexOf('.')
            val stem = if (dot > 0) name.substring(0, dot) else name
            val ext = if (dot > 0) name.substring(dot) else ""
            var i = 1
            while (f.exists()) f = File(dir, "$stem (${i++})$ext")
            return f
        }
    }
}
