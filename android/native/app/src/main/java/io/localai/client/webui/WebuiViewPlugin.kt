// SPDX-License-Identifier: MIT
package io.localai.client.webui

import com.getcapacitor.Plugin
import com.getcapacitor.PluginCall
import com.getcapacitor.PluginMethod
import com.getcapacitor.annotation.CapacitorPlugin

/** The server WebUI in a second, locked WebView (contract: WebuiViewPlugin in src/plugins/definitions.ts). */
@CapacitorPlugin(name = "WebuiView")
class WebuiViewPlugin : Plugin() {
    @PluginMethod
    fun show(call: PluginCall) = call.reject(NOT_IMPLEMENTED)

    @PluginMethod
    fun navigate(call: PluginCall) = call.reject(NOT_IMPLEMENTED)

    @PluginMethod
    fun close(call: PluginCall) = call.reject(NOT_IMPLEMENTED)

    @PluginMethod
    fun reload(call: PluginCall) = call.reject(NOT_IMPLEMENTED)

    @PluginMethod
    fun setLayout(call: PluginCall) = call.reject(NOT_IMPLEMENTED)

    @PluginMethod
    fun setPermissionStore(call: PluginCall) = call.reject(NOT_IMPLEMENTED)

    @PluginMethod
    fun goBack(call: PluginCall) = call.reject(NOT_IMPLEMENTED)

    @PluginMethod
    fun currentUrl(call: PluginCall) = call.reject(NOT_IMPLEMENTED)

    private companion object {
        const val NOT_IMPLEMENTED = "not implemented"
    }
}
