// SPDX-License-Identifier: MIT
package io.localai.client.update

import java.io.File
import java.security.MessageDigest

/** What a downloaded archive says about itself: its package name and signing certificates (SHA-256 hex). */
data class Candidate(val packageName: String, val signers: Set<String>)

/**
 * Android refuses an update signed by another key at install time too, but only
 * with a system error; checking first lets the app say why, and delete the file
 * before anything reaches the installer.
 */
object SignerCheck {
    /** Sets of SHA-256 hex of signing certificates; true only when equal and non-empty. */
    fun sameSigner(installed: Set<String>, candidate: Set<String>): Boolean =
        installed.isNotEmpty() && installed == candidate

    fun fingerprint(certificate: ByteArray): String =
        ApkDownloader.hex(MessageDigest.getInstance("SHA-256").digest(certificate))

    /**
     * True when [candidate] is this app ([ownId]) signed by the same certificates.
     * Otherwise, including an archive Android could not read ([candidate] null),
     * deletes [file] so it can never be installed later.
     */
    fun keepIfAcceptable(file: File, ownId: String, installed: Set<String>, candidate: Candidate?): Boolean {
        val ok = candidate != null &&
            candidate.packageName == ownId &&
            sameSigner(installed, candidate.signers)
        if (!ok) file.delete()
        return ok
    }
}
