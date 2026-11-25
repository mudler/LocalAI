// SPDX-License-Identifier: MIT
package io.localai.client.links

import com.getcapacitor.Plugin
import com.getcapacitor.PluginCall
import com.getcapacitor.PluginMethod
import com.getcapacitor.annotation.CapacitorPlugin

/** Invite links and shared text the app is opened with (contract: AppLinksPlugin in src/plugins/definitions.ts). */
@CapacitorPlugin(name = "AppLinks")
class AppLinksPlugin : Plugin() {
    @PluginMethod
    fun takeInitial(call: PluginCall) = call.reject("not implemented")
}
