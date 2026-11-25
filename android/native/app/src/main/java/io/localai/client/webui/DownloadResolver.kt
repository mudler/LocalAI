// SPDX-License-Identifier: MIT
package io.localai.client.webui

import okhttp3.OkHttpClient
import okhttp3.Request
import java.io.IOException
import java.util.concurrent.TimeUnit

/**
 * DownloadManager follows redirects and sends the same request headers on
 * every hop, so a `Cookie` header given to it would also reach another host
 * when the server redirects a download there. This follows the redirects
 * first, sending the sign-in cookie only to the server's own origin, and
 * answers the URL to hand to DownloadManager and whether the cookie may go
 * with it (only when that URL is on the server origin and answers without a
 * redirect).
 */
internal class DownloadResolver(
    private val client: OkHttpClient = OkHttpClient.Builder()
        .followRedirects(false)
        .followSslRedirects(false)
        .connectTimeout(15, TimeUnit.SECONDS)
        .readTimeout(30, TimeUnit.SECONDS)
        .build(),
) {
    data class Target(val url: String, val cookie: String?)

    fun resolve(url: String, origin: String, cookie: String?, userAgent: String?): Target {
        if (cookie.isNullOrEmpty()) return Target(url, null)
        var current = url
        repeat(MAX_HOPS) {
            if (WebuiPolicy.httpOrigin(current) != origin) return Target(current, null)
            val req = Request.Builder().url(current).get().header("Cookie", cookie)
            if (!userAgent.isNullOrEmpty()) req.header("User-Agent", userAgent)
            try {
                client.newCall(req.build()).execute().use { resp ->
                    if (!resp.isRedirect) return Target(current, cookie)
                    // A redirect without a usable Location: DownloadManager
                    // cannot follow it either; keep nothing private.
                    val next = resp.header("Location")?.let { resp.request.url.resolve(it) } ?: return Target(current, null)
                    current = next.toString()
                }
            } catch (_: IOException) {
                // Not resolved: download without the cookie rather than risk it.
                return Target(url, null)
            } catch (_: IllegalArgumentException) {
                return Target(url, null)
            }
        }
        return Target(current, null)
    }

    private companion object {
        const val MAX_HOPS = 5
    }
}
