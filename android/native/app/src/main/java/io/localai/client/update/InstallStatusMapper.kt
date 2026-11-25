// SPDX-License-Identifier: MIT
package io.localai.client.update

/**
 * Maps a PackageInstaller session status to the web side's status names.
 * The values are the documented PackageInstaller.STATUS_* constants.
 */
object InstallStatusMapper {
    private const val STATUS_PENDING_USER_ACTION = -1
    private const val STATUS_SUCCESS = 0

    private val failureNames = mapOf(
        1 to "failure",
        2 to "blocked",
        3 to "aborted",
        4 to "invalid",
        5 to "conflict",
        6 to "storage",
        7 to "incompatible",
        8 to "timeout",
    )

    /** PackageInstaller status → "pending-user-action" | "success" | "failure", with a message for failures. */
    fun map(status: Int, message: String?): Pair<String, String?> = when (status) {
        STATUS_PENDING_USER_ACTION -> "pending-user-action" to null
        STATUS_SUCCESS -> "success" to null
        else -> "failure" to (message?.takeIf { it.isNotBlank() } ?: failureNames[status] ?: "status $status")
    }
}
