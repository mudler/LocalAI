// SPDX-License-Identifier: MIT
package io.localai.client.update

import com.getcapacitor.Plugin
import com.getcapacitor.PluginCall
import com.getcapacitor.PluginMethod
import com.getcapacitor.annotation.CapacitorPlugin

/** Self-update from the release feed (contract: UpdaterPlugin in src/plugins/definitions.ts). */
@CapacitorPlugin(name = "Updater")
class UpdaterPlugin : Plugin() {
    @PluginMethod
    fun fetchText(call: PluginCall) = call.reject(NOT_IMPLEMENTED)

    @PluginMethod
    fun download(call: PluginCall) = call.reject(NOT_IMPLEMENTED)

    @PluginMethod
    fun verifySigner(call: PluginCall) = call.reject(NOT_IMPLEMENTED)

    @PluginMethod
    fun canInstall(call: PluginCall) = call.reject(NOT_IMPLEMENTED)

    @PluginMethod
    fun openInstallSettings(call: PluginCall) = call.reject(NOT_IMPLEMENTED)

    @PluginMethod
    fun install(call: PluginCall) = call.reject(NOT_IMPLEMENTED)

    @PluginMethod
    fun appInfo(call: PluginCall) = call.reject(NOT_IMPLEMENTED)

    private companion object {
        const val NOT_IMPLEMENTED = "not implemented"
    }
}
