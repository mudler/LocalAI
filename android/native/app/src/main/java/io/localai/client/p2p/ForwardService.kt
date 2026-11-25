// SPDX-License-Identifier: MIT
package io.localai.client.p2p

import android.app.Service
import android.content.Intent
import android.os.IBinder

/** The foreground service that will own the P2P forwarders. Stub: stops at once. */
class ForwardService : Service() {
    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        stopSelf(startId)
        return START_NOT_STICKY
    }
}
