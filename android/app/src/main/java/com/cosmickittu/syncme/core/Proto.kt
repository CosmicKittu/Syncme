package com.cosmickittu.syncme.core

import org.json.JSONObject
import java.io.BufferedInputStream
import java.io.BufferedOutputStream
import java.io.DataInputStream
import java.io.DataOutputStream
import java.io.IOException
import java.io.InputStream
import java.io.OutputStream
import java.net.Socket
import java.security.MessageDigest

// Frame format shared with the Go side (internal/proto/proto.go):
// 1 byte type ('J' json / 'D' data) | 4 byte big-endian length | payload.
// A file body is a run of 'D' frames ended by an empty 'D' frame.

const val PROTO_VERSION = 1
const val DEFAULT_PORT = 47810
const val DISCOVERY_PORT = 47811

class ProtocolException(msg: String) : IOException(msg)

/** An error reply from the PC (e.g. "not paired"). */
class RemoteException(msg: String) : IOException(msg)

class FrameConn(val socket: Socket) : AutoCloseable {
    private val input = DataInputStream(BufferedInputStream(socket.getInputStream(), 64 * 1024))
    private val output = DataOutputStream(BufferedOutputStream(socket.getOutputStream(), 64 * 1024))
    private val writeLock = Any()

    private fun writeFrame(type: Int, buf: ByteArray, len: Int) {
        output.writeByte(type)
        output.writeInt(len)
        output.write(buf, 0, len)
    }

    private fun readHeader(max: Int): Pair<Int, Int> {
        val type = input.readUnsignedByte()
        val len = input.readInt()
        if (len < 0 || len > max) throw ProtocolException("frame too large: $len")
        return type to len
    }

    fun send(m: JSONObject) {
        val b = m.toString().toByteArray(Charsets.UTF_8)
        synchronized(writeLock) {
            writeFrame('J'.code, b, b.size)
            output.flush()
        }
    }

    fun recv(): JSONObject {
        val (type, len) = readHeader(MAX_JSON)
        val b = ByteArray(len)
        input.readFully(b)
        if (type != 'J'.code) throw ProtocolException("expected message frame")
        return JSONObject(String(b, Charsets.UTF_8))
    }

    /** A message and its data stream, with no other frame in between. */
    fun sendWithData(m: JSONObject, src: InputStream, limit: Long): String =
        synchronized(writeLock) {
            send(m)
            sendData(src, limit)
        }

    /** Streams up to [limit] bytes of [src]; returns the sha256 of what was sent. */
    fun sendData(src: InputStream, limit: Long): String {
        val md = MessageDigest.getInstance("SHA-256")
        val buf = ByteArray(CHUNK)
        var left = limit
        var readError: IOException? = null
        synchronized(writeLock) {
            while (left > 0) {
                val n = try {
                    src.read(buf, 0, minOf(buf.size.toLong(), left).toInt())
                } catch (e: IOException) {
                    readError = e; -1
                }
                if (n < 0) break
                if (n == 0) continue
                md.update(buf, 0, n)
                writeFrame('D'.code, buf, n)
                left -= n
            }
            writeFrame('D'.code, buf, 0) // terminator keeps the stream in sync even on read errors
            output.flush()
        }
        return hex(md.digest())
    }

    class DataResult(val size: Long, val hash: String, val writeError: IOException?)

    /** Reads a data stream into [dst] (may be null to discard). */
    fun recvData(dst: OutputStream?, limit: Long): DataResult {
        val md = MessageDigest.getInstance("SHA-256")
        var total = 0L
        var writeError: IOException? = null
        var buf = ByteArray(CHUNK)
        while (true) {
            val (type, len) = readHeader(MAX_CHUNK)
            if (type != 'D'.code) throw ProtocolException("expected data frame")
            if (len == 0) return DataResult(total, hex(md.digest()), writeError)
            if (buf.size < len) buf = ByteArray(len)
            input.readFully(buf, 0, len)
            total += len
            if (total > limit) throw ProtocolException("data longer than declared")
            md.update(buf, 0, len)
            if (dst != null && writeError == null) {
                try {
                    dst.write(buf, 0, len)
                } catch (e: IOException) {
                    writeError = e
                }
            }
        }
    }

    override fun close() {
        try { socket.close() } catch (_: IOException) {}
    }

    companion object {
        const val MAX_JSON = 64 shl 20
        const val MAX_CHUNK = 1 shl 20
        const val CHUNK = 256 shl 10
    }
}

fun msg(t: String, vararg kv: Pair<String, Any?>): JSONObject {
    val o = JSONObject()
    o.put("t", t)
    for ((k, v) in kv) if (v != null) o.put(k, v)
    return o
}

fun hex(b: ByteArray): String {
    val sb = StringBuilder(b.size * 2)
    for (x in b) {
        val v = x.toInt() and 0xff
        sb.append("0123456789abcdef"[v shr 4]).append("0123456789abcdef"[v and 15])
    }
    return sb.toString()
}
