// SPDX-License-Identifier: MIT
package io.localai.client.webui

import org.junit.Assert.assertEquals
import org.junit.Assert.fail
import org.junit.Test

class ProfilesTest {
    @Test
    fun acceptsTheNamesTheClientCoreMakes() {
        for (name in listOf("webui-0123456789abcdef01234567", "webui-ffffffffffffffffffffffff", "webui-000000000000000000000000")) {
            assertEquals(name, Profiles.validate(name))
        }
    }

    @Test
    fun rejectsEverythingElse() {
        val bad = listOf(
            "",
            "Default",
            "webui-",
            "webui-0123456789abcdef0123456",
            "webui-0123456789abcdef012345678",
            "webui-0123456789ABCDEF01234567",
            "WEBUI-0123456789abcdef01234567",
            "webui-0123456789abcdef0123456g",
            "webui-0123456789abcdef01234567\n",
            " webui-0123456789abcdef01234567",
            "../webui-0123456789abcdef01234567",
            "webui-0123456789abcdef01234567/x",
        )
        for (name in bad) {
            try {
                Profiles.validate(name)
                fail("accepted ${'"'}$name${'"'}")
            } catch (_: IllegalArgumentException) {
                // expected
            }
        }
    }
}
