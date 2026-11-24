package com.cosmickittu.syncme

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import android.os.Build
import android.os.Environment
import android.provider.Settings
import androidx.core.content.ContextCompat
import com.cosmickittu.syncme.core.Crypto
import com.cosmickittu.syncme.core.Peer
import com.cosmickittu.syncme.core.PeerStore
import com.cosmickittu.syncme.core.Stats
import org.json.JSONObject
import java.io.File
import java.util.concurrent.CopyOnWriteArraySet

/** App-private storage for this phone's id and the paired PC (never backed up). */
class Prefs(ctx: Context) : PeerStore {
    private val sp = ctx.applicationContext.getSharedPreferences("syncme", Context.MODE_PRIVATE)
    private val appCtx = ctx.applicationContext

    val deviceId: String
        @Synchronized get() = sp.getString("device_id", null)
            ?: Crypto.randomHex(16).also { sp.edit().putString("device_id", it).commit() }

    val deviceName: String
        get() {
            val n = try { Settings.Global.getString(appCtx.contentResolver, Settings.Global.DEVICE_NAME) } catch (_: Exception) { null }
            return n?.takeIf { it.isNotBlank() } ?: "${Build.MANUFACTURER} ${Build.MODEL}"
        }

    var clipAuto: Boolean
        get() = sp.getBoolean("clip_auto", true)
        set(v) { sp.edit().putBoolean("clip_auto", v).apply() }

    override fun load(): Peer? = sp.getString("peer", null)?.let {
        try { Peer.fromJson(JSONObject(it)) } catch (_: Exception) { null }
    }

    override fun save(p: Peer?) {
        val e = sp.edit()
        if (p == null) e.remove("peer") else e.putString("peer", p.toJson().toString())
        e.commit()
    }
}

object Storage {
    /** The synced folder: /storage/emulated/0/Download/syncme */
    fun folder(): File = File(Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS), "syncme")

    fun hasAccess(ctx: Context): Boolean =
        if (Build.VERSION.SDK_INT >= 30) Environment.isExternalStorageManager()
        else ContextCompat.checkSelfPermission(ctx, Manifest.permission.WRITE_EXTERNAL_STORAGE) == PackageManager.PERMISSION_GRANTED
}

/** In-process status shared between the service, notification and UI. */
object SyncState {
    @Volatile var status: String = "Not running"
    @Volatile var via: String = ""
    @Volatile var connected: Boolean = false
    @Volatile var lastSync: Long = 0
    @Volatile var lastStats: Stats? = null

    private val listeners = CopyOnWriteArraySet<() -> Unit>()
    fun addListener(l: () -> Unit) = listeners.add(l)
    fun removeListener(l: () -> Unit) = listeners.remove(l)
    fun changed() = listeners.forEach { it() }
}
