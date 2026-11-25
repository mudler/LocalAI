// SPDX-License-Identifier: MIT
package io.localai.client.p2p

import org.junit.Assert.assertEquals
import org.junit.Test

class NotificationAskTest {
    @Test
    fun beforeAndroid13NothingIsAsked() {
        assertEquals(NotificationAsk.GRANTED, notificationAsk(32, granted = false, askedBefore = false))
        assertEquals(NotificationAsk.GRANTED, notificationAsk(26, granted = false, askedBefore = true))
    }

    @Test
    fun grantedIsNeverAskedAgain() {
        assertEquals(NotificationAsk.GRANTED, notificationAsk(33, granted = true, askedBefore = false))
        assertEquals(NotificationAsk.GRANTED, notificationAsk(35, granted = true, askedBefore = true))
    }

    @Test
    fun theFirstP2PServerAsksOnce() {
        assertEquals(NotificationAsk.ASK, notificationAsk(33, granted = false, askedBefore = false))
        assertEquals(NotificationAsk.DENIED, notificationAsk(33, granted = false, askedBefore = true))
        assertEquals(NotificationAsk.DENIED, notificationAsk(36, granted = false, askedBefore = true))
    }
}
