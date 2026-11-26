// SPDX-License-Identifier: MIT
package io.localai.client.webui

import android.util.Log
import android.view.ViewGroup
import com.getcapacitor.JSObject
import com.getcapacitor.Plugin
import com.getcapacitor.PluginCall
import com.getcapacitor.PluginMethod
import com.getcapacitor.annotation.CapacitorPlugin
import kotlin.math.roundToInt

/** The WebUI view layout the client core sends, in dp (contract: WebuiLayout). */
data class Layout(val visible: Boolean, val shellAbove: Boolean, val top: Float, val bottom: Float, val barHeight: Float)

/** No layout value is larger than this many dp; anything bigger is a bug or hostile. */
internal const val MAX_LAYOUT_DP = 10_000f

/**
 * Reads setLayout's argument. A missing or non-boolean flag is false; a
 * missing, non-numeric, negative or NaN size is 0.
 */
internal fun parseLayout(o: JSObject): Layout = Layout(
    visible = o.opt("visible") == true,
    shellAbove = o.opt("shellAbove") == true,
    top = dpOf(o, "top"),
    bottom = dpOf(o, "bottom"),
    barHeight = dpOf(o, "barHeight"),
)

private fun dpOf(o: JSObject, name: String): Float {
    val v = o.opt(name) as? Number ?: return 0f
    val d = v.toDouble()
    if (d.isNaN() || d <= 0.0) return 0f
    return minOf(d, MAX_LAYOUT_DP.toDouble()).toFloat()
}

internal fun dpToPx(dp: Float, density: Float): Int = (dp * density).roundToInt()

/** The server WebUI in a second, locked WebView (contract: WebuiViewPlugin in src/plugins/definitions.ts). */
@CapacitorPlugin(name = "WebuiView")
class WebuiViewPlugin : Plugin() {
    private var host: WebuiHost? = null

    private val events = object : WebuiEvents {
        // A load result or a crash must reach the client core even when its
        // listener is attached late, so those events are kept until consumed.
        override fun loaded() = notifyListeners("loaded", JSObject(), true)

        override fun loadFailed(code: Int, description: String) =
            notifyListeners("loadFailed", JSObject().put("code", code).put("description", description), true)

        override fun crashed(reason: String) = notifyListeners("crashed", JSObject().put("reason", reason), true)

        override fun unresponsive() = notifyListeners("unresponsive", JSObject())

        override fun responsive() = notifyListeners("responsive", JSObject())

        override fun permissionChoice(origin: String, choice: String, remember: Boolean) = notifyListeners(
            "permissionChoice",
            JSObject().put("origin", origin).put("choice", choice).put("remember", remember),
        )

        override fun openExternal(url: String) = notifyListeners("openExternal", JSObject().put("url", url))
    }

    override fun load() {
        val shell = bridge.webView
        val container = shell.parent as? ViewGroup
        if (container == null) {
            Log.e(TAG, "the Capacitor WebView has no parent view; the WebUI view is unavailable")
            return
        }
        host = WebuiHost(activity, container, events, shell)
    }

    @PluginMethod
    fun show(call: PluginCall) {
        val origin = call.getString("origin")
        val url = call.getString("url")
        val profile = call.getString("profile")
        if (origin == null || url == null || profile == null) {
            call.reject("origin, url and profile are required", INVALID)
            return
        }
        onUi(call) {
            it.show(origin, url, profile)
            call.resolve()
        }
    }

    @PluginMethod
    fun navigate(call: PluginCall) {
        val url = call.getString("url") ?: return call.reject("url is required", INVALID)
        onUi(call) {
            it.navigate(url)
            call.resolve()
        }
    }

    @PluginMethod
    fun close(call: PluginCall) = onUi(call) {
        it.close()
        call.resolve()
    }

    @PluginMethod
    fun reload(call: PluginCall) = onUi(call) {
        it.reload()
        call.resolve()
    }

    @PluginMethod
    fun setLayout(call: PluginCall) {
        val l = parseLayout(call.data)
        onUi(call) {
            val density = activity.resources.displayMetrics.density
            it.setLayout(l.visible, l.shellAbove, dpToPx(l.top, density), dpToPx(l.bottom, density), dpToPx(l.barHeight, density))
            call.resolve()
        }
    }

    @PluginMethod
    fun setPermissionStore(call: PluginCall) {
        val json = call.getString("json") ?: return call.reject("json is required", INVALID)
        // The store is thread-safe; no need to wait for the UI thread.
        val h = host ?: return call.reject(UNAVAILABLE, UNAVAILABLE_CODE)
        h.setPermissionStore(json)
        call.resolve()
    }

    @PluginMethod
    fun goBack(call: PluginCall) = onUi(call) {
        call.resolve(JSObject().put("handled", it.goBack()))
    }

    @PluginMethod
    fun currentUrl(call: PluginCall) = onUi(call) {
        val out = JSObject()
        it.currentUrl()?.let { url -> out.put("url", url) }
        call.resolve(out)
    }

    override fun handleOnPause() {
        super.handleOnPause()
        host?.onPause()
    }

    override fun handleOnResume() {
        super.handleOnResume()
        host?.onResume()
    }

    override fun handleOnDestroy() {
        host?.onDestroy()
        host = null
        super.handleOnDestroy()
    }

    /** Runs `block` with the host on the UI thread; a bad argument rejects the call. */
    private fun onUi(call: PluginCall, block: (WebuiHost) -> Unit) {
        val h = host ?: return call.reject(UNAVAILABLE, UNAVAILABLE_CODE)
        activity.runOnUiThread {
            try {
                block(h)
            } catch (e: IllegalArgumentException) {
                call.reject(e.message ?: "invalid argument", INVALID)
            }
        }
    }

    private companion object {
        const val TAG = "WebuiViewPlugin"
        const val INVALID = "invalid"
        const val UNAVAILABLE = "the WebUI view is unavailable"
        const val UNAVAILABLE_CODE = "unavailable"
    }
}
