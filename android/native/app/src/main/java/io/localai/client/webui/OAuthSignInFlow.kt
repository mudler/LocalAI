// SPDX-License-Identifier: MIT
package io.localai.client.webui

/**
 * Tracks one OAuth/OIDC sign-in of the WebUI view: a Kotlin port of
 * OAuthSignInFlow in launcher/src/core/webuiPolicy.ts. WebuiPolicyParityTest
 * runs the sign-in cases of launcher/test/fixtures/webui-policy.json through it.
 *
 * The server's /api/auth/oidc/login and /api/auth/github/login routes set an
 * HttpOnly `oauth_state` cookie in the view's profile and redirect to the
 * identity provider. The callback needs that cookie, and the session cookie it
 * sets must land in the same profile, so the identity provider pages must load
 * in the view and not in the system browser:
 *   - a main-frame navigation to a login route on the server origin makes the
 *     flow pending for OAUTH_PENDING_TIMEOUT_MS;
 *   - the first cross-origin http(s) redirect while pending makes it active,
 *     and allows that origin in the main frame for OAUTH_SIGNIN_TIMEOUT_MS;
 *   - while active, more redirects add origins, up to OAUTH_MAX_ORIGINS; a link
 *     the identity provider page follows adds none;
 *   - a return to the server origin, a timeout and reset() end the flow.
 * Subframes, permissions, downloads, new windows, other schemes and URLs with
 * credentials follow the usual rules throughout.
 *
 * The WebView calls it from the UI thread and from its network thread, so every
 * method is synchronized. Times are milliseconds of a monotonic clock.
 */
class OAuthSignInFlow(private val serverOrigin: String) {
    enum class Phase { IDLE, PENDING, ACTIVE }

    private var phase = Phase.IDLE
    private var origins: Set<String> = emptySet()
    private var expiresAt = 0L

    @Synchronized
    fun phase(): Phase = phase

    /** Ends the flow now: the app navigates the view, the view closes, or the server changes. */
    @Synchronized
    fun reset() {
        phase = Phase.IDLE
        origins = emptySet()
    }

    private fun expire(nowMs: Long) {
        if (phase != Phase.IDLE && nowMs > expiresAt) reset()
    }

    private fun pending(nowMs: Long) {
        phase = Phase.PENDING
        origins = emptySet()
        expiresAt = nowMs + WebuiPolicy.OAUTH_PENDING_TIMEOUT_MS
    }

    private fun isServer(url: String) = WebuiPolicy.httpOrigin(url) == serverOrigin

    private fun decide(url: String): WebuiPolicy.Navigation =
        WebuiPolicy.decideNavigation(url, serverOrigin, subframe = false, allowedOrigins = if (phase == Phase.ACTIVE) origins else emptySet())

    /** A navigation the page starts (link, form, location), not a redirect. */
    @Synchronized
    fun onNavigate(url: String, subframe: Boolean, nowMs: Long): WebuiPolicy.Navigation {
        expire(nowMs)
        if (subframe) return WebuiPolicy.decideNavigation(url, serverOrigin, subframe = true)
        if (WebuiPolicy.isOAuthLoginUrl(url, serverOrigin)) {
            pending(nowMs)
            return WebuiPolicy.Navigation.Allow
        }
        if (isServer(url)) {
            reset()
            return WebuiPolicy.Navigation.Allow
        }
        // A cross-origin navigation while waiting for the login redirect ends the flow.
        if (phase == Phase.PENDING) reset()
        return decide(url)
    }

    /** A server redirect of a navigation. */
    @Synchronized
    fun onRedirect(url: String, subframe: Boolean, nowMs: Long): WebuiPolicy.Navigation {
        expire(nowMs)
        if (subframe) return WebuiPolicy.decideNavigation(url, serverOrigin, subframe = true)
        if (WebuiPolicy.isOAuthLoginUrl(url, serverOrigin)) {
            pending(nowMs)
            return WebuiPolicy.Navigation.Allow
        }
        if (isServer(url)) {
            reset()
            return WebuiPolicy.Navigation.Allow
        }
        val target = WebuiPolicy.httpOrigin(url)
        if (target != null) {
            if (phase == Phase.PENDING) {
                phase = Phase.ACTIVE
                origins = setOf(target)
                expiresAt = nowMs + WebuiPolicy.OAUTH_SIGNIN_TIMEOUT_MS
                return WebuiPolicy.Navigation.Allow
            }
            if (phase == Phase.ACTIVE) {
                if (target in origins) return WebuiPolicy.Navigation.Allow
                if (origins.size < WebuiPolicy.OAUTH_MAX_ORIGINS) {
                    origins = origins + target
                    return WebuiPolicy.Navigation.Allow
                }
            }
        } else if (phase == Phase.PENDING) {
            reset()
        }
        return decide(url)
    }

    /** The network check of a frame document; it never adds an origin. */
    @Synchronized
    fun onBeforeRequest(url: String, subframe: Boolean, nowMs: Long): WebuiPolicy.Navigation {
        expire(nowMs)
        if (subframe) return WebuiPolicy.decideNavigation(url, serverOrigin, subframe = true)
        return decide(url)
    }

    /** A page finished loading; `currentUrl` is the view's URL. */
    @Synchronized
    fun onLoad(currentUrl: String, nowMs: Long) {
        expire(nowMs)
        if (phase == Phase.PENDING) {
            // The login route rendered a page (an error, say) instead of redirecting.
            reset()
        } else if (phase == Phase.ACTIVE && isServer(currentUrl)) {
            reset()
        }
    }

    /** The main frame failed to load. */
    @Synchronized
    fun onLoadFailed(nowMs: Long) {
        expire(nowMs)
        if (phase == Phase.PENDING) reset()
    }
}
