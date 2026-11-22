package com.cosmickittu.syncme

import android.Manifest
import android.annotation.SuppressLint
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.PowerManager
import android.provider.Settings
import android.text.InputFilter
import android.text.InputType
import android.text.format.DateUtils
import android.view.View
import android.widget.Button
import android.widget.EditText
import android.widget.FrameLayout
import android.widget.TextView
import android.widget.Toast
import androidx.activity.result.contract.ActivityResultContracts
import androidx.appcompat.app.AlertDialog
import androidx.appcompat.app.AppCompatActivity
import com.cosmickittu.syncme.core.Connector
import com.cosmickittu.syncme.core.Crypto
import com.cosmickittu.syncme.core.PairTarget
import com.cosmickittu.syncme.core.SyncClient
import com.google.android.material.dialog.MaterialAlertDialogBuilder
import com.journeyapps.barcodescanner.ScanContract
import com.journeyapps.barcodescanner.ScanOptions
import kotlin.concurrent.thread

class MainActivity : AppCompatActivity() {
    private lateinit var prefs: Prefs
    private lateinit var tvPeer: TextView
    private lateinit var tvStatus: TextView
    private lateinit var tvLast: TextView
    private lateinit var tvFolder: TextView
    private lateinit var btnAccess: Button
    private lateinit var btnBattery: Button
    private lateinit var groupUnpaired: View
    private lateinit var groupPaired: View

    private val stateListener: () -> Unit = { runOnUiThread { refresh() } }

    private val scanQr = registerForActivityResult(ScanContract()) { r -> r.contents?.let { handleLink(it) } }
    private val askPermissions = registerForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) { refresh(); maybeStart() }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)
        prefs = Prefs(this)
        tvPeer = findViewById(R.id.tvPeer)
        tvStatus = findViewById(R.id.tvStatus)
        tvLast = findViewById(R.id.tvLast)
        tvFolder = findViewById(R.id.tvFolder)
        btnAccess = findViewById(R.id.btnAccess)
        btnBattery = findViewById(R.id.btnBattery)
        groupUnpaired = findViewById(R.id.groupUnpaired)
        groupPaired = findViewById(R.id.groupPaired)

        btnAccess.setOnClickListener { requestFileAccess() }
        btnBattery.setOnClickListener { requestBatteryExemption() }
        findViewById<Button>(R.id.btnScan).setOnClickListener {
            scanQr.launch(ScanOptions().setPrompt(getString(R.string.scan_prompt)).setBeepEnabled(false)
                .setOrientationLocked(false).setDesiredBarcodeFormats(ScanOptions.QR_CODE))
        }
        findViewById<Button>(R.id.btnFind).setOnClickListener { findOnLan() }
        findViewById<Button>(R.id.btnPaste).setOnClickListener { pasteLink() }
        findViewById<Button>(R.id.btnSync).setOnClickListener {
            SyncService.syncNow(this)
            Toast.makeText(this, R.string.syncing, Toast.LENGTH_SHORT).show()
        }
        findViewById<Button>(R.id.btnUnpair).setOnClickListener { confirmUnpair() }

        tvFolder.text = getString(R.string.folder_path, Storage.folder().absolutePath)
        if (Build.VERSION.SDK_INT >= 33) askPermissions.launch(arrayOf(Manifest.permission.POST_NOTIFICATIONS))
        handleIntent(intent)
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        handleIntent(intent)
    }

    private fun handleIntent(i: Intent?) {
        val data = i?.data ?: return
        if (data.scheme == "syncme") handleLink(data.toString())
    }

    override fun onResume() {
        super.onResume()
        SyncState.addListener(stateListener)
        refresh()
        maybeStart()
    }

    override fun onPause() {
        SyncState.removeListener(stateListener)
        super.onPause()
    }

    private fun maybeStart() {
        if (prefs.load() != null && Storage.hasAccess(this)) SyncService.start(this)
    }

    @SuppressLint("SetTextI18n")
    private fun refresh() {
        val peer = prefs.load()
        val access = Storage.hasAccess(this)
        btnAccess.visibility = if (access) View.GONE else View.VISIBLE
        val pm = getSystemService(PowerManager::class.java)
        btnBattery.visibility = if (peer != null && !pm.isIgnoringBatteryOptimizations(packageName)) View.VISIBLE else View.GONE
        groupUnpaired.visibility = if (peer == null) View.VISIBLE else View.GONE
        groupPaired.visibility = if (peer != null) View.VISIBLE else View.GONE
        if (peer == null) {
            tvPeer.text = getString(R.string.not_paired)
            tvStatus.text = getString(R.string.pair_hint)
            tvLast.text = ""
        } else {
            tvPeer.text = "${peer.name}  ·  ${Crypto.shortId(peer.id)}"
            tvStatus.text = if (!access) getString(R.string.need_access) else SyncState.status
            val st = SyncState.lastStats
            tvLast.text = if (SyncState.lastSync == 0L) "" else getString(
                R.string.last_sync,
                DateUtils.getRelativeTimeSpanString(SyncState.lastSync, System.currentTimeMillis(), DateUtils.SECOND_IN_MILLIS),
                st?.pulled ?: 0, st?.pushed ?: 0, st?.conflicts ?: 0,
            )
        }
    }

    // ---- permissions ----

    private fun requestFileAccess() {
        if (Build.VERSION.SDK_INT >= 30) {
            try {
                startActivity(Intent(Settings.ACTION_MANAGE_APP_ALL_FILES_ACCESS_PERMISSION, Uri.parse("package:$packageName")))
            } catch (_: Exception) {
                startActivity(Intent(Settings.ACTION_MANAGE_ALL_FILES_ACCESS_PERMISSION))
            }
        } else {
            askPermissions.launch(arrayOf(Manifest.permission.WRITE_EXTERNAL_STORAGE, Manifest.permission.READ_EXTERNAL_STORAGE))
        }
    }

    @SuppressLint("BatteryLife")
    private fun requestBatteryExemption() {
        try {
            startActivity(Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, Uri.parse("package:$packageName")))
        } catch (_: Exception) {
            startActivity(Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS))
        }
    }

    // ---- pairing ----

    private fun handleLink(link: String) {
        val target = try { PairTarget.parse(link) } catch (e: Exception) {
            error(getString(R.string.bad_code, e.message ?: "")); return
        }
        if (prefs.load() != null) {
            MaterialAlertDialogBuilder(this)
                .setTitle(R.string.replace_title)
                .setMessage(getString(R.string.replace_msg, prefs.load()?.name ?: "", target.name))
                .setPositiveButton(R.string.replace) { _, _ -> startPair(target) }
                .setNegativeButton(android.R.string.cancel, null).show()
        } else startPair(target)
    }

    private fun startPair(target: PairTarget) {
        if (target.token.isNotEmpty()) doPair(target, target.token) else askToken(target)
    }

    private fun findOnLan() {
        val progress = MaterialAlertDialogBuilder(this).setMessage(R.string.searching).setCancelable(false).show()
        thread {
            val found = Connector.discover(2500)
            runOnUiThread {
                progress.dismiss()
                if (found.isEmpty()) {
                    error(getString(R.string.none_found))
                    return@runOnUiThread
                }
                val labels = found.map { "${it.name}\n${Crypto.shortId(it.id)}  (${it.host})" }.toTypedArray()
                MaterialAlertDialogBuilder(this)
                    .setTitle(R.string.choose_pc)
                    .setItems(labels) { _, i ->
                        val pc = found[i]
                        askToken(PairTarget(pc.id, pc.name, listOf(pc.addr)))
                    }
                    .setNegativeButton(android.R.string.cancel, null).show()
            }
        }
    }

    private fun askToken(target: PairTarget) {
        val input = EditText(this).apply {
            hint = "ABCD2345"
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_CAP_CHARACTERS or InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS
            filters = arrayOf(InputFilter.AllCaps(), InputFilter.LengthFilter(8))
        }
        MaterialAlertDialogBuilder(this)
            .setTitle(getString(R.string.enter_code_title, target.name))
            .setMessage(getString(R.string.enter_code_msg, Crypto.shortId(target.id)))
            .setView(padded(input))
            .setPositiveButton(R.string.pair) { _, _ -> doPair(target, input.text.toString()) }
            .setNegativeButton(android.R.string.cancel, null).show()
    }

    private fun pasteLink() {
        val input = EditText(this).apply { hint = "syncme://pair?..."; setSingleLine(false) }
        MaterialAlertDialogBuilder(this)
            .setTitle(R.string.paste_title)
            .setView(padded(input))
            .setPositiveButton(R.string.pair) { _, _ -> handleLink(input.text.toString()) }
            .setNegativeButton(android.R.string.cancel, null).show()
    }

    private fun doPair(target: PairTarget, token: String) {
        var cancelled = false
        val dlg: AlertDialog = MaterialAlertDialogBuilder(this)
            .setTitle(getString(R.string.pairing_with, target.name))
            .setMessage(R.string.connecting)
            .setCancelable(false)
            .setNegativeButton(android.R.string.cancel) { _, _ -> cancelled = true }
            .show()
        thread {
            try {
                val peer = SyncClient.pair(target, token, prefs.deviceId, prefs.deviceName) { code ->
                    runOnUiThread { dlg.setMessage(getString(R.string.verify_code, code.substring(0, 3) + " " + code.substring(3))) }
                }
                runOnUiThread {
                    if (cancelled) return@runOnUiThread
                    dlg.dismiss()
                    SyncService.stop(this)
                    prefs.save(peer)
                    Toast.makeText(this, getString(R.string.paired_ok, peer.name), Toast.LENGTH_LONG).show()
                    refresh()
                    if (!Storage.hasAccess(this)) requestFileAccess() else SyncService.start(this)
                }
            } catch (e: Exception) {
                runOnUiThread {
                    if (cancelled) return@runOnUiThread
                    dlg.dismiss()
                    error(getString(R.string.pair_failed, e.message ?: e.javaClass.simpleName))
                }
            }
        }
    }

    private fun confirmUnpair() {
        MaterialAlertDialogBuilder(this)
            .setTitle(R.string.unpair)
            .setMessage(R.string.unpair_msg)
            .setPositiveButton(R.string.unpair) { _, _ ->
                SyncService.stop(this)
                prefs.save(null)
                SyncState.status = "Not paired"
                refresh()
            }
            .setNegativeButton(android.R.string.cancel, null).show()
    }

    private fun error(msg: String) {
        MaterialAlertDialogBuilder(this).setMessage(msg).setPositiveButton(android.R.string.ok, null).show()
    }

    private fun padded(v: View): View {
        val pad = (20 * resources.displayMetrics.density).toInt()
        return FrameLayout(this).apply { setPadding(pad, pad / 2, pad, 0); addView(v) }
    }
}
