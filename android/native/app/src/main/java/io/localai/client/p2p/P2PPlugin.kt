// SPDX-License-Identifier: MIT
package io.localai.client.p2p

import com.getcapacitor.Plugin
import com.getcapacitor.PluginCall
import com.getcapacitor.PluginMethod
import com.getcapacitor.annotation.CapacitorPlugin
import io.localai.client.BuildConfig

/** P2P pairing and forwarders over the gomobile library (contract: P2PPlugin in src/plugins/definitions.ts). */
@CapacitorPlugin(name = "P2P")
class P2PPlugin : Plugin() {
    @PluginMethod
    fun version(call: PluginCall) = stub(call)

    @PluginMethod
    fun deviceName(call: PluginCall) = stub(call)

    @PluginMethod
    fun peerId(call: PluginCall) = stub(call)

    @PluginMethod
    fun pair(call: PluginCall) = stub(call)

    @PluginMethod
    fun freePort(call: PluginCall) = stub(call)

    @PluginMethod
    fun setForwards(call: PluginCall) = stub(call)

    @PluginMethod
    fun statuses(call: PluginCall) = stub(call)

    @PluginMethod
    fun setKeepInBackground(call: PluginCall) = stub(call)

    // A build without the P2P library (no AAR) says so, so the app can hide P2P.
    private fun stub(call: PluginCall) {
        if (BuildConfig.P2P_LIBRARY) call.reject("not implemented") else call.reject("unavailable", "unavailable")
    }
}
