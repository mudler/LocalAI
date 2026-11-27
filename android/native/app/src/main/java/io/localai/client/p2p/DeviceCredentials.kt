// SPDX-License-Identifier: MIT
package io.localai.client.p2p

import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import org.json.JSONObject
import java.io.File
import java.io.IOException
import java.security.GeneralSecurityException
import java.security.KeyStore
import java.security.MessageDigest
import java.util.Collections
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/**
 * Encrypts and decrypts the stored device credentials. The app uses
 * [KeystoreCipher]; tests use a fake, because the JVM has no Android Keystore.
 */
interface CredentialCipher {
    fun encrypt(plain: ByteArray): ByteArray

    /** Throws GeneralSecurityException when [blob] does not decrypt (another key, or changed bytes). */
    fun decrypt(blob: ByteArray): ByteArray
}

/**
 * AES-256-GCM with one key in the Android Keystore. The key never leaves
 * the Keystore. A blob is a version byte, the 12-byte IV and the ciphertext
 * with its tag.
 */
class KeystoreCipher(private val alias: String = ALIAS) : CredentialCipher {
    override fun encrypt(plain: ByteArray): ByteArray {
        val c = Cipher.getInstance(TRANSFORMATION)
        c.init(Cipher.ENCRYPT_MODE, key())
        val iv = c.iv
        if (iv.size != IV_BYTES) throw GeneralSecurityException("unexpected IV size")
        return byteArrayOf(VERSION) + iv + c.doFinal(plain)
    }

    override fun decrypt(blob: ByteArray): ByteArray {
        if (blob.size <= 1 + IV_BYTES || blob[0] != VERSION) throw GeneralSecurityException("unknown blob")
        val existing = existingKey() ?: throw GeneralSecurityException("no key")
        val c = Cipher.getInstance(TRANSFORMATION)
        c.init(Cipher.DECRYPT_MODE, existing, GCMParameterSpec(TAG_BITS, blob, 1, IV_BYTES))
        return c.doFinal(blob, 1 + IV_BYTES, blob.size - 1 - IV_BYTES)
    }

    private fun keyStore(): KeyStore = KeyStore.getInstance(PROVIDER).apply { load(null) }

    private fun existingKey(): SecretKey? = keyStore().getKey(alias, null) as? SecretKey

    @Synchronized
    private fun key(): SecretKey {
        existingKey()?.let { return it }
        val gen = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, PROVIDER)
        gen.init(
            KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .build(),
        )
        return gen.generateKey()
    }

    private companion object {
        const val ALIAS = "p2p-device-credentials"
        const val PROVIDER = "AndroidKeyStore"
        const val TRANSFORMATION = "AES/GCM/NoPadding"
        const val IV_BYTES = 12
        const val TAG_BITS = 128
        const val VERSION: Byte = 1
    }
}

/**
 * The device credentials of the servers this device paired with, one per
 * server peer ID. A server gives one when a sign-in invite is redeemed; the
 * app posts it to the server's `/api/p2p/device/session` to get a web
 * session. It never reaches the web layer, a log or a crash report.
 *
 * Each credential is a file in [dir] (under noBackupFilesDir: a restored
 * phone has another Keystore key and pairs again), named by the SHA-256 of
 * the peer ID and encrypted with [cipher]. A file that does not decrypt is
 * removed and reads as no credential.
 *
 * A server the person signed out of in the WebUI is "signed out": the app
 * does not post its credential until the person opens it again from the
 * server list ([allowAutoSignIn]) or pairs again ([save]). The flag is an
 * empty file next to the credential (the same name plus [OFF_SUFFIX]), so a
 * cold start keeps it, and a set in memory, so a failed write still holds
 * for the life of the process.
 */
class DeviceCredentials(private val dir: File, private val cipher: CredentialCipher) {
    /** Peers paired since the process started whose server view has not opened yet. */
    private val fresh: MutableSet<String> = Collections.synchronizedSet(HashSet())

    /** Peers signed out of since the process started (the files hold the rest). */
    private val off: MutableSet<String> = Collections.synchronizedSet(HashSet())

    /**
     * Stores [credential] for [peer] and marks the peer fresh. A new pairing
     * turns automatic sign-in on again. Throws on a storage or Keystore error.
     */
    @Synchronized
    fun save(peer: String, credential: String) {
        require(peer.isNotEmpty() && credential.isNotEmpty()) { "empty peer or credential" }
        if (!dir.isDirectory && !dir.mkdirs()) throw IOException("cannot create the credential folder")
        val blob = cipher.encrypt(credential.toByteArray(Charsets.UTF_8))
        val target = fileOf(peer)
        val tmp = File(dir, target.name + ".tmp")
        tmp.writeBytes(blob)
        if (!tmp.renameTo(target)) {
            tmp.delete()
            throw IOException("cannot store the credential")
        }
        clearSignedOut(peer)
        fresh.add(peer)
    }

    /** The credential of [peer], or null when there is none or it does not decrypt. */
    @Synchronized
    fun load(peer: String): String? {
        if (peer.isEmpty()) return null
        val f = fileOf(peer)
        if (!f.isFile) return null
        return try {
            String(cipher.decrypt(f.readBytes()), Charsets.UTF_8)
        } catch (_: GeneralSecurityException) {
            // The key is gone (cleared data, restored phone): the credential is useless.
            f.delete()
            null
        } catch (_: IOException) {
            null
        }
    }

    fun has(peer: String): Boolean = peer.isNotEmpty() && fileOf(peer).isFile

    /**
     * The person signed out of [peer] in the WebUI: no automatic sign-in
     * until [allowAutoSignIn] or [save]. Never throws; a flag that cannot be
     * written holds until the process ends.
     */
    @Synchronized
    fun signOut(peer: String) {
        if (peer.isEmpty()) return
        off.add(peer)
        fresh.remove(peer)
        try {
            if (!dir.isDirectory && !dir.mkdirs()) return
            offFileOf(peer).createNewFile()
        } catch (_: IOException) {
            // The flag in memory still holds.
        } catch (_: SecurityException) {
            // Same.
        }
    }

    /** True after [signOut] of [peer], until [allowAutoSignIn], [save] or [delete]. */
    fun signedOut(peer: String): Boolean = peer.isNotEmpty() && (peer in off || offFileOf(peer).isFile)

    /**
     * The person opened [peer] from the server list: automatic sign-in is on
     * again. True when the server was signed out.
     */
    @Synchronized
    fun allowAutoSignIn(peer: String): Boolean {
        if (peer.isEmpty()) return false
        val was = signedOut(peer)
        clearSignedOut(peer)
        return was
    }

    private fun clearSignedOut(peer: String) {
        off.remove(peer)
        offFileOf(peer).delete()
    }

    /** True once for a peer saved since the process started: the first open after pairing. */
    fun takeFresh(peer: String): Boolean = fresh.remove(peer)

    /** Deletes the credential of [peer]; the server was removed from the app. */
    @Synchronized
    fun delete(peer: String) {
        if (peer.isEmpty()) return
        fresh.remove(peer)
        clearSignedOut(peer)
        fileOf(peer).delete()
    }

    /** Deletes every credential whose peer is not in [peers]: the saved servers. */
    @Synchronized
    fun retain(peers: Collection<String>) {
        val names = peers.filter { it.isNotEmpty() }.map { nameOf(it) }
        val keep = names.toSet() + names.map { it + OFF_SUFFIX }
        synchronized(fresh) { fresh.retainAll(peers.toSet()) }
        synchronized(off) { off.retainAll(peers.toSet()) }
        dir.listFiles()?.forEach { f -> if (f.name !in keep) f.delete() }
    }

    private fun fileOf(peer: String) = File(dir, nameOf(peer))

    private fun offFileOf(peer: String) = File(dir, nameOf(peer) + OFF_SUFFIX)

    companion object {
        /** The suffix of the flag file of a signed-out server. */
        const val OFF_SUFFIX = ".signed-out"

        fun nameOf(peer: String): String =
            MessageDigest.getInstance("SHA-256").digest(peer.toByteArray(Charsets.UTF_8))
                .joinToString("") { b -> (b.toInt() and 0xff).toString(16).padStart(2, '0') }
    }
}

/** The answer of the library's pairing with the device credential taken out. */
internal data class CredentialSplit(
    /** The result object without `device_credential`: what the web layer gets. */
    val json: String,
    /** The server's peer ID (`server.peer_id`), or "" when the answer has none. */
    val peer: String,
    /** The credential, or null when the server gave none (an old server, or an invite without sign-in). */
    val credential: String?,
)

internal const val CREDENTIAL_KEY = "device_credential"

/**
 * Takes `device_credential` out of a pairing result object. The key is
 * always removed, whatever its value, so the web layer never sees it.
 * [resultJson] is the `result` object of [parsePairAnswer]; it is valid
 * JSON there.
 */
internal fun splitCredential(resultJson: String): CredentialSplit {
    val o = JSONObject(resultJson)
    val raw = if (o.has(CREDENTIAL_KEY)) o.opt(CREDENTIAL_KEY) else null
    o.remove(CREDENTIAL_KEY)
    val credential = (raw as? String)?.takeIf { it.isNotEmpty() }
    val peer = o.optJSONObject("server")?.optString("peer_id").orEmpty()
    return CredentialSplit(json = o.toString(), peer = peer, credential = credential)
}
