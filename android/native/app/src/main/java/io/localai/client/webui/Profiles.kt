// SPDX-License-Identifier: MIT
package io.localai.client.webui

import android.webkit.CookieManager
import android.webkit.WebView
import androidx.webkit.ProfileStore
import androidx.webkit.WebViewCompat
import androidx.webkit.WebViewFeature

/**
 * One WebView profile per server origin, so each server keeps its own cookies
 * and storage (two servers on 127.0.0.1 with different ports would otherwise
 * share a `session` cookie and sign each other out). The client core names the
 * profile: "webui-" and the first 24 hex characters of the SHA-256 of the origin.
 *
 * Without MULTI_PROFILE support every server uses the default profile; P2P
 * servers are still separated by their own loopback hosts.
 */
object Profiles {
    private val NAME = Regex("webui-[0-9a-f]{24}")

    /** Returns `profile` when it is a name the client core makes; throws IllegalArgumentException otherwise. */
    fun validate(profile: String): String {
        require(NAME.matches(profile)) { "bad WebUI profile name" }
        return profile
    }

    fun supported(): Boolean = WebViewFeature.isFeatureSupported(WebViewFeature.MULTI_PROFILE)

    /**
     * Puts `webView` in `profile`. Call it on a new WebView before anything
     * else: a profile can be set only once, before the first load.
     */
    fun apply(webView: WebView, profile: String) {
        validate(profile)
        if (!supported()) return
        ProfileStore.getInstance().getOrCreateProfile(profile)
        WebViewCompat.setProfile(webView, profile)
    }

    /** The cookie store `webView` uses: its profile's, or the default one. */
    fun cookieManager(webView: WebView): CookieManager =
        if (supported()) WebViewCompat.getProfile(webView).cookieManager else CookieManager.getInstance()
}
