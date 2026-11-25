// SPDX-License-Identifier: MIT
package io.localai.client.p2p

import org.junit.Assert.assertEquals
import org.junit.Test

class BackoffTest {
    // The same values as backoffMs(n) in launcher/src/core/p2pInvite.ts:
    // Math.min(60_000, 1000 * 2 ** Math.min(n, 6)).
    @Test
    fun matchesTheDesktopBackoff() {
        val expected = mapOf(
            0 to 1_000L,
            1 to 2_000L,
            2 to 4_000L,
            3 to 8_000L,
            4 to 16_000L,
            5 to 32_000L,
            6 to 60_000L, // 64 000 clamped
            7 to 60_000L,
            10 to 60_000L,
            1_000 to 60_000L,
        )
        for ((n, ms) in expected) assertEquals("n=$n", ms, Backoff.delayMs(n))
    }

    @Test
    fun negativeAttemptIsTheFirstDelay() {
        assertEquals(1_000L, Backoff.delayMs(-1))
    }
}
