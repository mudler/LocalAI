// SPDX-License-Identifier: MIT
package io.localai.client.http

import com.getcapacitor.JSObject
import com.getcapacitor.Plugin
import com.getcapacitor.PluginCall
import com.getcapacitor.PluginMethod
import com.getcapacitor.annotation.CapacitorPlugin
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors
import okhttp3.OkHttpClient

/** HTTP requests for the connection test (contract: NativeHttpPlugin in src/plugins/definitions.ts). */
@CapacitorPlugin(name = "NativeHttp")
class NativeHttpPlugin : Plugin() {
    private val executor: ExecutorService = Executors.newCachedThreadPool()
    private val probe by lazy { ProbeClient(OkHttpClient()) }

    @PluginMethod
    fun get(call: PluginCall) {
        val id = call.getString("id")
        val url = call.getString("url")
        if (id.isNullOrEmpty() || url.isNullOrEmpty()) {
            call.reject("id and url are required")
            return
        }
        // A JS number arrives as Integer or Double, which PluginCall.getLong refuses.
        val timeoutMs = ((call.data.opt("timeoutMs") as? Number)?.toLong() ?: DEFAULT_TIMEOUT_MS)
            .coerceIn(1, MAX_TIMEOUT_MS)
        executor.execute {
            val result = try {
                probe.get(id, url, timeoutMs)
            } catch (_: Exception) {
                ProbeResult.Unreachable(UnreachableReason.NETWORK)
            }
            call.resolve(toJs(result))
        }
    }

    @PluginMethod
    fun cancel(call: PluginCall) {
        val id = call.getString("id")
        if (id.isNullOrEmpty()) {
            call.reject("id is required")
            return
        }
        probe.cancel(id)
        call.resolve()
    }

    override fun handleOnDestroy() {
        executor.shutdownNow()
    }

    private fun toJs(r: ProbeResult): JSObject = JSObject().apply {
        when (r) {
            is ProbeResult.Answer -> {
                put("ok", true)
                put("status", r.status)
                r.location?.let { put("location", it) }
                put("contentType", r.contentType)
                put("body", r.body)
            }
            is ProbeResult.Tls -> {
                put("ok", false)
                put("error", "tls")
                put("reason", r.reason)
            }
            is ProbeResult.Unreachable -> {
                put("ok", false)
                put("error", "unreachable")
                put("reason", r.reason)
            }
            ProbeResult.Cancelled -> {
                put("ok", false)
                put("error", "cancelled")
            }
        }
    }

    private companion object {
        const val DEFAULT_TIMEOUT_MS = 8_000L
        const val MAX_TIMEOUT_MS = 60_000L
    }
}
