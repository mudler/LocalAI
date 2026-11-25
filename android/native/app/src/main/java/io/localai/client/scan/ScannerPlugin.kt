// SPDX-License-Identifier: MIT
package io.localai.client.scan

import com.getcapacitor.Plugin
import com.getcapacitor.PluginCall
import com.getcapacitor.PluginMethod
import com.getcapacitor.annotation.CapacitorPlugin

/** QR code scan with the camera (contract: ScannerPlugin in src/plugins/definitions.ts). */
@CapacitorPlugin(name = "Scanner")
class ScannerPlugin : Plugin() {
    @PluginMethod
    fun scan(call: PluginCall) = call.reject("not implemented")
}
