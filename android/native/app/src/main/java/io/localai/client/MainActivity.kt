// SPDX-License-Identifier: MIT
package io.localai.client

import android.os.Bundle
import com.getcapacitor.BridgeActivity
import io.localai.client.http.NativeHttpPlugin
import io.localai.client.links.AppLinksPlugin
import io.localai.client.p2p.P2PPlugin
import io.localai.client.scan.ScannerPlugin
import io.localai.client.update.UpdaterPlugin
import io.localai.client.webui.WebuiViewPlugin

class MainActivity : BridgeActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        // The app's own plugins are not npm packages, so Capacitor does not
        // find them; they must be registered before the bridge starts.
        registerPlugin(NativeHttpPlugin::class.java)
        registerPlugin(WebuiViewPlugin::class.java)
        registerPlugin(P2PPlugin::class.java)
        registerPlugin(UpdaterPlugin::class.java)
        registerPlugin(ScannerPlugin::class.java)
        registerPlugin(AppLinksPlugin::class.java)
        super.onCreate(savedInstanceState)
    }
}
