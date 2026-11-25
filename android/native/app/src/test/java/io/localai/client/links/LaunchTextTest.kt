// SPDX-License-Identifier: MIT
package io.localai.client.links

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class LaunchTextTest {
    private val launch = Any()
    private val link = "localai://pair?v=1"

    @Test
    fun launchTextIsTakenOnce() {
        val t = LaunchText<Any>()
        t.start(launch, link)
        assertEquals(link, t.take())
        assertNull(t.take())
    }

    @Test
    fun launchIntentPassedAgainIsNotAnEvent() {
        // Capacitor hands the launch intent to handleOnNewIntent when the bridge
        // starts; the link must reach the renderer once, through take().
        val t = LaunchText<Any>()
        t.start(launch, link)
        assertNull(t.onNew(launch, link))
        assertEquals(link, t.take())
    }

    @Test
    fun launchIntentPassedAfterTakeIsStillNotAnEvent() {
        val t = LaunchText<Any>()
        t.start(launch, link)
        assertEquals(link, t.take())
        assertNull(t.onNew(launch, link))
    }

    @Test
    fun laterIntentsAreEvents() {
        val t = LaunchText<Any>()
        t.start(launch, link)
        assertNull(t.onNew(launch, link))
        val next = Any()
        assertEquals("localai://pair?v=2", t.onNew(next, "localai://pair?v=2"))
        assertEquals(link, t.take())
    }

    @Test
    fun theSameLinkInANewIntentIsAnEvent() {
        // Opening the same link again later is a new user action.
        val t = LaunchText<Any>()
        t.start(launch, link)
        assertNull(t.onNew(launch, link))
        assertEquals(link, t.onNew(Any(), link))
    }

    @Test
    fun intentsWithoutTextAreNotEvents() {
        val t = LaunchText<Any>()
        t.start(launch, null)
        assertNull(t.take())
        assertNull(t.onNew(Any(), null))
    }

    @Test
    fun withoutStartEveryIntentIsAnEvent() {
        val t = LaunchText<Any>()
        assertNull(t.take())
        assertEquals(link, t.onNew(launch, link))
    }
}
