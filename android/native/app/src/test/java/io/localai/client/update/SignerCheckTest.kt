// SPDX-License-Identifier: MIT
package io.localai.client.update

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

class SignerCheckTest {
    @get:Rule
    val tmp = TemporaryFolder()

    private val a = "a".repeat(64)
    private val b = "b".repeat(64)

    @Test
    fun equalSetsAreTheSameSigner() {
        assertTrue(SignerCheck.sameSigner(setOf(a), setOf(a)))
        assertTrue(SignerCheck.sameSigner(setOf(a, b), setOf(b, a)))
    }

    @Test
    fun differentSetsAreNot() {
        assertFalse(SignerCheck.sameSigner(setOf(a), setOf(b)))
        assertFalse(SignerCheck.sameSigner(setOf(a), setOf(a, b)))
    }

    @Test
    fun emptySetsAreNeverTheSameSigner() {
        assertFalse(SignerCheck.sameSigner(emptySet(), emptySet()))
        assertFalse(SignerCheck.sameSigner(setOf(a), emptySet()))
        assertFalse(SignerCheck.sameSigner(emptySet(), setOf(a)))
    }

    @Test
    fun fingerprintIsLowercaseSha256Hex() {
        // SHA-256 of the three bytes "abc".
        assertEquals(
            "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
            SignerCheck.fingerprint("abc".toByteArray()),
        )
    }

    @Test
    fun anotherCertificateIsRefusedAndTheFileIsDeleted() {
        val apk = tmp.newFile("update.apk").apply { writeText("apk") }
        val kept = SignerCheck.keepIfAcceptable(apk, "io.localai.client", setOf(a), Candidate("io.localai.client", setOf(b)))
        assertFalse(kept)
        assertFalse(apk.exists())
    }

    @Test
    fun anotherPackageIsRefusedAndTheFileIsDeleted() {
        val apk = tmp.newFile("update.apk").apply { writeText("apk") }
        val kept = SignerCheck.keepIfAcceptable(apk, "io.localai.client", setOf(a), Candidate("io.example.other", setOf(a)))
        assertFalse(kept)
        assertFalse(apk.exists())
    }

    @Test
    fun anUnreadableArchiveIsRefusedAndTheFileIsDeleted() {
        val apk = tmp.newFile("update.apk").apply { writeText("not an apk") }
        assertFalse(SignerCheck.keepIfAcceptable(apk, "io.localai.client", setOf(a), null))
        assertFalse(apk.exists())
    }

    @Test
    fun theSameSignerAndPackageKeepTheFile() {
        val apk = tmp.newFile("update.apk").apply { writeText("apk") }
        assertTrue(SignerCheck.keepIfAcceptable(apk, "io.localai.client", setOf(a), Candidate("io.localai.client", setOf(a))))
        assertTrue(apk.exists())
    }
}
