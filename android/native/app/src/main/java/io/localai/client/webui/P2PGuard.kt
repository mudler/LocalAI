// SPDX-License-Identifier: MIT
package io.localai.client.webui

import io.localai.client.p2p.Forward
import io.localai.client.p2p.stateOfLine

/**
 * Any app on the phone can bind a loopback address. While this app's
 * forwarder of a P2P server does not listen, another app may hold the
 * server's address, so the WebUI view must not load that origin (it would
 * send the sign-in cookie there, or show the other app's page under the
 * server's name).
 */
object P2PGuard {
    private val LISTENING = setOf("ready", "up", "down")

    /**
     * null when `origin` is not the address of a forward of this app (a URL
     * server). Otherwise true while that forwarder listens (its last state is
     * ready, up or down) and false in any other state, or with no state yet.
     */
    fun listening(origin: String?, forwards: List<Forward>, states: Map<String, String>): Boolean? {
        val f = forwardOf(origin, forwards) ?: return null
        val state = states[f.id]?.let { stateOfLine(it) } ?: return false
        return state in LISTENING
    }

    /** The forward whose address is `origin`, or null (a URL server). */
    fun forwardOf(origin: String?, forwards: List<Forward>): Forward? {
        if (origin == null) return null
        return forwards.firstOrNull { "http://${it.host}:${it.port}" == origin }
    }
}
