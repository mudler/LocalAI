// SPDX-License-Identifier: MIT
package io.localai.client.update

import android.content.ActivityNotFoundException
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.pm.PackageInstaller
import android.os.Build
import android.util.Log

/** Receives the package installer's session status and passes it to the Updater plugin. */
class InstallStatusReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        val status = intent.getIntExtra(PackageInstaller.EXTRA_STATUS, PackageInstaller.STATUS_FAILURE)
        val message = intent.getStringExtra(PackageInstaller.EXTRA_STATUS_MESSAGE)
        var (name, text) = InstallStatusMapper.map(status, message)
        if (status == PackageInstaller.STATUS_PENDING_USER_ACTION) {
            // The system wants its own confirmation: show it. The user already
            // tapped "Update now"; this dialog is Android asking once more.
            val confirm = confirmIntent(intent)
            if (confirm == null) {
                name = "failure"
                text = "no confirmation screen"
            } else {
                try {
                    context.startActivity(confirm.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
                } catch (e: ActivityNotFoundException) {
                    Log.w(TAG, "cannot show the install confirmation", e)
                    name = "failure"
                    text = "no confirmation screen"
                }
            }
        }
        // On success the process is replaced; nothing is emitted that matters.
        listener?.invoke(name, text)
    }

    private fun confirmIntent(intent: Intent): Intent? =
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            intent.getParcelableExtra(Intent.EXTRA_INTENT, Intent::class.java)
        } else {
            @Suppress("DEPRECATION")
            intent.getParcelableExtra(Intent.EXTRA_INTENT)
        }

    companion object {
        private const val TAG = "InstallStatus"

        /** Set by the Updater plugin while it is loaded; receives (status, message). */
        @Volatile
        var listener: ((String, String?) -> Unit)? = null
    }
}
