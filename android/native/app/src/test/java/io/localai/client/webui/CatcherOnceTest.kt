// SPDX-License-Identifier: MIT
package io.localai.client.webui

import org.junit.Assert.assertEquals
import org.junit.Test

/** Every window catcher must be destroyed (released) once. */
class CatcherOnceTest {
    private val urls = ArrayList<String>()
    private var released = 0
    private val once = CatcherOnce(onUrl = { urls += it }, release = { released++ })

    @Test
    fun aCatcherThatGotAUrlIsReleasedOnce() {
        once.take("https://ai.example.com/app/chat")
        once.take("https://ai.example.com/other")
        once.expire()
        assertEquals(listOf("https://ai.example.com/app/chat"), urls)
        assertEquals(1, released)
    }

    @Test
    fun aCatcherThatNeverGotAUrlIsReleasedWhenItExpires() {
        once.expire()
        once.expire()
        once.take("https://ai.example.com/late")
        assertEquals(emptyList<String>(), urls)
        assertEquals(1, released)
    }
}
