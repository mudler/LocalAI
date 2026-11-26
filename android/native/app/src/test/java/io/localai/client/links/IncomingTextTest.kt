// SPDX-License-Identifier: MIT
package io.localai.client.links

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class IncomingTextTest {
    private val view = "android.intent.action.VIEW"
    private val send = "android.intent.action.SEND"
    private val link = "localai://pair?v=1&k=device&p=12D3KooWExamplePeer&c=abc&e=1900000000&n=Kitchen"

    @Test
    fun viewLinkIsHandedOver() {
        assertEquals(link, IncomingText.fromIntent(view, link, null))
    }

    @Test
    fun viewIgnoresSharedTextExtra() {
        assertEquals(link, IncomingText.fromIntent(view, link, "something else"))
    }

    @Test
    fun viewOfAnotherPairShapeIsHandedOverForTheRendererToRefuse() {
        // The intent filter only admits localai://pair; a link without a query
        // still reaches the renderer, which reports "not an invite".
        assertEquals("localai://pair", IncomingText.fromIntent(view, "localai://pair", null))
    }

    @Test
    fun viewOfOtherUrisIsIgnored() {
        assertNull(IncomingText.fromIntent(view, "https://example.com/pair?v=1", null))
        assertNull(IncomingText.fromIntent(view, "localai://pairing?v=1", null))
        assertNull(IncomingText.fromIntent(view, null, link))
    }

    @Test
    fun sendWithProseTakesTheLinkUpToWhitespace() {
        val text = "Hi, here is the invite:\n$link\tand it works once."
        assertEquals(link, IncomingText.fromIntent(send, null, text))
    }

    @Test
    fun sendWithLinkAtTheEnd() {
        assertEquals(link, IncomingText.fromIntent(send, null, "Join me: $link"))
    }

    @Test
    fun sendWithoutInviteGivesTheTrimmedText() {
        assertEquals("hello there", IncomingText.fromIntent(send, null, "  hello there \n"))
    }

    @Test
    fun sendWithBlankTextIsIgnored() {
        assertNull(IncomingText.fromIntent(send, null, "   \n"))
        assertNull(IncomingText.fromIntent(send, null, null))
    }

    @Test
    fun otherActionsAreIgnored() {
        assertNull(IncomingText.fromIntent("android.intent.action.MAIN", null, null))
        assertNull(IncomingText.fromIntent("android.intent.action.SEND_MULTIPLE", null, link))
        assertNull(IncomingText.fromIntent(null, link, link))
    }

    @Test
    fun overlongTextIsIgnored() {
        val long = link + "&x=" + "a".repeat(5000 - link.length - 3)
        assertEquals(5000, long.length)
        assertNull(IncomingText.fromIntent(view, long, null))
        assertNull(IncomingText.fromIntent(send, null, long))
        assertNull(IncomingText.fromIntent(send, null, "b".repeat(5000)))
    }

    @Test
    fun textAtTheLimitIsKept() {
        val atLimit = "c".repeat(IncomingText.MAX_LENGTH)
        assertEquals(atLimit, IncomingText.fromIntent(send, null, atLimit))
    }
}
