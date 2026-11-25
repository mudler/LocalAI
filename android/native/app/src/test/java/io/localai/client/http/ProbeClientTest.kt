// SPDX-License-Identifier: MIT
package io.localai.client.http

import java.net.InetAddress
import java.net.ServerSocket
import java.security.cert.CertificateExpiredException
import java.security.cert.CertificateNotYetValidException
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLHandshakeException
import javax.net.ssl.SSLPeerUnverifiedException
import okhttp3.OkHttpClient
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.SocketPolicy
import okhttp3.tls.HandshakeCertificates
import okhttp3.tls.HeldCertificate
import okio.Buffer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Before
import org.junit.Test

class ProbeClientTest {
    private lateinit var server: MockWebServer
    private val day = TimeUnit.DAYS.toMillis(1)

    @Before
    fun setUp() {
        server = MockWebServer()
    }

    @After
    fun tearDown() {
        server.shutdown()
    }

    private val ca: HeldCertificate by lazy {
        HeldCertificate.Builder().certificateAuthority(0).commonName("Test CA").build()
    }

    private fun trustingCa(): OkHttpClient {
        val hc = HandshakeCertificates.Builder().addTrustedCertificate(ca.certificate).build()
        return OkHttpClient.Builder().sslSocketFactory(hc.sslSocketFactory(), hc.trustManager).build()
    }

    private fun serveHttps(leaf: HeldCertificate, vararg chain: java.security.cert.X509Certificate) {
        val hc = HandshakeCertificates.Builder().heldCertificate(leaf, *chain).build()
        server.useHttps(hc.sslSocketFactory(), false)
        server.enqueue(MockResponse().setBody("ok"))
        server.start(InetAddress.getByName("127.0.0.1"), 0)
    }

    // The certificates name "localhost"; the server listens on 127.0.0.1.
    private fun httpsUrl() = "https://localhost:${server.port}/"

    private fun leaf(host: String = "localhost") = HeldCertificate.Builder().addSubjectAlternativeName(host)

    @Test
    fun answersWithStatusTypeAndBody() {
        server.enqueue(MockResponse().setHeader("Content-Type", "application/json").setBody("{\"version\":\"v4.3.0\"}"))
        server.start()
        val r = ProbeClient(OkHttpClient()).get("a", server.url("/version").toString(), 5000)
        assertEquals(ProbeResult.Answer(200, null, "application/json", "{\"version\":\"v4.3.0\"}"), r)
        val req = server.takeRequest()
        assertEquals("application/json, text/html;q=0.5", req.getHeader("Accept"))
        assertEquals("localai-android", req.getHeader("User-Agent"))
        assertNull(req.getHeader("Authorization"))
    }

    @Test
    fun doesNotFollowRedirects() {
        server.enqueue(MockResponse().setResponseCode(302).setHeader("Location", "/login"))
        server.enqueue(MockResponse().setBody("login page"))
        server.start()
        val r = ProbeClient(OkHttpClient()).get("a", server.url("/").toString(), 5000)
        assertEquals(ProbeResult.Answer(302, "/login", "", ""), r)
        assertEquals(1, server.requestCount)
    }

    @Test
    fun truncatesTheBodyAt64KiB() {
        server.enqueue(MockResponse().setBody(Buffer().writeUtf8("x".repeat(70 * 1024))))
        server.start()
        val r = ProbeClient(OkHttpClient()).get("a", server.url("/").toString(), 5000) as ProbeResult.Answer
        assertEquals(65_536, r.body.length)
    }

    @Test
    fun sendsNoCookieBack() {
        server.enqueue(MockResponse().setHeader("Set-Cookie", "session=abc; Path=/").setBody("one"))
        server.enqueue(MockResponse().setBody("two"))
        server.start()
        val p = ProbeClient(OkHttpClient())
        p.get("a", server.url("/").toString(), 5000)
        p.get("b", server.url("/").toString(), 5000)
        server.takeRequest()
        assertNull(server.takeRequest().getHeader("Cookie"))
    }

    @Test
    fun refusedPort() {
        val port = ServerSocket(0).use { it.localPort }
        val r = ProbeClient(OkHttpClient()).get("a", "http://127.0.0.1:$port/", 5000)
        assertEquals(ProbeResult.Unreachable("refused"), r)
    }

    @Test
    fun unknownHost() {
        val r = ProbeClient(OkHttpClient()).get("a", "http://nohost.invalid/", 5000)
        assertEquals(ProbeResult.Unreachable("dns"), r)
    }

    @Test
    fun serverThatNeverAnswers() {
        server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.NO_RESPONSE))
        server.start()
        val r = ProbeClient(OkHttpClient()).get("a", server.url("/").toString(), 300)
        assertEquals(ProbeResult.Unreachable("timeout"), r)
    }

    @Test
    fun aUrlOkHttpCannotUseIsANetworkFailure() {
        assertEquals(ProbeResult.Unreachable("network"), ProbeClient(OkHttpClient()).get("a", "ftp://example.com/", 300))
    }

    @Test
    fun selfSignedLeafWithTheDefaultTrust() {
        serveHttps(leaf().build())
        val r = ProbeClient(OkHttpClient()).get("a", httpsUrl(), 5000)
        assertEquals(ProbeResult.Tls("self-signed"), r)
    }

    @Test
    fun leafFromAnUnknownCa() {
        serveHttps(leaf().signedBy(ca).build(), ca.certificate)
        val r = ProbeClient(OkHttpClient()).get("a", httpsUrl(), 5000)
        assertEquals(ProbeResult.Tls("untrusted"), r)
    }

    @Test
    fun trustedCaAnswers() {
        serveHttps(leaf().signedBy(ca).build(), ca.certificate)
        val r = ProbeClient(trustingCa()).get("a", httpsUrl(), 5000)
        assertEquals(ProbeResult.Answer(200, null, "", "ok"), r)
    }

    @Test
    fun expiredCertificateFromATrustedCa() {
        val now = System.currentTimeMillis()
        serveHttps(leaf().validityInterval(now - 3 * day, now - day).signedBy(ca).build(), ca.certificate)
        val r = ProbeClient(trustingCa()).get("a", httpsUrl(), 5000)
        assertEquals(ProbeResult.Tls("expired"), r)
    }

    @Test
    fun certificateNotYetValidFromATrustedCa() {
        val now = System.currentTimeMillis()
        serveHttps(leaf().validityInterval(now + day, now + 3 * day).signedBy(ca).build(), ca.certificate)
        val r = ProbeClient(trustingCa()).get("a", httpsUrl(), 5000)
        assertEquals(ProbeResult.Tls("not-yet-valid"), r)
    }

    @Test
    fun wrongHostName() {
        serveHttps(leaf("other.example.com").signedBy(ca).build(), ca.certificate)
        val r = ProbeClient(trustingCa()).get("a", httpsUrl(), 5000)
        assertEquals(ProbeResult.Tls("name-mismatch"), r)
    }

    @Test
    fun cancelDuringASlowAnswer() {
        server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.NO_RESPONSE))
        server.start()
        val p = ProbeClient(OkHttpClient())
        val pool = Executors.newSingleThreadExecutor()
        try {
            val f = pool.submit<ProbeResult> { p.get("slow", server.url("/").toString(), 8000) }
            server.takeRequest(5, TimeUnit.SECONDS)
            p.cancel("slow")
            assertEquals(ProbeResult.Cancelled, f.get(5, TimeUnit.SECONDS))
        } finally {
            pool.shutdownNow()
        }
    }

    @Test
    fun cancelBeforeTheCallStarts() {
        val p = ProbeClient(OkHttpClient())
        p.cancel("early")
        assertEquals(ProbeResult.Cancelled, p.get("early", "http://127.0.0.1:1/", 5000))
    }

    @Test
    fun cancelOfAnotherIdDoesNotTouchACall() {
        server.enqueue(MockResponse().setBody("ok"))
        server.start()
        val p = ProbeClient(OkHttpClient())
        p.cancel("other")
        assertEquals(ProbeResult.Answer(200, null, "", "ok"), p.get("mine", server.url("/").toString(), 5000))
    }

    @Test
    fun classifiesCauseChains() {
        fun tls(e: Throwable) = classifyIoError(e)
        assertEquals(ProbeResult.Tls("name-mismatch"), tls(SSLPeerUnverifiedException("Hostname x not verified")))
        assertEquals(ProbeResult.Tls("expired"), tls(SSLHandshakeException("x").apply { initCause(CertificateExpiredException()) }))
        assertEquals(ProbeResult.Tls("not-yet-valid"), tls(SSLHandshakeException("x").apply { initCause(CertificateNotYetValidException()) }))
        assertEquals(ProbeResult.Tls("untrusted"), tls(SSLHandshakeException("x")))
        assertEquals(ProbeResult.Unreachable("dns"), classifyIoError(java.net.UnknownHostException("x")))
        assertEquals(ProbeResult.Unreachable("refused"), classifyIoError(java.net.ConnectException("x")))
        assertEquals(ProbeResult.Unreachable("timeout"), classifyIoError(java.net.SocketTimeoutException("x")))
        assertEquals(ProbeResult.Unreachable("timeout"), classifyIoError(java.io.InterruptedIOException("timeout")))
        assertEquals(ProbeResult.Unreachable("network"), classifyIoError(java.io.IOException("reset")))
    }
}
