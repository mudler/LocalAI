// SPDX-License-Identifier: MIT
package io.localai.client.webui

import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Before
import org.junit.Test
import java.net.InetAddress
import java.util.concurrent.TimeUnit

/** The sign-in cookie of a download never follows a redirect to another host. */
class DownloadResolverTest {
    private val server = MockWebServer()
    private val other = MockWebServer()
    private val resolver = DownloadResolver()
    private lateinit var origin: String

    @Before
    fun setUp() {
        // Two loopback hosts: two origins.
        server.start(InetAddress.getByName("127.0.0.1"), 0)
        other.start(InetAddress.getByName("127.0.0.2"), 0)
        origin = WebuiPolicy.httpOrigin(server.url("/").toString())!!
    }

    @After
    fun tearDown() {
        server.shutdown()
        other.shutdown()
    }

    private fun redirect(to: String) = MockResponse().setResponseCode(302).setHeader("Location", to)

    @Test
    fun aDownloadOnTheOriginKeepsTheCookie() {
        server.enqueue(MockResponse().setBody("file"))
        val url = server.url("/files/a.txt").toString()
        assertEquals(DownloadResolver.Target(url, "session=s1"), resolver.resolve(url, origin, "session=s1", "UA"))
        val req = server.takeRequest(1, TimeUnit.SECONDS)!!
        assertEquals("session=s1", req.getHeader("Cookie"))
        assertEquals("UA", req.getHeader("User-Agent"))
    }

    @Test
    fun aRedirectOnTheOriginIsFollowedWithTheCookie() {
        server.enqueue(redirect("/files/b.txt"))
        server.enqueue(MockResponse().setBody("file"))
        val t = resolver.resolve(server.url("/files/a.txt").toString(), origin, "session=s1", null)
        assertEquals(DownloadResolver.Target(server.url("/files/b.txt").toString(), "session=s1"), t)
        assertEquals("session=s1", server.takeRequest(1, TimeUnit.SECONDS)!!.getHeader("Cookie"))
        assertEquals("session=s1", server.takeRequest(1, TimeUnit.SECONDS)!!.getHeader("Cookie"))
    }

    @Test
    fun aRedirectToAnotherHostDropsTheCookieAndIsNotRequested() {
        val elsewhere = other.url("/bucket/a.txt").toString()
        server.enqueue(redirect(elsewhere))
        val t = resolver.resolve(server.url("/files/a.txt").toString(), origin, "session=s1", null)
        assertEquals(DownloadResolver.Target(elsewhere, null), t)
        assertEquals(0, other.requestCount)
    }

    @Test
    fun withoutACookieNothingIsRequested() {
        val url = server.url("/files/a.txt").toString()
        assertEquals(DownloadResolver.Target(url, null), resolver.resolve(url, origin, null, null))
        assertEquals(0, server.requestCount)
    }

    @Test
    fun tooManyRedirectsDropTheCookie() {
        repeat(6) { server.enqueue(redirect("/loop$it")) }
        assertNull(resolver.resolve(server.url("/files/a.txt").toString(), origin, "session=s1", null).cookie)
    }

    @Test
    fun aServerThatDoesNotAnswerDropsTheCookie() {
        val url = server.url("/files/a.txt").toString()
        server.shutdown()
        assertEquals(DownloadResolver.Target(url, null), resolver.resolve(url, origin, "session=s1", null))
    }
}
