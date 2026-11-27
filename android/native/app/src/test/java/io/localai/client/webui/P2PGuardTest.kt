// SPDX-License-Identifier: MIT
package io.localai.client.webui

import io.localai.client.p2p.Forward
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class P2PGuardTest {
    private val studio = Forward("srv_1", "Studio", "peer", "127.0.0.2", 41001, emptyList(), emptyList())
    private val office = Forward("srv_2", "Office", "peer2", "127.0.0.3", 41001, emptyList(), emptyList())
    private val forwards = listOf(studio, office)

    @Test
    fun aUrlServerIsNotGuarded() {
        assertNull(P2PGuard.listening("https://ai.example.com", forwards, emptyMap()))
        assertNull(P2PGuard.listening("http://127.0.0.1:8080", forwards, emptyMap()))
        assertNull(P2PGuard.listening(null, forwards, emptyMap()))
        // Same host, another port: not this forward's address.
        assertNull(P2PGuard.listening("http://127.0.0.2:41002", forwards, mapOf("srv_1" to """{"state":"ready"}""")))
    }

    @Test
    fun aListeningForwarderAllowsTheOrigin() {
        for (line in listOf("""{"state":"ready","addr":"127.0.0.2:41001"}""", """{"state":"up","path":"direct"}""", """{"state":"down","error":"x"}""")) {
            assertEquals(line, true, P2PGuard.listening("http://127.0.0.2:41001", forwards, mapOf("srv_1" to line)))
        }
    }

    @Test
    fun anyOtherStateBlocksTheOrigin() {
        val origin = "http://127.0.0.2:41001"
        assertEquals(false, P2PGuard.listening(origin, forwards, emptyMap()))
        for (line in listOf(
            """{"state":"starting"}""",
            """{"state":"closed"}""",
            """{"state":"failed","error":"listen tcp 127.0.0.2:41001: bind: address already in use"}""",
            "not json",
        )) {
            assertEquals(line, false, P2PGuard.listening(origin, forwards, mapOf("srv_1" to line)))
        }
        // The other server's state does not count.
        assertEquals(false, P2PGuard.listening(origin, forwards, mapOf("srv_2" to """{"state":"up"}""")))
    }

    @Test
    fun findsTheForwardOfAnOrigin() {
        assertEquals("peer2", P2PGuard.forwardOf("http://127.0.0.3:41001", forwards)?.peer)
        assertNull(P2PGuard.forwardOf("http://127.0.0.3:41002", forwards))
        assertNull(P2PGuard.forwardOf(null, forwards))
    }
}
