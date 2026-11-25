// SPDX-License-Identifier: MIT
package io.localai.client.http

import java.io.IOException
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLContext
import okhttp3.Call
import okhttp3.CookieJar
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.ResponseBody
import okio.Buffer

sealed interface ProbeResult {
    data class Answer(val status: Int, val location: String?, val contentType: String, val body: String) : ProbeResult

    /** [reason] is one of [TlsReason]. */
    data class Tls(val reason: String) : ProbeResult

    /** [reason] is one of [UnreachableReason]. */
    data class Unreachable(val reason: String) : ProbeResult

    object Cancelled : ProbeResult
}

/**
 * One GET for the connection test: no redirects, no cookies, no credentials,
 * no cache, the platform's trust only (there is no way to skip a certificate
 * check). Blocking; callers run it off the main thread.
 */
class ProbeClient(base: OkHttpClient) {
    private val client: OkHttpClient = base.newBuilder()
        .followRedirects(false)
        .followSslRedirects(false)
        .cookieJar(CookieJar.NO_COOKIES)
        .cache(null)
        .apply {
            base.x509TrustManager?.let { tm ->
                val recording = ChainRecordingTrustManager(tm)
                val ctx = SSLContext.getInstance("TLS").apply { init(null, arrayOf(recording), null) }
                sslSocketFactory(ctx.socketFactory, recording)
            }
        }
        .build()

    private val lock = Any()
    private val calls = HashMap<String, Call>()

    // A cancel can reach us before its get has created the call.
    private val cancelled = LinkedHashSet<String>()

    fun get(id: String, url: String, timeoutMs: Long): ProbeResult {
        val request = try {
            Request.Builder()
                .url(url)
                .header("Accept", "application/json, text/html;q=0.5")
                .header("User-Agent", USER_AGENT)
                .build()
        } catch (_: IllegalArgumentException) {
            return ProbeResult.Unreachable(UnreachableReason.NETWORK)
        }
        val call = client.newCall(request)
        call.timeout().timeout(timeoutMs.coerceAtLeast(1), TimeUnit.MILLISECONDS)
        synchronized(lock) {
            if (cancelled.remove(id)) return ProbeResult.Cancelled
            calls[id] = call
        }
        try {
            call.execute().use { r ->
                return ProbeResult.Answer(r.code, r.header("Location"), r.header("Content-Type").orEmpty(), readCapped(r.body))
            }
        } catch (e: IOException) {
            // OkHttp cancels a call on its own call timeout too, so only our
            // own record says that the user cancelled.
            if (synchronized(lock) { id in cancelled }) return ProbeResult.Cancelled
            return classifyIoError(e)
        } finally {
            synchronized(lock) {
                calls.remove(id)
                cancelled.remove(id)
            }
        }
    }

    fun cancel(id: String) {
        val call = synchronized(lock) {
            cancelled.add(id)
            while (cancelled.size > MAX_PENDING_CANCELS) cancelled.remove(cancelled.first())
            calls[id]
        }
        call?.cancel()
    }

    private fun readCapped(body: ResponseBody?): String {
        if (body == null) return ""
        val source = body.source()
        val buf = Buffer()
        while (buf.size < MAX_BODY_BYTES) {
            if (source.read(buf, MAX_BODY_BYTES - buf.size) == -1L) break
        }
        return buf.readUtf8()
    }

    companion object {
        const val MAX_BODY_BYTES = 65_536L
        const val USER_AGENT = "localai-android"
        private const val MAX_PENDING_CANCELS = 64
    }
}
