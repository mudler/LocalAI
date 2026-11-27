// SPDX-License-Identifier: MIT
package io.localai.client.webui

import java.net.URI
import java.net.URISyntaxException
import java.net.URLEncoder

/**
 * When the WebUI view of a P2P server posts the device credential to the
 * server's session route, so a device paired with a sign-in invite needs no
 * password. One instance per view; every method runs on the UI thread.
 *
 * Rules:
 *  - the first open after pairing posts once, instead of loading the page;
 *  - each landing on the sign-in page posts once;
 *  - a landing on the sign-in page soon after a post, or on the session
 *    route itself (the server answered 4xx), is a refusal: no automatic post
 *    until [reset] (a new view, a new open, a new pairing);
 *  - after the person signs out in the WebUI (the page posts to
 *    [SIGN_OUT_PATH]), the server is signed out: no automatic post at all
 *    until the person opens the server again from the server list, or pairs
 *    again. The caller keeps that flag (DeviceCredentials.signedOut) and
 *    passes it in.
 *
 * The window keeps a cookie that never sticks from looping: the post would
 * redirect to `/`, the page would go to the sign-in page again, and so on.
 *
 * The post of a landing waits [SIGN_OUT_GRACE_MS] ([onPostDue]): on sign-out
 * the WebUI moves to its sign-in page with the history API in the same tick
 * as it sends the sign-out request, and the two reach the app in either
 * order.
 */
class DeviceSignIn(private val windowMs: Long = REFUSAL_WINDOW_MS) {
    enum class Action {
        /** Do nothing. */
        NONE,

        /** Post the credential to [SESSION_PATH]. */
        POST,

        /** The view shows the session route's error answer: load the sign-in page instead. */
        LEAVE,
    }

    private var refused = false
    private var lastPostMs = -1L

    /** True after a refusal, until [reset]. */
    val stopped: Boolean get() = refused

    fun reset() {
        refused = false
        lastPostMs = -1L
    }

    /** The view of a server opens. Answers true when it posts the credential instead of loading the page. */
    fun onOpen(fresh: Boolean, hasCredential: Boolean, nowMs: Long, signedOut: Boolean = false): Boolean {
        if (!fresh || !hasCredential || refused || signedOut) return false
        lastPostMs = nowMs
        return true
    }

    /**
     * The main frame committed [url] on the server origin: a page load or a
     * history change of the page (the WebUI goes to its sign-in page with
     * the history API, without a load). [signedOut]: the person signed out
     * of this server; a landing then is the result, not an ended session.
     * POST is due after [SIGN_OUT_GRACE_MS]: see [onPostDue].
     */
    fun onUrl(url: String, hasCredential: Boolean, nowMs: Long, signedOut: Boolean = false): Action {
        val path = pathOf(url) ?: return Action.NONE
        if (path == SESSION_PATH) {
            // A good post redirects to "/": this is the error answer.
            refused = true
            lastPostMs = -1L
            return Action.LEAVE
        }
        if (!isSignInPath(path)) return Action.NONE
        if (signedOut) {
            lastPostMs = -1L
            return Action.NONE
        }
        if (lastPostMs >= 0 && nowMs - lastPostMs in 0 until windowMs) {
            // The post did not give a session that the page accepts.
            refused = true
            lastPostMs = -1L
            return Action.NONE
        }
        if (refused || !hasCredential) return Action.NONE
        lastPostMs = nowMs
        return Action.POST
    }

    /**
     * The post that [onUrl] asked for is due. True when it goes ahead: the
     * person did not sign out meanwhile ([signedOut]) and the page is still
     * on the sign-in page ([onSignInPage]).
     */
    fun onPostDue(signedOut: Boolean, onSignInPage: Boolean): Boolean {
        if (signedOut || refused || !onSignInPage) {
            lastPostMs = -1L
            return false
        }
        return true
    }

    companion object {
        /** The server route that turns a device credential into a web session. */
        const val SESSION_PATH = "/api/p2p/device/session"

        /** The sign-in page of the WebUI (its router's `/login`). */
        const val SIGN_IN_PATH = "/login"

        /** The WebUI's sign-out route; its page posts to it when the person signs out. */
        const val SIGN_OUT_PATH = "/api/auth/logout"

        const val REFUSAL_WINDOW_MS = 20_000L

        /** How long the post of a landing waits for a sign-out request of the same moment. */
        const val SIGN_OUT_GRACE_MS = 1_000L

        /** The form body of the post. */
        fun body(credential: String): ByteArray =
            ("credential=" + URLEncoder.encode(credential, "UTF-8")).toByteArray(Charsets.UTF_8)

        /**
         * Only the exact path: a forwarder origin serves the WebUI at its
         * root, and a suffix match would also take `/api/auth/oidc/login`.
         */
        fun isSignInPath(path: String): Boolean = path.trimEnd('/') == SIGN_IN_PATH

        /**
         * True when a request of the page is the sign-out: a POST to
         * [SIGN_OUT_PATH] on [origin]. The exact path, as for [isSignInPath].
         */
        fun isSignOutRequest(method: String?, url: String, origin: String): Boolean {
            if (!"POST".equals(method, ignoreCase = true)) return false
            if (WebuiPolicy.httpOrigin(url) != origin) return false
            return pathOf(url)?.trimEnd('/') == SIGN_OUT_PATH
        }

        internal fun pathOf(url: String): String? = try {
            URI(url).rawPath?.ifEmpty { "/" }
        } catch (_: URISyntaxException) {
            null
        }
    }
}
