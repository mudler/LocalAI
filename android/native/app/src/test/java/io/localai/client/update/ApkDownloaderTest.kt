// SPDX-License-Identifier: MIT
package io.localai.client.update

import okhttp3.OkHttpClient
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.tls.HandshakeCertificates
import okhttp3.tls.HeldCertificate
import okio.Buffer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File
import java.net.InetAddress
import java.security.MessageDigest

class ApkDownloaderTest {
    @get:Rule
    val tmp = TemporaryFolder()

    private lateinit var tls: MockWebServer
    private lateinit var plain: MockWebServer
    private lateinit var client: OkHttpClient
    private lateinit var dir: File

    private val body = ByteArray(200_000) { (it % 251).toByte() }
    private val bodySha = sha256(body)

    @Before
    fun setUp() {
        val localhost = InetAddress.getByName("localhost").canonicalHostName
        val cert = HeldCertificate.Builder().addSubjectAlternativeName(localhost).build()
        val server = HandshakeCertificates.Builder().heldCertificate(cert).build()
        val trust = HandshakeCertificates.Builder().addTrustedCertificate(cert.certificate).build()
        tls = MockWebServer().apply {
            useHttps(server.sslSocketFactory(), false)
            start()
        }
        plain = MockWebServer().apply { start() }
        client = OkHttpClient.Builder()
            .sslSocketFactory(trust.sslSocketFactory(), trust.trustManager)
            .build()
        dir = File(tmp.root, "updates")
    }

    @After
    fun tearDown() {
        tls.shutdown()
        plain.shutdown()
    }

    private fun downloader() = ApkDownloader(client, dir)

    private fun leftovers(): List<String> = dir.listFiles()?.map { it.name }.orEmpty()

    private fun refusedReason(block: () -> Unit): String {
        try {
            block()
        } catch (e: DownloadRefused) {
            return e.reason
        }
        fail("expected DownloadRefused")
        throw AssertionError()
    }

    @Test
    fun goodFileIsRenamedAndProgressReachesSize() {
        tls.enqueue(MockResponse().setBody(Buffer().write(body)))
        var last = 0L
        var total = 0L
        val file = downloader().download(tls.url("/a.apk").toString(), bodySha, body.size.toLong()) { done, t ->
            assertTrue(done >= last)
            last = done
            total = t
        }
        assertEquals(File(dir, "update.apk"), file)
        assertTrue(file.readBytes().contentEquals(body))
        assertEquals(body.size.toLong(), last)
        assertEquals(body.size.toLong(), total)
        assertEquals(listOf("update.apk"), leftovers())
    }

    @Test
    fun wrongSizeIsRefusedAndNoFileIsLeft() {
        tls.enqueue(MockResponse().setBody(Buffer().write(body)))
        val reason = refusedReason {
            downloader().download(tls.url("/a.apk").toString(), bodySha, body.size.toLong() + 1) { _, _ -> }
        }
        assertEquals("size", reason)
        assertEquals(emptyList<String>(), leftovers())
    }

    @Test
    fun longerBodyThanAnnouncedIsRefused() {
        // No Content-Length: the size limit must hold while streaming.
        tls.enqueue(MockResponse().setChunkedBody(Buffer().write(body), 4096))
        val reason = refusedReason {
            downloader().download(tls.url("/a.apk").toString(), bodySha, 1000) { _, _ -> }
        }
        assertEquals("size", reason)
        assertEquals(emptyList<String>(), leftovers())
    }

    @Test
    fun wrongHashIsRefusedAndNoFileIsLeft() {
        tls.enqueue(MockResponse().setBody(Buffer().write(body)))
        val reason = refusedReason {
            downloader().download(tls.url("/a.apk").toString(), "0".repeat(64), body.size.toLong()) { _, _ -> }
        }
        assertEquals("sha256", reason)
        assertEquals(emptyList<String>(), leftovers())
    }

    @Test
    fun malformedHashIsRefusedBeforeDownloading() {
        val reason = refusedReason {
            downloader().download(tls.url("/a.apk").toString(), bodySha.uppercase(), body.size.toLong()) { _, _ -> }
        }
        assertEquals("sha256", reason)
        assertEquals(0, tls.requestCount)
    }

    @Test
    fun notFoundIsRefusedWithTheStatus() {
        tls.enqueue(MockResponse().setResponseCode(404))
        val reason = refusedReason {
            downloader().download(tls.url("/a.apk").toString(), bodySha, body.size.toLong()) { _, _ -> }
        }
        assertEquals("http-404", reason)
        assertEquals(emptyList<String>(), leftovers())
    }

    @Test
    fun redirectFromHttpsToHttpIsRefused() {
        plain.enqueue(MockResponse().setBody(Buffer().write(body)))
        tls.enqueue(MockResponse().setResponseCode(302).setHeader("Location", plain.url("/a.apk").toString()))
        val reason = refusedReason {
            downloader().download(tls.url("/a.apk").toString(), bodySha, body.size.toLong()) { _, _ -> }
        }
        assertEquals("scheme", reason)
        assertEquals(0, plain.requestCount)
        assertEquals(emptyList<String>(), leftovers())
    }

    @Test
    fun redirectToHttpsIsFollowed() {
        tls.enqueue(MockResponse().setResponseCode(302).setHeader("Location", "/asset.apk"))
        tls.enqueue(MockResponse().setBody(Buffer().write(body)))
        val file = downloader().download(tls.url("/a.apk").toString(), bodySha, body.size.toLong()) { _, _ -> }
        assertTrue(file.isFile)
        assertEquals("/a.apk", tls.takeRequest().path)
        assertEquals("/asset.apk", tls.takeRequest().path)
    }

    @Test
    fun plainHttpUrlIsRefusedWhenTheFeedIsHttps() {
        val reason = refusedReason {
            downloader().download(plain.url("/a.apk").toString(), bodySha, body.size.toLong()) { _, _ -> }
        }
        assertEquals("scheme", reason)
        assertEquals(0, plain.requestCount)
    }

    @Test
    fun plainHttpIsAllowedForAnHttpTestFeed() {
        plain.enqueue(MockResponse().setBody(Buffer().write(body)))
        val file = ApkDownloader(client, dir, allowHttp = true)
            .download(plain.url("/a.apk").toString(), bodySha, body.size.toLong()) { _, _ -> }
        assertTrue(file.isFile)
    }

    @Test
    fun theFolderIsEmptiedBeforeANewDownload() {
        dir.mkdirs()
        File(dir, "update.apk").writeText("old")
        File(dir, "stale.apk.part").writeText("old")
        tls.enqueue(MockResponse().setResponseCode(500))
        refusedReason {
            downloader().download(tls.url("/a.apk").toString(), bodySha, body.size.toLong()) { _, _ -> }
        }
        assertEquals(emptyList<String>(), leftovers())
    }

    @Test
    fun fetchTextReturnsStatusAndBody() {
        tls.enqueue(MockResponse().setBody("{\"schema\":1}"))
        val (status, text) = downloader().fetchText(tls.url("/m.json").toString(), 1024)
        assertEquals(200, status)
        assertEquals("{\"schema\":1}", text)
    }

    @Test
    fun fetchTextReturnsTheStatusOfAMissingFeed() {
        tls.enqueue(MockResponse().setResponseCode(404).setBody("Not Found"))
        val (status, text) = downloader().fetchText(tls.url("/m.json").toString(), 1024)
        assertEquals(404, status)
        assertEquals("", text)
    }

    @Test
    fun fetchTextRefusesABodyOverTheLimit() {
        tls.enqueue(MockResponse().setBody("x".repeat(2048)))
        val reason = refusedReason { downloader().fetchText(tls.url("/m.json").toString(), 1024) }
        assertEquals("size", reason)
    }

    @Test
    fun fetchTextRefusesARedirectToHttp() {
        tls.enqueue(MockResponse().setResponseCode(302).setHeader("Location", plain.url("/m.json").toString()))
        val reason = refusedReason { downloader().fetchText(tls.url("/m.json").toString(), 1024) }
        assertEquals("scheme", reason)
        assertEquals(0, plain.requestCount)
    }

    @Test
    fun downloadedResolvesOnlyTheUpdateFile() {
        tls.enqueue(MockResponse().setBody(Buffer().write(body)))
        val d = downloader()
        val file = d.download(tls.url("/a.apk").toString(), bodySha, body.size.toLong()) { _, _ -> }
        assertNotNull(d.downloaded(file.absolutePath))
        assertNotNull(d.downloaded(File(dir, "../updates/update.apk").path))
        assertNull(d.downloaded(File(dir, "update.apk.part").path))
        val outside = tmp.newFile("update.apk")
        assertNull(d.downloaded(outside.absolutePath))
        file.delete()
        assertNull(d.downloaded(file.absolutePath))
        assertFalse(file.exists())
    }

    @Test
    fun onlyUrlsUnderTheFeedBaseAreAccepted() {
        val base = "https://github.com/mudler/LocalAI/releases"
        assertTrue(ApkDownloader.underFeed("$base/latest/download/android-update.json", base))
        assertTrue(ApkDownloader.underFeed("$base/download/dev-master/LocalAI-Android-dev.apk", base))
        assertFalse(ApkDownloader.underFeed("$base/../../other/x.apk", base))
        assertFalse(ApkDownloader.underFeed("https://github.com/mudler/LocalAI/releases-evil/x.apk", base))
        assertFalse(ApkDownloader.underFeed("https://example.com/mudler/LocalAI/releases/x.apk", base))
        assertFalse(ApkDownloader.underFeed("http://github.com/mudler/LocalAI/releases/x.apk", base))
        assertFalse(ApkDownloader.underFeed("https://github.com:8443/mudler/LocalAI/releases/x.apk", base))
        assertFalse(ApkDownloader.underFeed("not a url", base))
    }

    private fun sha256(b: ByteArray): String =
        MessageDigest.getInstance("SHA-256").digest(b).joinToString("") { "%02x".format(it) }
}
