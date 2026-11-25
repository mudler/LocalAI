// SPDX-License-Identifier: MIT
package io.localai.client.update

import org.junit.Assert.assertEquals
import org.junit.Test

/** Uses the documented values of the PackageInstaller.STATUS_* constants. */
class InstallStatusMapperTest {
    @Test
    fun pendingUserAction() {
        assertEquals("pending-user-action" to null, InstallStatusMapper.map(-1, "ignored"))
    }

    @Test
    fun success() {
        assertEquals("success" to null, InstallStatusMapper.map(0, null))
    }

    @Test
    fun everyFailureKeepsTheSystemMessage() {
        // FAILURE, BLOCKED, ABORTED, INVALID, CONFLICT, STORAGE, INCOMPATIBLE, FAILURE_TIMEOUT
        for (status in 1..8) {
            assertEquals("failure" to "system says $status", InstallStatusMapper.map(status, "system says $status"))
        }
    }

    @Test
    fun failureWithoutAMessageGetsTheStatusName() {
        val expected = mapOf(
            1 to "failure",
            2 to "blocked",
            3 to "aborted",
            4 to "invalid",
            5 to "conflict",
            6 to "storage",
            7 to "incompatible",
            8 to "timeout",
        )
        for ((status, name) in expected) {
            assertEquals("failure" to name, InstallStatusMapper.map(status, null))
            assertEquals("failure" to name, InstallStatusMapper.map(status, "  "))
        }
    }

    @Test
    fun unknownStatusIsAFailure() {
        assertEquals("failure" to "status 42", InstallStatusMapper.map(42, null))
        assertEquals("failure" to "status -7", InstallStatusMapper.map(-7, null))
    }
}
