// SPDX-License-Identifier: MIT
package io.localai.client.p2p

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

// A switch from Wi-Fi to mobile data (or VPN on/off) restarts every forwarder.
class NetworkPolicyTest {
    private val wifi = setOf("wifi")
    private val cell = setOf("cellular")

    private fun registered(): NetworkPolicy = NetworkPolicy().also { assertNull(it.onDefaultNetwork(100, wifi, 0)) }

    @Test
    fun firstCallbackAfterRegistrationIsIgnored() {
        val p = NetworkPolicy()
        assertNull(p.onDefaultNetwork(100, wifi, 0))
        assertFalse(p.onTimer(10_000))
    }

    @Test
    fun wifiToCellularRestartsAfterOneSecond() {
        val p = registered()
        assertEquals(5_000L + 1_000, p.onDefaultNetwork(200, cell, 5_000))
        assertFalse(p.onTimer(5_999))
        assertTrue(p.onTimer(6_000))
        assertFalse(p.onTimer(7_000))
    }

    @Test
    fun twoChangesInTheWindowGiveOneRestartAfterTheSecond() {
        val p = registered()
        assertEquals(6_000L, p.onDefaultNetwork(200, cell, 5_000))
        assertEquals(6_300L, p.onDefaultNetwork(300, wifi, 5_300))
        assertFalse(p.onTimer(6_000))
        assertTrue(p.onTimer(6_300))
        assertFalse(p.onTimer(9_000))
    }

    @Test
    fun sameNetworkAgainDoesNothing() {
        val p = registered()
        assertNull(p.onDefaultNetwork(100, wifi, 5_000))
        assertFalse(p.onTimer(10_000))
    }

    @Test
    fun vpnOnRestarts() {
        val p = registered()
        assertEquals(6_000L, p.onDefaultNetwork(100, setOf("wifi", "vpn"), 5_000))
        assertTrue(p.onTimer(6_000))
    }

    @Test
    fun vpnOffRestarts() {
        val p = NetworkPolicy()
        p.onDefaultNetwork(100, setOf("wifi", "vpn"), 0)
        assertEquals(6_000L, p.onDefaultNetwork(100, wifi, 5_000))
    }

    @Test
    fun lostAloneSchedulesNothing() {
        val p = registered()
        assertNull(p.onDefaultNetwork(null, emptySet(), 5_000))
        assertFalse(p.onTimer(60_000))
    }

    @Test
    fun lostThenTheSameNetworkBackRestarts() {
        val p = registered()
        assertNull(p.onDefaultNetwork(null, emptySet(), 5_000))
        assertEquals(9_000L, p.onDefaultNetwork(100, wifi, 8_000))
        assertTrue(p.onTimer(9_000))
    }

    @Test
    fun lostDoesNotCancelAPendingRestart() {
        val p = registered()
        assertEquals(6_000L, p.onDefaultNetwork(200, cell, 5_000))
        assertNull(p.onDefaultNetwork(null, emptySet(), 5_500))
        assertTrue(p.onTimer(6_000))
    }

    @Test
    fun debounceIsConfigurable() {
        val p = NetworkPolicy(debounceMs = 250)
        p.onDefaultNetwork(100, wifi, 0)
        assertEquals(1_250L, p.onDefaultNetwork(200, cell, 1_000))
    }
}
