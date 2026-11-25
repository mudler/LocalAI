// SPDX-License-Identifier: MIT
package io.localai.client.scan

import android.Manifest
import android.content.pm.PackageManager
import androidx.activity.result.ActivityResult
import com.getcapacitor.JSObject
import com.getcapacitor.PermissionState
import com.getcapacitor.Plugin
import com.getcapacitor.PluginCall
import com.getcapacitor.PluginMethod
import com.getcapacitor.annotation.ActivityCallback
import com.getcapacitor.annotation.CapacitorPlugin
import com.getcapacitor.annotation.Permission
import com.getcapacitor.annotation.PermissionCallback
import com.journeyapps.barcodescanner.ScanContract
import com.journeyapps.barcodescanner.ScanOptions
import io.localai.client.R

/**
 * QR code scan with the camera (contract: ScannerPlugin in src/plugins/definitions.ts).
 *
 * zxing decodes on the device, so scanning works without Google Play
 * services. The plugin returns the raw text; the renderer checks it with
 * `readInvite` and never pairs without a tap.
 *
 * Rejection codes: `no-camera` (the device has none), `permission-denied`.
 */
@CapacitorPlugin(
    name = "Scanner",
    permissions = [Permission(alias = ScannerPlugin.CAMERA, strings = [Manifest.permission.CAMERA])],
)
class ScannerPlugin : Plugin() {
    @PluginMethod
    fun scan(call: PluginCall) {
        if (!context.packageManager.hasSystemFeature(PackageManager.FEATURE_CAMERA_ANY)) {
            call.reject("This device has no camera.", "no-camera")
            return
        }
        // The camera permission is asked only now, when the user taps Scan.
        if (getPermissionState(CAMERA) != PermissionState.GRANTED) {
            requestPermissionForAlias(CAMERA, call, "cameraPermission")
            return
        }
        startScan(call)
    }

    @PermissionCallback
    private fun cameraPermission(call: PluginCall) {
        if (getPermissionState(CAMERA) == PermissionState.GRANTED) {
            startScan(call)
        } else {
            call.reject("Camera permission was denied.", "permission-denied")
        }
    }

    private fun startScan(call: PluginCall) {
        val options = ScanOptions()
            .setDesiredBarcodeFormats(ScanOptions.QR_CODE)
            .setBeepEnabled(false)
            .setOrientationLocked(true)
            .setBarcodeImageEnabled(false)
            .setPrompt(context.getString(R.string.scanner_prompt))
        // Capacitor owns the activity result launchers, so the plugin launches
        // the contract's intent itself and parses the result with the same
        // contract.
        startActivityForResult(call, ScanContract().createIntent(context, options), "scanResult")
    }

    @ActivityCallback
    private fun scanResult(call: PluginCall?, result: ActivityResult) {
        if (call == null) return
        val text = ScanContract().parseResult(result.resultCode, result.data).contents
        val ret = JSObject()
        if (text == null) {
            ret.put("cancelled", true)
        } else {
            ret.put("text", text)
            ret.put("cancelled", false)
        }
        call.resolve(ret)
    }

    companion object {
        const val CAMERA = "camera"
    }
}
