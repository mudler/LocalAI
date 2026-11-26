// SPDX-License-Identifier: MIT
package io.localai.client.update

import okhttp3.OkHttpClient
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File

/** An APK signed by another certificate is refused, deleted and never installed. */
class UpdateGateTest {
    @get:Rule
    val tmp = TemporaryFolder()

    private val own = "io.localai.client"
    private val ourKey = "a".repeat(64)
    private val otherKey = "b".repeat(64)

    private lateinit var dir: File
    private lateinit var apk: File
    private val installed = mutableListOf<File>()

    @Before
    fun setUp() {
        dir = tmp.newFolder("updates")
        apk = File(dir, ApkDownloader.APK_NAME).apply { writeText("apk") }
    }

    private fun gate(candidate: Candidate?) = UpdateGate(
        downloader = ApkDownloader(OkHttpClient(), dir),
        ownId = own,
        installedSigners = { setOf(ourKey) },
        readArchive = { candidate },
        installer = { installed += it },
    )

    @Test
    fun anotherCertificateIsNeverInstalled() {
        val result = gate(Candidate(own, setOf(otherKey))).install(apk.absolutePath)
        assertEquals(UpdateGate.Outcome.Refused("signer"), result)
        assertFalse(apk.exists())
        assertEquals(emptyList<File>(), installed)
    }

    @Test
    fun verifyRefusesAnotherCertificateAndDeletesTheFile() {
        val result = gate(Candidate(own, setOf(otherKey))).verify(apk.absolutePath)
        assertEquals(UpdateGate.Outcome.Refused("signer"), result)
        assertFalse(apk.exists())
        assertEquals(emptyList<File>(), installed)
    }

    @Test
    fun anotherPackageIsNeverInstalled() {
        val result = gate(Candidate("io.example.other", setOf(ourKey))).install(apk.absolutePath)
        assertEquals(UpdateGate.Outcome.Refused("signer"), result)
        assertFalse(apk.exists())
        assertEquals(emptyList<File>(), installed)
    }

    @Test
    fun anUnreadableArchiveIsNeverInstalled() {
        val result = gate(null).install(apk.absolutePath)
        assertEquals(UpdateGate.Outcome.Refused("signer"), result)
        assertFalse(apk.exists())
        assertEquals(emptyList<File>(), installed)
    }

    @Test
    fun aFileOtherThanTheDownloadIsNeverInstalled() {
        val elsewhere = tmp.newFile("update.apk").apply { writeText("apk") }
        val result = gate(Candidate(own, setOf(ourKey))).install(elsewhere.absolutePath)
        assertEquals(UpdateGate.Outcome.Refused("file"), result)
        assertTrue(elsewhere.exists())
        assertEquals(emptyList<File>(), installed)
    }

    @Test
    fun theSameSignerIsVerifiedAndInstalledOnce() {
        val g = gate(Candidate(own, setOf(ourKey)))
        assertEquals(UpdateGate.Outcome.Done, g.verify(apk.absolutePath))
        assertEquals(UpdateGate.Outcome.Done, g.install(apk.absolutePath))
        assertEquals(listOf(apk), installed)
        assertTrue(apk.exists())
    }
}
