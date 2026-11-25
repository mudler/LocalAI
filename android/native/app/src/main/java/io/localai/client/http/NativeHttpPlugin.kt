// SPDX-License-Identifier: MIT
package io.localai.client.http

import com.getcapacitor.Plugin
import com.getcapacitor.PluginCall
import com.getcapacitor.PluginMethod
import com.getcapacitor.annotation.CapacitorPlugin

/** HTTP requests for the connection test (contract: NativeHttpPlugin in src/plugins/definitions.ts). */
@CapacitorPlugin(name = "NativeHttp")
class NativeHttpPlugin : Plugin() {
    @PluginMethod
    fun get(call: PluginCall) = call.reject(NOT_IMPLEMENTED)

    @PluginMethod
    fun cancel(call: PluginCall) = call.reject(NOT_IMPLEMENTED)

    private companion object {
        const val NOT_IMPLEMENTED = "not implemented"
    }
}
