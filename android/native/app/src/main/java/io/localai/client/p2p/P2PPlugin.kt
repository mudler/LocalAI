// SPDX-License-Identifier: MIT
package io.localai.client.p2p

import android.os.Build
import com.getcapacitor.JSArray
import com.getcapacitor.JSObject
import com.getcapacitor.Plugin
import com.getcapacitor.PluginCall
import com.getcapacitor.PluginMethod
import com.getcapacitor.annotation.CapacitorPlugin
import io.localai.client.BuildConfig
import org.json.JSONArray
import org.json.JSONObject
import java.net.InetAddress
import java.net.ServerSocket
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors

/** P2P pairing and forwarders over the gomobile library (contract: P2PPlugin in src/plugins/definitions.ts). */
@CapacitorPlugin(name = "P2P")
class P2PPlugin : Plugin() {
    // Pairing blocks for up to two minutes; it must not hold up other calls.
    private val work: ExecutorService = Executors.newCachedThreadPool()

    private val stateListener: (String, String) -> Unit = { id, line ->
        // Retained until the web side listens, so a line sent while it loads is not lost.
        notifyListeners("forwardState", JSObject().put("id", id).put("line", line), true)
    }

    override fun load() {
        P2PRuntime.init(context)
        P2PRuntime.addListener(stateListener)
    }

    override fun handleOnDestroy() {
        P2PRuntime.removeListener(stateListener)
        work.shutdown()
    }

    @PluginMethod
    fun version(call: PluginCall) {
        if (!available(call)) return
        background(call) { call.resolve(JSObject().put("version", P2PRuntime.engine.version())) }
    }

    @PluginMethod
    fun deviceName(call: PluginCall) {
        call.resolve(JSObject().put("name", defaultDeviceName()))
    }

    @PluginMethod
    fun peerId(call: PluginCall) {
        if (!available(call)) return
        background(call) {
            // No identity yet (never paired) is an answer, not an error.
            val id = try {
                P2PRuntime.engine.peerId(P2PRuntime.identityPath)
            } catch (e: P2PUnavailableException) {
                throw e
            } catch (_: Exception) {
                null
            }
            call.resolve(JSObject().apply { if (id != null) put("peerId", id) })
        }
    }

    @PluginMethod
    fun pair(call: PluginCall) {
        if (!available(call)) return
        val link = call.getString("link")?.trim().orEmpty()
        val name = call.getString("name")?.trim()?.take(60)?.takeIf { it.isNotEmpty() } ?: defaultDeviceName()
        val allowOpenRelay = call.getBoolean("allowOpenRelay", false) == true
        background(call) {
            val out = try {
                JSObject().put("ok", true).put("json", P2PRuntime.engine.pair(P2PRuntime.identityPath, link, name, allowOpenRelay))
            } catch (e: P2PUnavailableException) {
                throw e
            } catch (e: Exception) {
                JSObject().put("ok", false).put("detail", e.message ?: "pairing failed")
            }
            call.resolve(out)
        }
    }

    @PluginMethod
    fun freePort(call: PluginCall) {
        val host = call.getString("host").orEmpty()
        if (!isLoopbackHost(host)) {
            call.reject("host must be a loopback address", "invalid")
            return
        }
        background(call) {
            val port = ServerSocket(0, 1, InetAddress.getByName(host)).use { it.localPort }
            call.resolve(JSObject().put("port", port))
        }
    }

    @PluginMethod
    fun setForwards(call: PluginCall) {
        if (!available(call)) return
        val arr = call.getArray("forwards")
        if (arr == null) {
            call.reject("forwards is required", "invalid")
            return
        }
        P2PRuntime.setForwards(parseForwards(arr))
        call.resolve()
    }

    @PluginMethod
    fun statuses(call: PluginCall) {
        call.resolve(statesObject(P2PRuntime.lastStates()))
    }

    @PluginMethod
    fun setKeepInBackground(call: PluginCall) {
        call.reject("not implemented")
    }

    private fun statesObject(states: Map<String, String>): JSObject {
        val arr = JSArray()
        for ((id, line) in states) arr.put(JSObject().put("id", id).put("line", line))
        return JSObject().put("states", arr)
    }

    // A build without the P2P library (no AAR) says so, so the app can hide P2P.
    private fun available(call: PluginCall): Boolean {
        if (BuildConfig.P2P_LIBRARY) return true
        call.reject("unavailable", "unavailable")
        return false
    }

    private fun background(call: PluginCall, block: () -> Unit) {
        work.execute {
            try {
                block()
            } catch (_: P2PUnavailableException) {
                call.reject("unavailable", "unavailable")
            } catch (e: Exception) {
                call.reject(e.message ?: e.javaClass.simpleName)
            }
        }
    }

    private fun defaultDeviceName() = "${Build.MANUFACTURER} ${Build.MODEL}".trim().take(60)
}

/** Reads the forwards of setForwards. Entries without an id are skipped; the supervisor checks the address. */
internal fun parseForwards(arr: JSONArray): List<Forward> {
    val out = ArrayList<Forward>()
    for (i in 0 until arr.length()) {
        val o: JSONObject = arr.optJSONObject(i) ?: continue
        val id = o.optString("id")
        if (id.isEmpty()) continue
        out += Forward(
            id = id,
            name = o.optString("name"),
            peer = o.optString("peer"),
            host = o.optString("host"),
            port = o.optInt("port", 0),
            addrs = strings(o.optJSONArray("addrs")),
            relays = strings(o.optJSONArray("relays")),
        )
    }
    return out
}

private fun strings(a: JSONArray?): List<String> {
    if (a == null) return emptyList()
    return (0 until a.length()).mapNotNull { a.opt(it) as? String }
}
