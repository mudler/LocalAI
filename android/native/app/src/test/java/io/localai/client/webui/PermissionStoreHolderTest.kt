// SPDX-License-Identifier: MIT
package io.localai.client.webui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class PermissionStoreHolderTest {
    @Test
    fun readsTheTypeScriptStore() {
        val h = PermissionStoreHolder()
        h.set("""{"https://ai.example.com":{"media":"allow"},"http://192.0.2.10:8080":{"media":"deny"}}""")
        assertEquals("allow", h.recall("https://ai.example.com"))
        assertEquals("deny", h.recall("http://192.0.2.10:8080"))
        assertNull(h.recall("http://127.0.0.1:41873"))
        assertEquals(
            mapOf("https://ai.example.com" to mapOf("media" to "allow"), "http://192.0.2.10:8080" to mapOf("media" to "deny")),
            h.asMap(),
        )
    }

    @Test
    fun startsEmpty() {
        val h = PermissionStoreHolder()
        assertTrue(h.asMap().isEmpty())
        assertNull(h.recall("https://ai.example.com"))
    }

    @Test
    fun badJsonEmptiesTheStore() {
        val h = PermissionStoreHolder()
        h.set("""{"https://ai.example.com":{"media":"allow"}}""")
        for (bad in listOf("", "not json", "[]", "null", "42", """{"https://ai.example.com":""")) {
            h.set(bad)
            assertTrue("store after ${'"'}$bad${'"'}", h.asMap().isEmpty())
        }
    }

    @Test
    fun dropsMalformedEntriesLikeParsePermissionStore() {
        val h = PermissionStoreHolder()
        h.set(
            """{
              "https://ai.example.com": {"media": "allow", "camera": "allow"},
              "https://AI.example.com": {"media": "allow"},
              "https://ai.example.com/app": {"media": "allow"},
              "https://user@ai.example.com": {"media": "allow"},
              "javascript:alert(1)": {"media": "allow"},
              "http://192.0.2.10:8080": {"media": "maybe"},
              "http://192.0.2.11:8080": ["allow"],
              "http://192.0.2.12:8080": "allow",
              "http://127.0.0.1:41873": {"media": true}
            }""",
        )
        assertEquals(mapOf("https://ai.example.com" to mapOf("media" to "allow")), h.asMap())
    }
}
