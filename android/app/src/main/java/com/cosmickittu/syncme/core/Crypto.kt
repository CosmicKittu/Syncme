package com.cosmickittu.syncme.core

import java.net.Socket
import java.security.MessageDigest
import java.security.SecureRandom
import java.security.cert.CertificateException
import java.security.cert.X509Certificate
import javax.crypto.Mac
import javax.crypto.spec.SecretKeySpec
import javax.net.ssl.SSLContext
import javax.net.ssl.SSLSocket
import javax.net.ssl.X509TrustManager

// Must match internal/proto/crypto.go exactly.
object Crypto {
    private val rng = SecureRandom()

    fun sha256Hex(b: ByteArray): String = hex(MessageDigest.getInstance("SHA-256").digest(b))

    fun randomHex(n: Int): String = ByteArray(n).also { rng.nextBytes(it) }.let(::hex)

    fun authMac(secret: ByteArray, nonce: String, serverId: String, clientId: String): String {
        val mac = Mac.getInstance("HmacSHA256")
        mac.init(SecretKeySpec(secret, "HmacSHA256"))
        return hex(mac.doFinal("syncme-auth-v1\n$nonce\n$serverId\n$clientId".toByteArray(Charsets.UTF_8)))
    }

    fun pairCode(serverId: String, clientId: String, token: String): String {
        val d = MessageDigest.getInstance("SHA-256")
            .digest("syncme-pair-v1\n$serverId\n$clientId\n${token.uppercase()}".toByteArray(Charsets.UTF_8))
        val v = ((d[0].toLong() and 0xff) shl 24) or ((d[1].toLong() and 0xff) shl 16) or
            ((d[2].toLong() and 0xff) shl 8) or (d[3].toLong() and 0xff)
        return String.format(java.util.Locale.US, "%06d", v % 1_000_000)
    }

    /** "ABCD-1234-..." like the PC shows. */
    fun shortId(id: String): String {
        val u = id.take(16).uppercase()
        return u.chunked(4).joinToString("-")
    }

    /**
     * Trusts exactly one server key: sha256(SPKI) must equal [pinnedId].
     * There are no certificate authorities involved, so a compromised CA,
     * a hostile Wi-Fi or the relay cannot impersonate the PC.
     */
    class PinnedTrustManager(private val pinnedId: String) : X509TrustManager {
        override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?) =
            throw CertificateException("client certs not used")

        override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?) {
            val leaf = chain?.firstOrNull() ?: throw CertificateException("no certificate")
            val id = sha256Hex(leaf.publicKey.encoded)
            if (!MessageDigest.isEqual(id.toByteArray(), pinnedId.lowercase().toByteArray())) {
                throw CertificateException("PC key does not match the paired fingerprint")
            }
        }

        override fun getAcceptedIssuers(): Array<X509Certificate> = arrayOf()
    }

    fun tlsWrap(plain: Socket, host: String, port: Int, pinnedId: String): SSLSocket {
        val ctx = SSLContext.getInstance("TLS")
        ctx.init(null, arrayOf(PinnedTrustManager(pinnedId)), rng)
        val s = ctx.socketFactory.createSocket(plain, host, port, true) as SSLSocket
        s.enabledProtocols = s.supportedProtocols.filter { it == "TLSv1.3" || it == "TLSv1.2" }.toTypedArray()
        s.startHandshake()
        return s
    }
}
