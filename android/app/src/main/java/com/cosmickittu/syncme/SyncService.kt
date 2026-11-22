package com.cosmickittu.syncme

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.ConnectivityManager
import android.net.Network
import android.os.Build
import android.os.IBinder
import android.os.PowerManager
import android.util.Log
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import com.cosmickittu.syncme.core.Connector
import com.cosmickittu.syncme.core.Peer
import com.cosmickittu.syncme.core.RemoteException
import com.cosmickittu.syncme.core.Stats
import com.cosmickittu.syncme.core.SyncClient
import com.cosmickittu.syncme.core.SyncListener

/**
 * Keeps a connection to the paired PC and syncs whenever either side
 * changes. Reconnects with backoff and re-evaluates the route (LAN first)
 * whenever the network changes.
 */
class SyncService : Service() {
    private lateinit var prefs: Prefs
    @Volatile private var running = false
    private var worker: Thread? = null
    @Volatile private var client: SyncClient? = null
    private val wake = java.lang.Object()
    private var lastNetwork: Network? = null
    private var wakeLock: PowerManager.WakeLock? = null

    private val netCallback = object : ConnectivityManager.NetworkCallback() {
        override fun onAvailable(network: Network) {
            val changed = lastNetwork != null && lastNetwork != network
            lastNetwork = network
            if (changed) {
                client?.dropConnection() // re-pick the best route (e.g. Wi-Fi came back)
            }
            kick()
        }
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        prefs = Prefs(this)
        val nm = getSystemService(NotificationManager::class.java)
        nm.createNotificationChannel(NotificationChannel(CHANNEL, getString(R.string.channel_name), NotificationManager.IMPORTANCE_LOW))
        Connector.log = { Log.d(TAG, it) }
        wakeLock = getSystemService(PowerManager::class.java).newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "syncme:session")
            .apply { setReferenceCounted(false) }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        ServiceCompat.startForeground(
            this, NOTIF_ID, buildNotification(SyncState.status),
            if (Build.VERSION.SDK_INT >= 34) ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE else 0,
        )
        when (intent?.action) {
            ACTION_STOP -> { stopSelf(); return START_NOT_STICKY }
            ACTION_SYNC -> { client?.requestSync(); kick() }
        }
        if (worker == null) {
            running = true
            try {
                getSystemService(ConnectivityManager::class.java).registerDefaultNetworkCallback(netCallback)
            } catch (_: Exception) {}
            worker = Thread(::loop, "syncme-worker").apply { start() }
        }
        return START_STICKY
    }

    override fun onDestroy() {
        running = false
        try { getSystemService(ConnectivityManager::class.java).unregisterNetworkCallback(netCallback) } catch (_: Exception) {}
        client?.dropConnection()
        worker?.interrupt()
        worker = null
        wakeLock?.let { if (it.isHeld) it.release() }
        setStatus("Stopped", connected = false)
        super.onDestroy()
    }

    private fun kick() = synchronized(wake) { wake.notifyAll() }

    private fun pause(ms: Long) {
        try { synchronized(wake) { wake.wait(ms) } } catch (_: InterruptedException) {}
    }

    private val listener = object : SyncListener {
        override fun onStatus(text: String) = setStatus(text)
        override fun onConnected(peer: Peer, via: String, local: Boolean) {
            SyncState.via = if (local) "local network" else if (via.startsWith("relay")) "relay" else "internet"
            setStatus("Connected to ${peer.name} (${SyncState.via})", connected = true)
        }
        override fun onSynced(stats: Stats) {
            SyncState.lastSync = System.currentTimeMillis()
            SyncState.lastStats = stats
            val peer = prefs.load()
            setStatus("Up to date with ${peer?.name ?: "PC"} (${SyncState.via})", connected = true)
        }
        override fun onLog(text: String) { Log.i(TAG, text) }
        override fun onSessionStart() { wakeLock?.acquire(10 * 60 * 1000L) }
        override fun onSessionEnd() { wakeLock?.let { if (it.isHeld) it.release() } }
    }

    private fun loop() {
        var backoff = 2_000L
        while (running) {
            if (prefs.load() == null) {
                setStatus("Not paired", connected = false)
                stopSelf()
                return
            }
            if (!Storage.hasAccess(this)) {
                setStatus("Needs file access — open Syncme", connected = false)
                pause(15_000)
                continue
            }
            val folder = Storage.folder()
            folder.mkdirs()
            val c = client ?: SyncClient(folder, filesDir, prefs, prefs.deviceId, prefs.deviceName, listener).also { client = it }
            try {
                c.index.cleanTrash()
                c.runConnected { running }
                backoff = 2_000L
            } catch (e: RemoteException) {
                Log.w(TAG, "remote: ${e.message}")
                setStatus("PC refused: ${e.message}", connected = false)
                backoff = 60_000L
            } catch (e: Exception) {
                Log.w(TAG, "connection: $e")
                setStatus("Waiting for PC… (${e.message ?: e.javaClass.simpleName})", connected = false)
            }
            if (!running) break
            pause(backoff)
            backoff = (backoff * 2).coerceAtMost(60_000L)
        }
    }

    private fun setStatus(text: String, connected: Boolean? = null) {
        SyncState.status = text
        if (connected != null) SyncState.connected = connected
        SyncState.changed()
        if (running) getSystemService(NotificationManager::class.java).notify(NOTIF_ID, buildNotification(text))
    }

    private fun buildNotification(text: String): Notification {
        val open = PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE)
        val sync = PendingIntent.getService(this, 1, Intent(this, SyncService::class.java).setAction(ACTION_SYNC), PendingIntent.FLAG_IMMUTABLE)
        return NotificationCompat.Builder(this, CHANNEL)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle(getString(R.string.app_name))
            .setContentText(text)
            .setContentIntent(open)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setSilent(true)
            .addAction(0, getString(R.string.sync_now), sync)
            .build()
    }

    companion object {
        private const val TAG = "Syncme"
        private const val CHANNEL = "sync"
        private const val NOTIF_ID = 1
        const val ACTION_SYNC = "com.cosmickittu.syncme.SYNC"
        const val ACTION_STOP = "com.cosmickittu.syncme.STOP"

        fun start(ctx: Context) {
            ContextCompat.startForegroundService(ctx, Intent(ctx, SyncService::class.java))
        }

        fun syncNow(ctx: Context) {
            ContextCompat.startForegroundService(ctx, Intent(ctx, SyncService::class.java).setAction(ACTION_SYNC))
        }

        fun stop(ctx: Context) {
            ctx.stopService(Intent(ctx, SyncService::class.java))
        }
    }
}
