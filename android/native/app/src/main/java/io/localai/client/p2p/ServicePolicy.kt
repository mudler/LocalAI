// SPDX-License-Identifier: MIT
package io.localai.client.p2p

/** How long the P2P service keeps running after the app leaves the screen. */
const val BACKGROUND_GRACE_MS = 600_000L

data class PolicyInput(val p2pServers: Int, val foreground: Boolean, val keepInBackground: Boolean)

sealed interface PolicyAction {
    object Start : PolicyAction
    object Stop : PolicyAction
    data class StopAt(val atMs: Long) : PolicyAction
    object CancelStop : PolicyAction
    object None : PolicyAction
}

/**
 * When the P2P foreground service runs: while at least one P2P server is
 * saved and the app is on screen, and for [BACKGROUND_GRACE_MS] after it
 * leaves, unless the user keeps P2P in the background. The service is only
 * ever started from the foreground: Android 12+ refuses a foreground service
 * started from the background. [running] means started or starting.
 */
class ServicePolicy {
    private var stopAt: Long? = null

    fun onChange(input: PolicyInput, nowMs: Long, running: Boolean): PolicyAction {
        if (input.p2pServers <= 0) {
            stopAt = null
            return if (running) PolicyAction.Stop else PolicyAction.None
        }
        if (input.foreground) {
            if (!running) {
                stopAt = null
                return PolicyAction.Start
            }
            return cancelPending()
        }
        if (!running) {
            stopAt = null
            return PolicyAction.None
        }
        if (input.keepInBackground) return cancelPending()
        if (stopAt != null) return PolicyAction.None
        val at = nowMs + BACKGROUND_GRACE_MS
        stopAt = at
        return PolicyAction.StopAt(at)
    }

    fun onTimer(input: PolicyInput, nowMs: Long, running: Boolean): PolicyAction {
        val at = stopAt ?: return PolicyAction.None
        if (input.p2pServers > 0 && (input.foreground || input.keepInBackground)) {
            stopAt = null
            return PolicyAction.None
        }
        // A timer that fired early waits again for the same deadline.
        if (input.p2pServers > 0 && nowMs < at) return PolicyAction.StopAt(at)
        stopAt = null
        return if (running) PolicyAction.Stop else PolicyAction.None
    }

    private fun cancelPending(): PolicyAction {
        if (stopAt == null) return PolicyAction.None
        stopAt = null
        return PolicyAction.CancelStop
    }
}
