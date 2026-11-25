// SPDX-License-Identifier: MIT
package io.localai.client.webui

import com.getcapacitor.JSObject
import org.junit.Assert.assertEquals
import org.junit.Test

class WebuiViewPluginArgsTest {
    @Test
    fun missingFieldsGiveDefaults() {
        assertEquals(Layout(visible = false, shellAbove = false, top = 0f, bottom = 0f, barHeight = 0f), parseLayout(JSObject()))
    }

    @Test
    fun readsEveryField() {
        val o = JSObject("""{"visible":true,"shellAbove":true,"top":24,"bottom":12.5,"barHeight":48}""")
        assertEquals(Layout(visible = true, shellAbove = true, top = 24f, bottom = 12.5f, barHeight = 48f), parseLayout(o))
    }

    @Test
    fun negativeAndBadValuesClampToZero() {
        val o = JSObject("""{"visible":"yes","shellAbove":1,"top":-5,"bottom":"x","barHeight":-0.5}""")
        assertEquals(Layout(visible = false, shellAbove = false, top = 0f, bottom = 0f, barHeight = 0f), parseLayout(o))
    }

    @Test
    fun hugeValuesAreCapped() {
        val o = JSObject("""{"visible":true,"shellAbove":false,"top":1e12,"bottom":0,"barHeight":0}""")
        assertEquals(MAX_LAYOUT_DP, parseLayout(o).top, 0f)
    }

    @Test
    fun convertsDpToPixels() {
        assertEquals(0, dpToPx(0f, 2.75f))
        assertEquals(132, dpToPx(48f, 2.75f))
        assertEquals(34, dpToPx(12.5f, 2.75f))
    }
}
