// SPDX-License-Identifier: MIT
package io.localai.client.links

import android.content.Intent
import android.util.Log
import com.getcapacitor.JSObject
import com.getcapacitor.Plugin
import com.getcapacitor.PluginCall
import com.getcapacitor.PluginMethod
import com.getcapacitor.annotation.CapacitorPlugin

/**
 * Invite links and shared text the app is opened with (contract: AppLinksPlugin
 * in src/plugins/definitions.ts).
 *
 * The plugin only hands text to the renderer. The renderer shows the invite
 * preview, and pairing starts only when the user taps Pair.
 */
@CapacitorPlugin(name = "AppLinks")
class AppLinksPlugin : Plugin() {
    private val launch = LaunchText<Intent>()

    override fun load() {
        val intent = activity?.intent ?: return
        // Android re-delivers the original intent when the app is opened again
        // from the recents screen; that is not a new link.
        val fromHistory = intent.flags and Intent.FLAG_ACTIVITY_LAUNCHED_FROM_HISTORY != 0
        launch.start(intent, if (fromHistory) null else textOf(intent))
    }

    @PluginMethod
    fun takeInitial(call: PluginCall) {
        val text = launch.take()
        val ret = JSObject()
        if (text != null) ret.put("text", text)
        call.resolve(ret)
    }

    override fun handleOnNewIntent(intent: Intent) {
        super.handleOnNewIntent(intent)
        // The launch intent comes through here too when the bridge starts;
        // LaunchText drops it, since takeInitial already delivers it.
        val text = launch.onNew(intent, textOf(intent)) ?: return
        // Kept until the renderer adds its listener, so a link that arrives
        // during start-up is not lost.
        notifyListeners("link", JSObject().put("text", text), true)
    }

    private fun textOf(intent: Intent): String? = try {
        val shared = if (intent.action == Intent.ACTION_SEND) {
            intent.getCharSequenceExtra(Intent.EXTRA_TEXT)?.toString()
        } else {
            null
        }
        IncomingText.fromIntent(intent.action, intent.dataString, shared)
    } catch (e: RuntimeException) {
        // Extras come from any app; a malformed bundle must not crash the app.
        Log.w(TAG, "ignored an intent with unreadable extras", e)
        null
    }

    private companion object {
        const val TAG = "AppLinks"
    }
}
