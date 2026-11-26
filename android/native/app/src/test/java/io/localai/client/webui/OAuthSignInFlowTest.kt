// SPDX-License-Identifier: MIT
package io.localai.client.webui

import io.localai.client.webui.OAuthSignInFlow.Phase
import io.localai.client.webui.WebuiPolicy.Navigation
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The OAuth/OIDC sign-in of the WebUI view, the same checks as the
 * "OAuth/OIDC sign-in flow policy" tests of launcher/test/webuiPolicy.test.ts.
 * WebuiPolicyParityTest runs the shared fixture cases on top.
 */
class OAuthSignInFlowTest {
    private val remote = "https://remote.example.test"
    private val idp = "https://idp.example.test"

    private fun ext(url: String) = Navigation.External(url)

    @Test
    fun allowedOriginsApplyToTheMainFrameOnly() {
        val allowed = setOf(idp)
        assertEquals(Navigation.Allow, WebuiPolicy.decideNavigation("$idp/login", remote, subframe = false, allowedOrigins = allowed))
        assertTrue(WebuiPolicy.decideNavigation("$idp/login", remote, subframe = true, allowedOrigins = allowed) is Navigation.Deny)
        assertTrue(WebuiPolicy.decideNavigation("https://user:pw@idp.example.test/login", remote, subframe = false, allowedOrigins = allowed) is Navigation.Deny)
        assertEquals(
            ext("https://idp.example.test.evil.test/login"),
            WebuiPolicy.decideNavigation("https://idp.example.test.evil.test/login", remote, subframe = false, allowedOrigins = allowed),
        )
        assertEquals(ext("$idp/login"), WebuiPolicy.decideNavigation("$idp/login", remote, subframe = false))
    }

    @Test
    fun recognisesOnlyTheServerLoginRoutes() {
        assertTrue(WebuiPolicy.isOAuthLoginUrl("$remote/api/auth/oidc/login", remote))
        assertTrue(WebuiPolicy.isOAuthLoginUrl("$remote/api/auth/oidc/login?invite_code=abc", remote))
        assertTrue(WebuiPolicy.isOAuthLoginUrl("$remote/api/auth/github/login", remote))
        assertTrue(WebuiPolicy.isOAuthLoginUrl("$remote/api/auth/github/login?invite_code=abc", remote))
        assertFalse(WebuiPolicy.isOAuthLoginUrl("$remote/api/auth/oidc/callback?code=1", remote))
        assertFalse(WebuiPolicy.isOAuthLoginUrl("$remote/api/auth/login", remote))
        assertFalse(WebuiPolicy.isOAuthLoginUrl("$remote/prefix/api/auth/oidc/login", remote))
        assertFalse(WebuiPolicy.isOAuthLoginUrl("$idp/api/auth/oidc/login", remote))
        assertFalse(WebuiPolicy.isOAuthLoginUrl("https://user@remote.example.test/api/auth/oidc/login", remote))
    }

    @Test
    fun oidcSignInGoesThroughTheIdpAndEndsOnReturn() {
        val flow = OAuthSignInFlow(remote)
        val t = 1_000L
        assertEquals(Phase.IDLE, flow.phase())

        // Unrelated redirects go to the browser while idle.
        assertEquals(ext("$idp/authorize"), flow.onRedirect("$idp/authorize", false, t))
        assertEquals(ext("$idp/authorize"), flow.onBeforeRequest("$idp/authorize", false, t))

        assertEquals(Navigation.Allow, flow.onNavigate("$remote/api/auth/oidc/login", false, t))
        assertEquals(Phase.PENDING, flow.phase())
        assertEquals(Navigation.Allow, flow.onRedirect("$idp/authorize?state=s1", false, t + 500))
        assertEquals(Phase.ACTIVE, flow.phase())
        assertEquals(Navigation.Allow, flow.onBeforeRequest("$idp/authorize?state=s1", false, t + 500))

        // An IdP page load keeps the sign-in open for its forms and second factors.
        flow.onLoad("$idp/login", t + 600)
        assertEquals(Phase.ACTIVE, flow.phase())
        assertEquals(Navigation.Allow, flow.onNavigate("$idp/mfa", false, t + 700))

        // Other links of the IdP page open in the browser and do not end the sign-in.
        assertEquals(ext("https://docs.example.test/help"), flow.onNavigate("https://docs.example.test/help", false, t + 800))
        assertEquals(Phase.ACTIVE, flow.phase())

        // Subframes stay refused.
        assertTrue(flow.onNavigate("$idp/embed", true, t + 900) is Navigation.Deny)
        assertTrue(flow.onRedirect("$idp/embed", true, t + 900) is Navigation.Deny)
        assertTrue(flow.onBeforeRequest("$idp/embed", true, t + 900) is Navigation.Deny)

        // The callback on the server ends the sign-in.
        assertEquals(Navigation.Allow, flow.onRedirect("$remote/api/auth/oidc/callback?code=c1&state=s1", false, t + 1_000))
        assertEquals(Phase.IDLE, flow.phase())
        assertEquals(ext("$idp/account"), flow.onNavigate("$idp/account", false, t + 1_100))
        assertEquals(ext("$idp/account"), flow.onBeforeRequest("$idp/account", false, t + 1_100))
    }

    @Test
    fun githubSignInBoundsTheRedirectChain() {
        val flow = OAuthSignInFlow(remote)
        val t = 1_000L
        assertEquals(Navigation.Allow, flow.onNavigate("$remote/api/auth/github/login?invite_code=k", false, t))
        for (i in 1..WebuiPolicy.OAUTH_MAX_ORIGINS) {
            assertEquals(Navigation.Allow, flow.onRedirect("https://hop$i.example.test/auth", false, t))
        }
        val beyond = "https://hop${WebuiPolicy.OAUTH_MAX_ORIGINS + 1}.example.test/auth"
        assertEquals(ext(beyond), flow.onRedirect(beyond, false, t))
        assertEquals(Navigation.Allow, flow.onNavigate("https://hop1.example.test/callback", false, t))
        assertEquals(Navigation.Allow, flow.onNavigate("$remote/api/auth/github/callback?code=1&state=2", false, t))
        assertEquals(Phase.IDLE, flow.phase())
    }

    @Test
    fun pendingAndActiveStatesExpire() {
        val flow = OAuthSignInFlow(remote)
        assertEquals(Navigation.Allow, flow.onNavigate("$remote/api/auth/oidc/login", false, 1_000))
        assertEquals(ext("$idp/authorize"), flow.onRedirect("$idp/authorize", false, 1_000 + WebuiPolicy.OAUTH_PENDING_TIMEOUT_MS + 1))
        assertEquals(Phase.IDLE, flow.phase())

        assertEquals(Navigation.Allow, flow.onNavigate("$remote/api/auth/oidc/login", false, 2_000))
        assertEquals(Navigation.Allow, flow.onRedirect("$idp/authorize", false, 2_100))
        assertEquals(Navigation.Allow, flow.onBeforeRequest("$idp/login", false, 2_100 + WebuiPolicy.OAUTH_SIGNIN_TIMEOUT_MS))
        assertEquals(ext("$idp/login"), flow.onBeforeRequest("$idp/login", false, 2_100 + WebuiPolicy.OAUTH_SIGNIN_TIMEOUT_MS + 1))
        assertEquals(Phase.IDLE, flow.phase())
    }

    @Test
    fun pendingEndsWithoutAnIdpRedirect() {
        val flow = OAuthSignInFlow(remote)
        flow.onNavigate("$remote/api/auth/oidc/login", false, 1_000)
        flow.onLoad("$remote/api/auth/oidc/login", 1_100)
        assertEquals(Phase.IDLE, flow.phase())

        flow.onNavigate("$remote/api/auth/oidc/login", false, 2_000)
        flow.onLoadFailed(2_100)
        assertEquals(Phase.IDLE, flow.phase())

        flow.onNavigate("$remote/api/auth/oidc/login", false, 3_000)
        assertTrue(flow.onRedirect("javascript:alert(1)", false, 3_100) is Navigation.Deny)
        assertEquals(Phase.IDLE, flow.phase())

        flow.onNavigate("$remote/api/auth/oidc/login", false, 4_000)
        assertEquals(ext("https://evil.example.test/"), flow.onNavigate("https://evil.example.test/", false, 4_100))
        assertEquals(Phase.IDLE, flow.phase())

        flow.onNavigate("$remote/api/auth/oidc/login", false, 5_000)
        assertEquals(Navigation.Allow, flow.onNavigate("$remote/redirect", false, 5_100))
        assertEquals(Phase.IDLE, flow.phase())
        assertEquals(ext("https://evil.example.test/"), flow.onRedirect("https://evil.example.test/", false, 5_200))
    }

    @Test
    fun activeEndsOnResetOrAServerPage() {
        val flow = OAuthSignInFlow(remote)
        flow.onNavigate("$remote/api/auth/oidc/login", false, 1_000)
        flow.onRedirect("$idp/authorize", false, 1_100)
        assertEquals(Phase.ACTIVE, flow.phase())
        flow.onLoad("$remote/login", 1_200)
        assertEquals(Phase.IDLE, flow.phase())

        flow.onNavigate("$remote/api/auth/oidc/login", false, 2_000)
        flow.onRedirect("$idp/authorize", false, 2_100)
        flow.reset()
        assertEquals(Phase.IDLE, flow.phase())
        assertEquals(ext("$idp/authorize"), flow.onBeforeRequest("$idp/authorize", false, 2_200))
    }
}
