// SPDX-License-Identifier: MIT
package io.localai.client.update

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent

/** Receives the package installer's session status. Stub: ignores it. */
class InstallStatusReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) = Unit
}
