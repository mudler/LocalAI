// SPDX-License-Identifier: MIT
package io.localai.client.p2p

import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class PairAnswerTest {
    @Test
    fun okAnswerGivesTheResultObject() {
        val a = parsePairAnswer("""{"ok":true,"result":{"phrase":"apple banana","server":{"peer_id":"p"}}}""")
        assertTrue(a.ok)
        assertEquals("apple banana", JSONObject(a.json).getString("phrase"))
    }

    @Test
    fun failedAnswerKeepsTheCodeAndTheDetail() {
        val a = parsePairAnswer("""{"ok":false,"code":"needs_relay","detail":"cannot connect directly"}""")
        assertFalse(a.ok)
        assertEquals("needs_relay", a.code)
        assertEquals("cannot connect directly", a.detail)
    }

    @Test
    fun unexpectedAnswersAreFailures() {
        for (s in listOf("not json", """{"ok":true}""", """{"ok":false}""")) {
            val a = parsePairAnswer(s)
            assertFalse(s, a.ok)
            assertEquals(s, PAIR_FAILED, a.code)
            assertTrue(s, a.detail.isNotEmpty())
        }
    }

    @Test
    fun forwardsCarryTheirHints() {
        val arr = JSONArray(
            """[{"id":"a","name":"Home","peer":"p","host":"127.0.0.2","port":41001,"addrs":[],"relays":[],"hints":["/ip4/192.0.2.7/tcp/4001/p2p/12D3KooWHint"]},
               {"id":"b","name":"Office","peer":"q","host":"127.0.0.3","port":41002,"addrs":[],"relays":[]}]""",
        )
        val out = parseForwards(arr)
        assertEquals(listOf("/ip4/192.0.2.7/tcp/4001/p2p/12D3KooWHint"), out[0].hints)
        assertEquals(emptyList<String>(), out[1].hints)
    }
}
