package com.cosmickittu.syncme

import android.content.Intent
import android.graphics.BitmapFactory
import android.os.Bundle
import android.text.format.DateUtils
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.ImageView
import android.widget.TextView
import androidx.appcompat.app.AppCompatActivity
import androidx.core.content.FileProvider
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView
import com.google.android.material.dialog.MaterialAlertDialogBuilder
import com.google.android.material.materialswitch.MaterialSwitch

/** Clipboard history: everything sent to or received from the PC. */
class ClipboardActivity : AppCompatActivity() {
    private lateinit var store: ClipStore
    private lateinit var adapter: Adapter
    private lateinit var empty: TextView
    private val listener: () -> Unit = { runOnUiThread { reload() } }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_clipboard)
        store = ClipStore.get(this)
        empty = findViewById(R.id.tvEmpty)
        val prefs = Prefs(this)

        findViewById<Button>(R.id.btnSendClip).setOnClickListener {
            // We have focus here, so the clipboard can be read directly.
            try { ClipActions.send(this, ClipBridge.readClipboard(this)) } catch (e: Exception) { toast(this, e.message ?: "") }
        }
        findViewById<Button>(R.id.btnGetClip).setOnClickListener { ClipActions.requestFromPc(this) }
        findViewById<Button>(R.id.btnClear).setOnClickListener {
            MaterialAlertDialogBuilder(this).setMessage(R.string.clip_clear_confirm)
                .setPositiveButton(R.string.clip_clear) { _, _ -> store.clear() }
                .setNegativeButton(android.R.string.cancel, null).show()
        }
        findViewById<MaterialSwitch>(R.id.swAuto).apply {
            isChecked = prefs.clipAuto
            setOnCheckedChangeListener { _, on -> ClipActions.setAuto(this@ClipboardActivity, on) }
        }

        adapter = Adapter()
        findViewById<RecyclerView>(R.id.list).apply {
            layoutManager = LinearLayoutManager(this@ClipboardActivity)
            adapter = this@ClipboardActivity.adapter
        }
        reload()
    }

    override fun onResume() {
        super.onResume()
        store.addListener(listener)
        reload()
    }

    override fun onPause() {
        store.removeListener(listener)
        super.onPause()
    }

    private fun reload() {
        adapter.items = store.list()
        adapter.notifyDataSetChanged()
        empty.visibility = if (adapter.items.isEmpty()) View.VISIBLE else View.GONE
    }

    private fun copy(e: ClipStore.Entry) {
        val item = store.toItem(e) ?: return toast(this, getString(R.string.clip_missing))
        ClipBridge.setClipboard(this, item)
        toast(this, getString(R.string.clip_copied))
    }

    private fun actions(e: ClipStore.Entry) {
        val labels = arrayOf(getString(R.string.clip_copy), getString(R.string.clip_send_pc), getString(R.string.clip_share), getString(R.string.clip_delete))
        MaterialAlertDialogBuilder(this).setItems(labels) { _, which ->
            when (which) {
                0 -> copy(e)
                1 -> store.toItem(e)?.let { ClipActions.send(this, it) }
                2 -> share(e)
                3 -> store.delete(e)
            }
        }.show()
    }

    private fun share(e: ClipStore.Entry) {
        val i = Intent(Intent.ACTION_SEND)
        if (e.kind == "text") {
            i.type = "text/plain"
            i.putExtra(Intent.EXTRA_TEXT, store.file(e).readText())
        } else {
            i.type = e.mime
            i.putExtra(Intent.EXTRA_STREAM, FileProvider.getUriForFile(this, ClipBridge.authority(this), store.file(e)))
            i.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
        }
        startActivity(Intent.createChooser(i, null))
    }

    inner class Holder(v: View) : RecyclerView.ViewHolder(v) {
        val meta: TextView = v.findViewById(R.id.tvMeta)
        val text: TextView = v.findViewById(R.id.tvText)
        val image: ImageView = v.findViewById(R.id.ivImage)
    }

    inner class Adapter : RecyclerView.Adapter<Holder>() {
        var items: List<ClipStore.Entry> = emptyList()

        override fun onCreateViewHolder(parent: ViewGroup, viewType: Int) =
            Holder(LayoutInflater.from(parent.context).inflate(R.layout.item_clip, parent, false))

        override fun getItemCount() = items.size

        override fun onBindViewHolder(h: Holder, pos: Int) {
            val e = items[pos]
            val arrow = if (e.dir == ClipStore.IN) "⬇ " + getString(R.string.clip_from, e.from.ifEmpty { "PC" }) else "⬆ " + getString(R.string.clip_to_pc)
            val time = DateUtils.getRelativeTimeSpanString(e.time, System.currentTimeMillis(), DateUtils.MINUTE_IN_MILLIS)
            h.meta.text = "$arrow  ·  $time"
            if (e.kind == "image") {
                h.text.visibility = View.GONE
                h.image.visibility = View.VISIBLE
                h.image.setImageBitmap(thumbnail(store.file(e).path))
            } else {
                h.image.visibility = View.GONE
                h.text.visibility = View.VISIBLE
                h.text.text = e.preview
            }
            h.itemView.setOnClickListener { copy(e) }
            h.itemView.setOnLongClickListener { actions(e); true }
        }
    }

    private fun thumbnail(path: String): android.graphics.Bitmap? {
        val o = BitmapFactory.Options().apply { inJustDecodeBounds = true }
        BitmapFactory.decodeFile(path, o)
        var sample = 1
        while (o.outWidth / sample > 600 || o.outHeight / sample > 600) sample *= 2
        return BitmapFactory.decodeFile(path, BitmapFactory.Options().apply { inSampleSize = sample })
    }
}
