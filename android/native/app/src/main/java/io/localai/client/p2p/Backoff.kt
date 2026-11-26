// SPDX-License-Identifier: MIT
package io.localai.client.p2p

/** Restart delays of a forwarder; the same rule as backoffMs in launcher/src/core/p2pInvite.ts. */
object Backoff {
    private const val FIRST_MS = 1_000L
    private const val MAX_MS = 60_000L

    fun delayMs(n: Int): Long = minOf(MAX_MS, FIRST_MS shl n.coerceIn(0, 6))
}
