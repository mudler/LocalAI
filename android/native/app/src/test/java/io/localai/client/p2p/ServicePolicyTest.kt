// SPDX-License-Identifier: MIT
package io.localai.client.p2p

import org.junit.Assert.assertEquals
import org.junit.Test

class ServicePolicyTest {
    private val fg = PolicyInput(p2pServers = 1, foreground = true, keepInBackground = false)
    private val bg = fg.copy(foreground = false)
    private val t = 1_000_000L

    @Test
    fun graceIsTenMinutes() {
        assertEquals(600_000L, BACKGROUND_GRACE_MS)
    }

    @Test
    fun foregroundWithAServerStartsTheService() {
        val p = ServicePolicy()
        assertEquals(PolicyAction.Start, p.onChange(fg, t, running = false))
        assertEquals(PolicyAction.None, p.onChange(fg, t + 1, running = true))
    }

    @Test
    fun noServerStopsARunningService() {
        val p = ServicePolicy()
        assertEquals(PolicyAction.Stop, p.onChange(fg.copy(p2pServers = 0), t, running = true))
        assertEquals(PolicyAction.None, p.onChange(fg.copy(p2pServers = 0), t, running = false))
    }

    @Test
    fun lastServerRemovedInTheForegroundStops() {
        val p = ServicePolicy()
        assertEquals(PolicyAction.Start, p.onChange(fg, t, running = false))
        assertEquals(PolicyAction.Stop, p.onChange(fg.copy(p2pServers = 0), t + 5_000, running = true))
    }

    @Test
    fun backgroundSchedulesAStopAfterTheGrace() {
        val p = ServicePolicy()
        assertEquals(PolicyAction.StopAt(t + 600_000), p.onChange(bg, t, running = true))
        // A second background event keeps the first deadline.
        assertEquals(PolicyAction.None, p.onChange(bg, t + 60_000, running = true))
    }

    @Test
    fun returningBeforeTheTimerCancelsTheStop() {
        val p = ServicePolicy()
        p.onChange(bg, t, running = true)
        assertEquals(PolicyAction.CancelStop, p.onChange(fg, t + 300_000, running = true))
        assertEquals(PolicyAction.None, p.onTimer(fg, t + 600_000, running = true))
    }

    // The app is in the background when the timer ends, then comes back.
    @Test
    fun timerStopsInTheBackgroundAndTheAppStartsItAgainOnReturn() {
        val p = ServicePolicy()
        assertEquals(PolicyAction.StopAt(t + 600_000), p.onChange(bg, t, running = true))
        assertEquals(PolicyAction.Stop, p.onTimer(bg, t + 600_000, running = true))
        assertEquals(PolicyAction.Start, p.onChange(fg, t + 700_000, running = false))
    }

    @Test
    fun earlyTimerKeepsTheDeadline() {
        val p = ServicePolicy()
        p.onChange(bg, t, running = true)
        assertEquals(PolicyAction.StopAt(t + 600_000), p.onTimer(bg, t + 599_000, running = true))
        assertEquals(PolicyAction.Stop, p.onTimer(bg, t + 600_000, running = true))
    }

    @Test
    fun keepInBackgroundNeverStops() {
        val p = ServicePolicy()
        val keep = bg.copy(keepInBackground = true)
        assertEquals(PolicyAction.None, p.onChange(keep, t, running = true))
        assertEquals(PolicyAction.None, p.onTimer(keep, t + 600_000, running = true))
        assertEquals(PolicyAction.None, p.onChange(keep, t + 3_600_000, running = true))
    }

    @Test
    fun keepTurnedOnDuringTheGraceCancelsTheStop() {
        val p = ServicePolicy()
        p.onChange(bg, t, running = true)
        assertEquals(PolicyAction.CancelStop, p.onChange(bg.copy(keepInBackground = true), t + 1_000, running = true))
        assertEquals(PolicyAction.None, p.onTimer(bg, t + 600_000, running = true))
    }

    @Test
    fun keepTurnedOffInTheBackgroundStartsTheGrace() {
        val p = ServicePolicy()
        p.onChange(bg.copy(keepInBackground = true), t, running = true)
        assertEquals(PolicyAction.StopAt(t + 5_000 + 600_000), p.onChange(bg, t + 5_000, running = true))
    }

    // Android 12+ refuses a foreground service start from the background.
    @Test
    fun neverStartsInTheBackground() {
        val p = ServicePolicy()
        assertEquals(PolicyAction.None, p.onChange(bg, t, running = false))
        assertEquals(PolicyAction.None, p.onChange(bg.copy(keepInBackground = true), t, running = false))
    }

    @Test
    fun timerWithoutAPendingStopDoesNothing() {
        val p = ServicePolicy()
        assertEquals(PolicyAction.None, p.onTimer(bg, t, running = true))
    }

    @Test
    fun serverRemovedDuringTheGraceStopsAtOnce() {
        val p = ServicePolicy()
        p.onChange(bg, t, running = true)
        assertEquals(PolicyAction.Stop, p.onChange(bg.copy(p2pServers = 0), t + 1_000, running = true))
        assertEquals(PolicyAction.None, p.onTimer(bg, t + 600_000, running = false))
    }
}
