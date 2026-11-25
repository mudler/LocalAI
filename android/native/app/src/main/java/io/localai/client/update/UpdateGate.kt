// SPDX-License-Identifier: MIT
package io.localai.client.update

import java.io.File

/**
 * The checks between a downloaded file and the system installer, kept apart from
 * Android so they run in JVM tests. Every install goes through [install], which
 * repeats the signer check, so a file signed by someone else is deleted and never
 * reaches [installer].
 */
class UpdateGate(
    private val downloader: ApkDownloader,
    private val ownId: String,
    private val installedSigners: () -> Set<String>,
    private val readArchive: (File) -> Candidate?,
    private val installer: (File) -> Unit,
) {
    sealed class Outcome {
        object Done : Outcome()
        /** [code] is "file" (not the downloaded update) or "signer" (refused and deleted). */
        data class Refused(val code: String) : Outcome()
    }

    /** Done when [path] is the downloaded update and it is this app signed by the same key; a refused signer deletes the file. */
    fun verify(path: String): Outcome {
        val file = downloader.downloaded(path) ?: return Outcome.Refused("file")
        return if (acceptable(file)) Outcome.Done else Outcome.Refused("signer")
    }

    fun install(path: String): Outcome {
        val file = downloader.downloaded(path) ?: return Outcome.Refused("file")
        if (!acceptable(file)) return Outcome.Refused("signer")
        installer(file)
        return Outcome.Done
    }

    private fun acceptable(file: File): Boolean =
        SignerCheck.keepIfAcceptable(file, ownId, installedSigners(), readArchive(file))
}
