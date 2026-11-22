package com.cosmickittu.syncme

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent

/** Restarts syncing after a reboot or app update. */
class BootReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action == Intent.ACTION_BOOT_COMPLETED || intent.action == Intent.ACTION_MY_PACKAGE_REPLACED) {
            if (Prefs(context).load() != null) {
                try { SyncService.start(context) } catch (_: Exception) {}
            }
        }
    }
}
