package com.cosmickittu.syncme

import android.app.Activity
import android.app.PendingIntent
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.service.quicksettings.Tile
import android.service.quicksettings.TileService
import androidx.core.content.IntentCompat
import com.cosmickittu.syncme.core.ClipItem
import kotlin.concurrent.thread

/**
 * Invisible screen that reads the clipboard and sends it to the PC.
 * Android only lets an app read the clipboard while it has focus, so the
 * notification button and the Quick Settings tile open this briefly.
 */
class ClipSendActivity : Activity() {
    private var handled = false

    override fun onWindowFocusChanged(hasFocus: Boolean) {
        super.onWindowFocusChanged(hasFocus)
        if (!hasFocus || handled) return
        handled = true
        try {
            ClipActions.send(this, ClipBridge.readClipboard(this))
        } catch (e: Exception) {
            toast(this, e.message ?: getString(R.string.clip_failed, ""))
        }
        finish()
    }
}

/** Share sheet entry "Syncme clipboard": puts shared text or an image on the PC clipboard. */
class ClipShareActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val text = intent.getStringExtra(Intent.EXTRA_TEXT)
        val uri = IntentCompat.getParcelableExtra(intent, Intent.EXTRA_STREAM, Uri::class.java)
        val app = applicationContext
        if (uri == null) {
            try {
                ClipActions.send(app, ClipItem.text(text ?: ""))
            } catch (e: Exception) {
                toast(app, e.message ?: "")
            }
            finish()
            return
        }
        val type = intent.type?.takeIf { it.startsWith("image/") && it != "image/*" } ?: contentResolver.getType(uri) ?: "image/png"
        thread {
            try {
                ClipActions.send(app, ClipItem.image(ClipBridge.readUri(app, uri), type))
            } catch (e: Exception) {
                toast(app, e.message ?: "")
            }
            runOnUiThread { finish() }
        }
    }
}

/** Quick Settings tile: "Send clipboard". */
class ClipTileService : TileService() {
    override fun onStartListening() {
        super.onStartListening()
        qsTile?.let {
            it.state = if (SyncService.activeClient?.isConnected == true) Tile.STATE_ACTIVE else Tile.STATE_INACTIVE
            it.updateTile()
        }
    }

    override fun onClick() {
        super.onClick()
        val i = Intent(this, ClipSendActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        if (Build.VERSION.SDK_INT >= 34) {
            startActivityAndCollapse(PendingIntent.getActivity(this, 2, i, PendingIntent.FLAG_IMMUTABLE))
        } else {
            @Suppress("DEPRECATION")
            startActivityAndCollapse(i)
        }
    }
}
