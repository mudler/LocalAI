// SPDX-License-Identifier: MIT
package io.localai.client

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class BuildConfigTest {
    @Test
    fun channelMatchesApplicationId() {
        val expected = when (BuildConfig.APPLICATION_ID) {
            "io.localai.client" -> "stable"
            "io.localai.client.dev" -> "dev"
            else -> "unknown application id ${BuildConfig.APPLICATION_ID}"
        }
        assertEquals(expected, BuildConfig.CHANNEL)
    }

    @Test
    fun defaultUpdateFeedIsHttps() {
        assertTrue(BuildConfig.UPDATE_FEED_BASE, BuildConfig.UPDATE_FEED_BASE.startsWith("https://"))
    }
}
