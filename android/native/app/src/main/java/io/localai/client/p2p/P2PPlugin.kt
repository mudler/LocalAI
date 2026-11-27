// SPDX-License-Identifier: MIT
package io.localai.client.p2p

import android.Manifest
import android.content.Context
import android.os.Build
import android.os.SystemClock
import android.util.Log
import com.getcapacitor.JSArray
import com.getcapacitor.JSObject
import com.getcapacitor.PermissionState
import com.getcapacitor.Plugin
import com.getcapacitor.PluginCall
import com.getcapacitor.PluginMethod
import com.getcapacitor.annotation.CapacitorPlugin
import com.getcapacitor.annotation.Permission
import com.getcapacitor.annotation.PermissionCallback
import io.localai.client.BuildConfig
import org.json.JSONArray
import org.json.JSONObject
import java.net.InetAddress
import java.net.ServerSocket
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors

/** P2P pairing and forwarders over the gomobile library (contract: P2PPlugin in src/plugins/definitions.ts). */
@CapacitorPlugin(
    name = "P2P",
    permissions = [Permission(alias = P2PPlugin.NOTIFICATIONS, strings = [Manifest.permission.POST_NOTIFICATIONS])],
)
class P2PPlugin : Plugin() {
    // Pairing blocks for up to two minutes; it must not hold up other calls.
    private val work: ExecutorService = Executors.newCachedThreadPool()

    private val stateListener: (String, String) -> Unit = { id, line ->
        // Retained until the web side listens, so a line sent while it loads is not lost.
        notifyListeners("forwardState", JSObject().put("id", id).put("line", line), true)
    }

    private val keepListener: (Boolean) -> Unit = { on ->
        // Retained too: Disconnect is often tapped while the app is in the background.
        notifyListeners("keepInBackground", JSObject().put("on", on), true)
    }

    override fun load() {
        P2PRuntime.init(context)
        P2PRuntime.addListener(stateListener)
        P2PRuntime.addKeepListener(keepListener)
    }

    override fun handleOnDestroy() {
        P2PRuntime.removeListener(stateListener)
        P2PRuntime.removeKeepListener(keepListener)
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
            val answer = try {
                parsePairAnswer(P2PRuntime.engine.pairJson(P2PRuntime.identityPath, link, name, allowOpenRelay))
            } catch (e: P2PUnavailableException) {
                throw e
            } catch (e: Exception) {
                PairAnswer(ok = false, code = PAIR_FAILED, detail = e.message ?: "pairing failed")
            }
            val out = if (answer.ok) {
                JSObject().put("ok", true).put("json", keepCredential(answer.json))
            } else {
                JSObject().put("ok", false).put("code", answer.code).put("detail", answer.detail)
            }
            call.resolve(out)
        }
    }

    /**
     * Takes the device credential out of a pairing result and stores it for
     * the server's peer ID. The web layer gets the rest. A credential that
     * cannot be stored is dropped: the person signs in by hand, as with a
     * server that gives none.
     */
    private fun keepCredential(resultJson: String): String {
        val split = try {
            splitCredential(resultJson)
        } catch (_: org.json.JSONException) {
            // Never reached (parsePairAnswer made this JSON), and the message
            // would quote the answer: answer an empty result instead.
            return "{}"
        }
        val credential = split.credential ?: return split.json
        val store = P2PRuntime.credentials
        if (store == null || split.peer.isEmpty()) return split.json
        try {
            store.save(split.peer, credential)
        } catch (e: Exception) {
            // The type only: the message of a Keystore error is not needed, and nothing of the credential is logged.
            Log.w(TAG, "cannot store the device credential: ${e.javaClass.simpleName}")
        }
        return split.json
    }

    /** The person removed a server: its device credential goes too. */
    @PluginMethod
    fun forgetCredential(call: PluginCall) {
        val peer = call.getString("peer").orEmpty()
        background(call) {
            P2PRuntime.credentials?.delete(peer)
            call.resolve()
        }
    }

    /**
     * The person opened a server from the server list: a sign-out in its
     * WebUI no longer stops the automatic sign-in. The client core waits for
     * this before it opens the server. `wasSignedOut` tells it whether the
     * flag was set.
     */
    @PluginMethod
    fun allowAutoSignIn(call: PluginCall) {
        val peer = call.getString("peer").orEmpty()
        background(call) {
            val was = P2PRuntime.credentials?.allowAutoSignIn(peer) == true
            call.resolve(JSObject().put("wasSignedOut", was))
        }
    }

    /** Deletes the device credentials of servers that are no longer saved (`peers`: the saved ones). */
    @PluginMethod
    fun retainCredentials(call: PluginCall) {
        val arr = call.getArray("peers")
        if (arr == null) {
            call.reject("peers is required", "invalid")
            return
        }
        val peers = strings(arr)
        background(call) {
            P2PRuntime.credentials?.retain(peers)
            call.resolve()
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

    /**
     * When the service is (re)starting, for example because the app came
     * back after the background stop, this answers once the forwarders
     * listen again or after 3 s, so the web side reloads the WebUI view only
     * then.
     */
    @PluginMethod
    fun statuses(call: PluginCall) {
        background(call) {
            val until = SystemClock.elapsedRealtime() + RESTART_WAIT_MS
            while (P2PRuntime.starting() && SystemClock.elapsedRealtime() < until) Thread.sleep(50)
            call.resolve(statesObject(P2PRuntime.lastStates()))
        }
    }

    @PluginMethod
    fun setKeepInBackground(call: PluginCall) {
        P2PRuntime.setKeepInBackground(call.getBoolean("on", false) == true)
        call.resolve()
    }

    @PluginMethod
    fun keepInBackground(call: PluginCall) {
        call.resolve(JSObject().put("on", P2PRuntime.keepInBackground()))
    }

    /**
     * The service notification (with its Disconnect action) needs
     * POST_NOTIFICATIONS on Android 13+. Called when a P2P server is added;
     * the system dialog comes at most once per install.
     */
    @PluginMethod
    fun requestNotifications(call: PluginCall) {
        val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val granted = Build.VERSION.SDK_INT < NOTIFICATIONS_RUNTIME_SDK || getPermissionState(NOTIFICATIONS) == PermissionState.GRANTED
        when (notificationAsk(Build.VERSION.SDK_INT, granted, prefs.getBoolean(KEY_ASKED, false))) {
            NotificationAsk.GRANTED -> call.resolve(JSObject().put("granted", true))
            NotificationAsk.DENIED -> call.resolve(JSObject().put("granted", false))
            NotificationAsk.ASK -> {
                prefs.edit().putBoolean(KEY_ASKED, true).apply()
                requestPermissionForAlias(NOTIFICATIONS, call, "notificationsPermission")
            }
        }
    }

    @PermissionCallback
    private fun notificationsPermission(call: PluginCall) {
        call.resolve(JSObject().put("granted", getPermissionState(NOTIFICATIONS) == PermissionState.GRANTED))
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

    companion object {
        const val NOTIFICATIONS = "notifications"
        private const val TAG = "P2P"
        private const val RESTART_WAIT_MS = 3_000L
        private const val PREFS = "p2p_plugin"
        private const val KEY_ASKED = "notifications_asked"
    }
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
            hints = strings(o.optJSONArray("hints")),
        )
    }
    return out
}

/** The code of a pairing that failed for a reason the library has no code for. */
internal const val PAIR_FAILED = "failed"

/**
 * A pairing answer as the plugin resolves it: [json] is the result object when [ok]; otherwise
 * [code] is the library's stable error code (such as `needs_relay`) and [detail] its error text.
 */
internal data class PairAnswer(val ok: Boolean, val json: String = "", val code: String = "", val detail: String = "")

/** Reads the answer of the library's PairJSON. An answer that is not the expected JSON is a failure. */
internal fun parsePairAnswer(envelope: String): PairAnswer {
    val o = try {
        JSONObject(envelope)
    } catch (e: org.json.JSONException) {
        return PairAnswer(ok = false, code = PAIR_FAILED, detail = "unexpected answer from the P2P library")
    }
    if (o.optBoolean("ok", false)) {
        val result = o.optJSONObject("result")
            ?: return PairAnswer(ok = false, code = PAIR_FAILED, detail = "the P2P library answered no result")
        return PairAnswer(ok = true, json = result.toString())
    }
    val code = o.optString("code").ifEmpty { PAIR_FAILED }
    return PairAnswer(ok = false, code = code, detail = o.optString("detail").ifEmpty { "pairing failed" })
}

private fun strings(a: JSONArray?): List<String> {
    if (a == null) return emptyList()
    return (0 until a.length()).mapNotNull { a.opt(it) as? String }
}
