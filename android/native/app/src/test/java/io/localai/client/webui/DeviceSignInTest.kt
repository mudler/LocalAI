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
    fun findsTheSignOutRequest() {
        val logout = origin + DeviceSignIn.SIGN_OUT_PATH
        assertTrue(DeviceSignIn.isSignOutRequest("POST", logout, origin))
        assertTrue(DeviceSignIn.isSignOutRequest("post", "$logout/", origin))
        // A GET is not the sign-out (the route takes a post only).
        assertFalse(DeviceSignIn.isSignOutRequest("GET", logout, origin))
        assertFalse(DeviceSignIn.isSignOutRequest(null, logout, origin))
        // Another origin, or another route.
        assertFalse(DeviceSignIn.isSignOutRequest("POST", "http://127.0.0.3:41001" + DeviceSignIn.SIGN_OUT_PATH, origin))
        assertFalse(DeviceSignIn.isSignOutRequest("POST", "$origin/api/auth/logout-all", origin))
        assertFalse(DeviceSignIn.isSignOutRequest("POST", "$origin/api/auth/login", origin))
        assertFalse(DeviceSignIn.isSignOutRequest("POST", session, origin))
    }

    @Test
    fun aSignedOutServerDoesNotPost() {
        val s = DeviceSignIn()
        assertFalse(s.onOpen(fresh = true, hasCredential = true, nowMs = 0, signedOut = true))
        assertEquals(Action.NONE, s.onUrl(login, hasCredential = true, nowMs = 1_000, signedOut = true))
        assertEquals(Action.NONE, s.onUrl(login, hasCredential = true, nowMs = 10 * DeviceSignIn.REFUSAL_WINDOW_MS, signedOut = true))
        // A sign-out is not a refusal: once the flag is off again (an open
        // from the server list), the next landing posts.
        assertFalse(s.stopped)
        assertEquals(Action.POST, s.onUrl(login, hasCredential = true, nowMs = 20 * DeviceSignIn.REFUSAL_WINDOW_MS, signedOut = false))
    }

    @Test
    fun aSignOutThatArrivesAfterTheLandingCancelsThePost() {
        val s = DeviceSignIn()
        // The WebUI moves to the sign-in page before the app sees its sign-out request.
        assertEquals(Action.POST, s.onUrl(login, hasCredential = true, nowMs = 0))
        assertFalse(s.onPostDue(signedOut = true, onSignInPage = true))
        // The full load of the sign-in page that follows the sign-out: no post, no refusal.
        assertEquals(Action.NONE, s.onUrl(login, hasCredential = true, nowMs = 500, signedOut = true))
        assertFalse(s.stopped)
    }

    @Test
    fun anEndedSessionWithoutSignOutStillPostsOnce() {
        val s = DeviceSignIn()
        assertEquals(Action.POST, s.onUrl(login, hasCredential = true, nowMs = 0, signedOut = false))
        assertTrue(s.onPostDue(signedOut = false, onSignInPage = true))
        assertEquals(Action.NONE, s.onUrl("$origin/", hasCredential = true, nowMs = 1_500))
        // The post did not give a session: the next landing is a refusal, not a second post.
        assertEquals(Action.NONE, s.onUrl(login, hasCredential = true, nowMs = 2_000))
        assertTrue(s.stopped)
    }

    @Test
    fun aPostIsDroppedWhenThePageLeftTheSignInPage() {
        val s = DeviceSignIn()
        assertEquals(Action.POST, s.onUrl(login, hasCredential = true, nowMs = 0))
        assertFalse(s.onPostDue(signedOut = false, onSignInPage = false))
        // Not counted as a post: a later landing posts.
        assertEquals(Action.POST, s.onUrl(login, hasCredential = true, nowMs = 1_000))
    }

    @Test
    fun theBodyIsAFormField() {
        assertEquals("credential=abc-_DEF", String(DeviceSignIn.body("abc-_DEF"), Charsets.UTF_8))
        assertEquals("credential=a%2Bb%3D", String(DeviceSignIn.body("a+b="), Charsets.UTF_8))
    }
}
