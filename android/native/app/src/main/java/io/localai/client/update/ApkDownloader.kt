// SPDX-License-Identifier: MIT
package io.localai.client.update

import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import java.io.ByteArrayOutputStream
import java.io.File
import java.io.IOException
import java.security.MessageDigest

/** A download or fetch the updater will not accept. [reason] is size, sha256, http-<code> or scheme. */
class DownloadRefused(val reason: String) : Exception(reason)

/**
 * Fetches the release feed and downloads the APK into [dir] (the app's cache).
 *
 * Redirects are followed by hand so that every hop is checked: the release feed
 * redirects to a storage host, and a hop to plain HTTP would let anyone on the
 * network swap the manifest. [allowHttp] is true only for a test build whose
 * feed itself is plain HTTP.
 */
class ApkDownloader(client: OkHttpClient, private val dir: File, private val allowHttp: Boolean = false) {
    private val client = client.newBuilder()
        .followRedirects(false)
        .followSslRedirects(false)
        .build()

    /** Downloads to dir/update.apk.part, checks size and SHA-256 while streaming, renames to update.apk. Deletes the file on any mismatch. */
    fun download(url: String, sha256: String, size: Long, progress: (Long, Long) -> Unit): File {
        if (!SHA256_HEX.matches(sha256)) throw DownloadRefused("sha256")
        if (size <= 0 || size > MAX_APK_BYTES) throw DownloadRefused("size")
        clear()
        val part = File(dir, PART_NAME)
        val target = File(dir, APK_NAME)
        try {
            open(url).use { response ->
                if (!response.isSuccessful) throw DownloadRefused("http-${response.code}")
                val body = response.body ?: throw DownloadRefused("size")
                val announced = body.contentLength()
                if (announced >= 0 && announced != size) throw DownloadRefused("size")
                val digest = MessageDigest.getInstance("SHA-256")
                var done = 0L
                val buffer = ByteArray(BUFFER_BYTES)
                body.byteStream().use { input ->
                    part.outputStream().use { output ->
                        while (true) {
                            val n = input.read(buffer)
                            if (n < 0) break
                            done += n
                            // Stop as soon as the file is longer than announced, so a
                            // lying server cannot fill the cache.
                            if (done > size) throw DownloadRefused("size")
                            digest.update(buffer, 0, n)
                            output.write(buffer, 0, n)
                            progress(done, size)
                        }
                        output.fd.sync()
                    }
                }
                if (done != size) throw DownloadRefused("size")
                if (hex(digest.digest()) != sha256) throw DownloadRefused("sha256")
            }
            if (!part.renameTo(target)) throw IOException("cannot rename the downloaded file")
            return target
        } catch (e: Throwable) {
            part.delete()
            target.delete()
            throw e
        }
    }

    /** Returns the HTTP status and, for a 2xx answer, the body. A body over [maxBytes] is refused. */
    fun fetchText(url: String, maxBytes: Int): Pair<Int, String> {
        open(url).use { response ->
            if (!response.isSuccessful) return response.code to ""
            val body = response.body ?: return response.code to ""
            if (body.contentLength() > maxBytes) throw DownloadRefused("size")
            val out = ByteArrayOutputStream()
            val buffer = ByteArray(BUFFER_BYTES)
            body.byteStream().use { input ->
                while (true) {
                    val n = input.read(buffer)
                    if (n < 0) break
                    if (out.size() + n > maxBytes) throw DownloadRefused("size")
                    out.write(buffer, 0, n)
                }
            }
            return response.code to out.toString(Charsets.UTF_8.name())
        }
    }

    /**
     * The downloaded update for a path the web side passed back, or null when the
     * path is not this downloader's finished file. The web side only ever echoes the
     * path [download] returned; anything else is refused rather than installed.
     */
    fun downloaded(path: String): File? {
        val target = File(dir, APK_NAME)
        val asked = try {
            File(path).canonicalFile
        } catch (_: IOException) {
            return null
        }
        if (asked != target.canonicalFile || !target.isFile) return null
        return target
    }

    /** Removes every file of earlier downloads. */
    fun clear() {
        dir.mkdirs()
        dir.listFiles()?.forEach { it.deleteRecursively() }
    }

    private fun open(url: String): Response {
        var current = url.toHttpUrlOrNull() ?: throw DownloadRefused("scheme")
        repeat(MAX_REDIRECTS + 1) {
            if (!schemeAllowed(current.scheme)) throw DownloadRefused("scheme")
            val response = client.newCall(Request.Builder().url(current).build()).execute()
            if (!response.isRedirect) return response
            val location = response.header("Location")
            response.close()
            current = location?.let { current.resolve(it) } ?: throw DownloadRefused("http-${response.code}")
        }
        throw DownloadRefused("redirects")
    }

    private fun schemeAllowed(scheme: String) = scheme == "https" || (allowHttp && scheme == "http")

    companion object {
        const val APK_NAME = "update.apk"
        const val PART_NAME = "update.apk.part"
        /** The release policy caps the manifest's size at 300 000 000 bytes. */
        const val MAX_APK_BYTES = 300_000_000L
        private const val MAX_REDIRECTS = 5
        private const val BUFFER_BYTES = 64 * 1024
        private val SHA256_HEX = Regex("^[0-9a-f]{64}$")

        fun hex(bytes: ByteArray): String = bytes.joinToString("") { "%02x".format(it) }

        /**
         * True when [url], after dot segments are resolved, lies under the build's
         * feed [base]. The web side builds every feed URL from that base; anything
         * else is not an update of this app.
         */
        fun underFeed(url: String, base: String): Boolean {
            val parsed = url.toHttpUrlOrNull() ?: return false
            val root = base.trimEnd('/').toHttpUrlOrNull() ?: return false
            return parsed.scheme == root.scheme &&
                parsed.host == root.host &&
                parsed.port == root.port &&
                parsed.encodedPath.startsWith(root.encodedPath.trimEnd('/') + "/")
        }
    }
}
