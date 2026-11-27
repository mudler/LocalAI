// SPDX-License-Identifier: MIT
package io.localai.client.webui

import io.localai.client.webui.DeviceSignIn.Action
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class DeviceSignInTest {
    private val origin = "http://127.0.0.2:41001"
    private val login = "$origin/login"
    private val home = "$origin/app"
    private val session = origin + DeviceSignIn.SESSION_PATH

    @Test
    fun findsTheSignInPage() {
        assertTrue(DeviceSignIn.isSignInPath("/login"))
        assertTrue(DeviceSignIn.isSignInPath("/login/"))
        assertFalse(DeviceSignIn.isSignInPath("/"))
        assertFalse(DeviceSignIn.isSignInPath("/app"))
        assertFalse(DeviceSignIn.isSignInPath("/api/auth/oidc/login"))
        assertFalse(DeviceSignIn.isSignInPath("/loginx"))
        assertEquals("/login", DeviceSignIn.pathOf("$login?next=%2Fapp#x"))
        assertEquals("/", DeviceSignIn.pathOf(origin))
    }

    @Test
    fun onlyAFreshPairingWithACredentialPostsOnOpen() {
        val s = DeviceSignIn()
        assertTrue(s.onOpen(fresh = true, hasCredential = true, nowMs = 0))
        assertFalse(DeviceSignIn().onOpen(fresh = false, hasCredential = true, nowMs = 0))
        assertFalse(DeviceSignIn().onOpen(fresh = true, hasCredential = false, nowMs = 0))
    }

    @Test
    fun aLandingOnTheSignInPagePostsOnce() {
        val s = DeviceSignIn()
        assertEquals(Action.POST, s.onUrl(login, hasCredential = true, nowMs = 1_000))
        // The post redirected to "/" and the page stays signed in.
        assertEquals(Action.NONE, s.onUrl("$origin/", hasCredential = true, nowMs = 1_100))
        assertEquals(Action.NONE, s.onUrl(home, hasCredential = true, nowMs = 1_200))
        // Much later the session ends: a new landing, a new post.
        assertEquals(Action.POST, s.onUrl(login, hasCredential = true, nowMs = 1_000 + DeviceSignIn.REFUSAL_WINDOW_MS + 1))
    }

    @Test
    fun aServerWithoutACredentialIsLeftAlone() {
        val s = DeviceSignIn()
        assertEquals(Action.NONE, s.onUrl(login, hasCredential = false, nowMs = 0))
        assertEquals(Action.NONE, s.onUrl(home, hasCredential = false, nowMs = 1))
    }

    @Test
    fun aRefusedPostStopsTheAttempts() {
        val s = DeviceSignIn()
        assertEquals(Action.POST, s.onUrl(login, hasCredential = true, nowMs = 0))
        // The server answered 4xx: the view shows the session route.
        assertEquals(Action.LEAVE, s.onUrl(session, hasCredential = true, nowMs = 100))
        assertTrue(s.stopped)
        // The sign-in page the app loads instead, and every later landing: no post.
        assertEquals(Action.NONE, s.onUrl(login, hasCredential = true, nowMs = 200))
        assertEquals(Action.NONE, s.onUrl(login, hasCredential = true, nowMs = 10 * DeviceSignIn.REFUSAL_WINDOW_MS))
        assertFalse(s.onOpen(fresh = true, hasCredential = true, nowMs = 0))
    }

    @Test
    fun aCookieThatDoesNotStickCannotLoop() {
        val s = DeviceSignIn()
        assertEquals(Action.POST, s.onUrl(login, hasCredential = true, nowMs = 0))
        assertEquals(Action.NONE, s.onUrl("$origin/", hasCredential = true, nowMs = 100))
        // The page sends the person to the sign-in page again right away.
        assertEquals(Action.NONE, s.onUrl(login, hasCredential = true, nowMs = 300))
        assertTrue(s.stopped)
        assertEquals(Action.NONE, s.onUrl(login, hasCredential = true, nowMs = 1_000_000))
    }

    @Test
    fun theFirstOpenPostCountsForTheWindow() {
        val s = DeviceSignIn()
        assertTrue(s.onOpen(fresh = true, hasCredential = true, nowMs = 0))
        assertEquals(Action.NONE, s.onUrl(login, hasCredential = true, nowMs = 500))
        assertTrue(s.stopped)
    }

    @Test
    fun resetAllowsANewAttempt() {
        val s = DeviceSignIn()
        s.onUrl(login, hasCredential = true, nowMs = 0)
        s.onUrl(session, hasCredential = true, nowMs = 1)
        s.reset()
        assertFalse(s.stopped)
        assertEquals(Action.POST, s.onUrl(login, hasCredential = true, nowMs = 2))
    }

    @Test
    fun theBodyIsAFormField() {
        assertEquals("credential=abc-_DEF", String(DeviceSignIn.body("abc-_DEF"), Charsets.UTF_8))
        assertEquals("credential=a%2Bb%3D", String(DeviceSignIn.body("a+b="), Charsets.UTF_8))
    }
}
