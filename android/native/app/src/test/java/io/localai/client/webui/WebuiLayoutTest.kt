// SPDX-License-Identifier: MIT
package io.localai.client.webui

import org.junit.Assert.assertEquals
import org.junit.Test

class WebuiLayoutTest {
    @Test
    fun withoutTheKeyboardTheViewKeepsRoomForTheStripTheBarAndTheNavigationBar() {
        assertEquals(40 + 144 + 63, WebuiLayout.bottomMargin(imeBottom = 0, clearedBelow = 0, shellBottom = 40, barHeight = 144, navBottom = 63))
    }

    @Test
    fun aKeyboardTheContainerAlreadyClearsAddsNoMargin() {
        // Capacitor pads the root by the keyboard height: the container ends at the keyboard.
        assertEquals(0, WebuiLayout.bottomMargin(imeBottom = 900, clearedBelow = 900, shellBottom = 40, barHeight = 0, navBottom = 0))
    }

    @Test
    fun aKeyboardOverTheContainerBecomesTheMargin() {
        // Nothing resized the container: the view ends at the keyboard on its own.
        assertEquals(900, WebuiLayout.bottomMargin(imeBottom = 900, clearedBelow = 0, shellBottom = 40, barHeight = 0, navBottom = 0))
        // Only the part of the keyboard that still covers the container counts.
        assertEquals(300, WebuiLayout.bottomMargin(imeBottom = 900, clearedBelow = 600, shellBottom = 40, barHeight = 0, navBottom = 0))
    }

    @Test
    fun clearedSpaceTallerThanTheKeyboardGivesNoNegativeMargin() {
        assertEquals(0, WebuiLayout.bottomMargin(imeBottom = 300, clearedBelow = 900, shellBottom = 0, barHeight = 0, navBottom = 0))
        assertEquals(300, WebuiLayout.bottomMargin(imeBottom = 300, clearedBelow = -5, shellBottom = 0, barHeight = 0, navBottom = 0))
    }
}
