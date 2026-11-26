// SPDX-License-Identifier: MIT
package io.localai.client.webui

import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import java.math.BigInteger
import java.util.Locale

/**
 * The security policy of the WebUI view: a Kotlin port of
 * launcher/src/core/webuiPolicy.ts. The WebView callbacks that need it are
 * synchronous on the UI thread and cannot wait for the TypeScript side, so the
 * decisions live here too. WebuiPolicyParityTest checks every case of
 * launcher/test/fixtures/webui-policy.json (written from the TypeScript
 * functions); where the two disagree, the fixture wins.
 *
 * URLs are compared the way Chromium (and the WHATWG URL parser of the
 * TypeScript side) sees them: lower-case host, punycode, default port dropped,
 * numeric IPv4 forms such as 2130706433 or 0x7f.1 canonicalised. OkHttp's
 * HttpUrl does most of this but keeps numeric hosts as names and turns
 * IPv4-mapped IPv6 literals into IPv4, so the host is fixed up here.
 */
object WebuiPolicy {
    sealed interface Navigation {
        object Allow : Navigation {
            override fun toString() = "Allow"
        }

        /** Do not navigate the view; open this URL in the system browser instead. */
        data class External(val url: String) : Navigation

        data class Deny(val reason: String) : Navigation
    }

    sealed interface WindowOpen {
        /** Load this URL in the WebUI view itself (same origin, WebUI page). */
        data class SameView(val url: String) : WindowOpen

        data class External(val url: String) : WindowOpen

        data class None(val reason: String) : WindowOpen
    }

    enum class Permission { ALLOW, DENY, ASK }

    data class PermissionRequest(
        val permission: String,
        /** The URL of the frame that asks. */
        val requestingUrl: String,
        /** The URL of the top-level page of the view. */
        val topUrl: String,
        /** The kinds of media for a `media` request; empty means unknown. */
        val mediaTypes: List<String>,
        /** The page had a user action within GESTURE_WINDOW_MS. */
        val userGesture: Boolean,
    )

    /** Chromium's transient user activation lasts about five seconds. */
    const val GESTURE_WINDOW_MS = 5000L

    // ---- origins ----------------------------------------------------------------

    /**
     * The origin of an http or https URL as `scheme://host[:port]`, or null for
     * any other scheme, for text that is not a URL, and for a URL with user info.
     */
    fun httpOrigin(url: String): String? {
        val p = parse(url) ?: return null
        val http = p.http ?: return null
        if (http.hasCredentials) return null
        return http.origin
    }

    private fun isServerOrigin(url: String, serverOrigin: String): Boolean {
        val o = httpOrigin(url)
        return o != null && o == serverOrigin
    }

    // ---- navigation -------------------------------------------------------------

    /**
     * Decides a navigation the page started (link, form, location, redirect).
     * `allowedOrigins` are http(s) origins an OAuth/OIDC sign-in allows for the
     * main frame for a while (see OAuthSignInFlow); they never apply to a subframe.
     */
    fun decideNavigation(
        url: String,
        serverOrigin: String,
        subframe: Boolean,
        allowedOrigins: Set<String> = emptySet(),
    ): Navigation {
        if (isServerOrigin(url, serverOrigin)) return Navigation.Allow
        val p = parse(url) ?: return Navigation.Deny("not a URL")
        if (subframe) {
            // srcdoc frames and object URLs the server page made itself; nothing
            // else loads inside the view, and a frame never opens the browser.
            if (p.href == "about:blank" || p.href == "about:srcdoc") return Navigation.Allow
            if (p.scheme == "blob" && p.opaquePath != null && isServerOrigin(p.opaquePath, serverOrigin)) {
                return Navigation.Allow
            }
            return Navigation.Deny("subframe to ${p.scheme}:")
        }
        if (p.http != null) {
            if (p.http.hasCredentials) return Navigation.Deny("credentials in URL")
            if (p.http.origin in allowedOrigins) return Navigation.Allow
            return Navigation.External(p.href)
        }
        if (p.scheme == "mailto") return Navigation.External(p.href)
        // javascript:, file:, data:, blob:, about:, intent:, custom schemes.
        return Navigation.Deny("scheme ${p.scheme}:")
    }

    // ---- OAuth / OIDC sign-in ---------------------------------------------------

    /**
     * Server routes that start an OAuth/OIDC sign-in: they set the `oauth_state`
     * cookie on the server origin and redirect to the identity provider.
     */
    private val OAUTH_LOGIN_PATHS = setOf("/api/auth/oidc/login", "/api/auth/github/login")

    /** How long a navigation to a login route waits for the server's redirect to the identity provider. */
    const val OAUTH_PENDING_TIMEOUT_MS = 30_000L

    /** How long a sign-in stays open: the server's `oauth_state` cookie lives 10 minutes. */
    const val OAUTH_SIGNIN_TIMEOUT_MS = 10 * 60 * 1000L

    /** The most identity provider origins one sign-in can go through. */
    const val OAUTH_MAX_ORIGINS = 5

    /** True when `url` is a route on `serverOrigin` that starts an OAuth/OIDC sign-in. */
    fun isOAuthLoginUrl(url: String, serverOrigin: String): Boolean {
        val p = parse(url) ?: return false
        val http = p.http ?: return false
        if (http.hasCredentials || http.origin != serverOrigin) return false
        return http.path in OAUTH_LOGIN_PATHS
    }

    // ---- new windows ------------------------------------------------------------

    private fun sameViewPath(path: String): Boolean = path == "/app" || path.startsWith("/app/")

    /** Decides window.open and target=_blank. The answer never creates a window. */
    fun decideWindowOpen(url: String, serverOrigin: String): WindowOpen {
        if (isServerOrigin(url, serverOrigin)) {
            val p = parse(url)
            val http = p?.http
            if (p != null && http != null) {
                // The API, the swagger page and sign-in hand-overs open in the
                // browser, where a new tab is what the page meant.
                return if (sameViewPath(http.path)) WindowOpen.SameView(p.href) else WindowOpen.External(p.href)
            }
        }
        return when (val nav = decideNavigation(url, serverOrigin, subframe = false)) {
            is Navigation.External -> WindowOpen.External(nav.url)
            is Navigation.Deny -> WindowOpen.None(nav.reason)
            Navigation.Allow -> WindowOpen.None("refused")
        }
    }

    // ---- permissions ------------------------------------------------------------

    /**
     * Decides a permission request. `store` is the TypeScript PermissionStore:
     * origin to { "media": "allow" | "deny" }.
     */
    fun decidePermission(r: PermissionRequest, serverOrigin: String, store: Map<String, Map<String, String>>): Permission {
        // Both the asking frame and the page around it must be the server.
        if (!isServerOrigin(r.requestingUrl, serverOrigin) || !isServerOrigin(r.topUrl, serverOrigin)) {
            return Permission.DENY
        }
        if (r.permission == "clipboard-sanitized-write") return if (r.userGesture) Permission.ALLOW else Permission.DENY
        if (r.permission == "media") {
            if (r.mediaTypes.any { it != "audio" && it != "video" }) return Permission.DENY
            if (!r.userGesture) return Permission.DENY
            return when (store[serverOrigin]?.get("media")) {
                "allow" -> Permission.ALLOW
                "deny" -> Permission.DENY
                else -> Permission.ASK
            }
        }
        return Permission.DENY
    }

    // ---- downloads --------------------------------------------------------------

    private const val MAX_FILE_NAME = 120

    // Control characters, bidirectional overrides and isolates, zero-width characters.
    private val HIDDEN_CHARS = Regex(
        "[\\x{0000}-\\x{001f}\\x{007f}-\\x{009f}\\x{200b}-\\x{200f}\\x{202a}-\\x{202e}\\x{2066}-\\x{2069}\\x{feff}]",
    )
    private val BAD_CHARS = Regex("[<>:\"|?*]")

    // JavaScript's \s, which Java's \s is not.
    private const val JS_SPACE =
        "\\t\\n\\x{000b}\\f\\r \\x{00a0}\\x{1680}\\x{2000}-\\x{200a}\\x{2028}\\x{2029}\\x{202f}\\x{205f}\\x{3000}\\x{feff}"
    private val LEADING = Regex("^[.$JS_SPACE]+")
    private val TRAILING = Regex("[.$JS_SPACE]+$")
    private val WINDOWS_RESERVED = Regex("^(con|prn|aux|nul|com[0-9]|lpt[0-9])(\\..*)?$", RegexOption.IGNORE_CASE)

    /**
     * The file name to save a download under. The page chooses it, so it is
     * cleaned: no folders, no hidden or direction-override characters, no
     * leading dots, no trailing dots or spaces, no reserved Windows names.
     */
    fun sanitizeFileName(name: String, fallback: String = "download"): String {
        var s = name
        s = s.substring(maxOf(s.lastIndexOf('/'), s.lastIndexOf('\\')) + 1)
        s = HIDDEN_CHARS.replace(s, "")
        s = BAD_CHARS.replace(s, "_")
        s = LEADING.replace(s, "")
        s = TRAILING.replace(s, "")
        if (WINDOWS_RESERVED.matches(s)) s = "_$s"
        if (s.length > MAX_FILE_NAME) {
            val dot = s.lastIndexOf('.')
            val ext = if (dot > 0 && s.length - dot <= 16) s.substring(dot) else ""
            s = s.substring(0, MAX_FILE_NAME - ext.length) + ext
        }
        return s.ifEmpty { fallback }
    }

    // ---- URL parsing ------------------------------------------------------------

    private class HttpParts(val origin: String, val hasCredentials: Boolean, val path: String)

    /**
     * A parsed URL. `href` is the serialised URL (WHATWG for http and https and
     * for the simple non-special forms the policy hands out, such as mailto);
     * `opaquePath` is the path of a non-special URL such as blob:.
     */
    private class Parsed(val scheme: String, val href: String, val http: HttpParts?, val opaquePath: String?)

    private val SPECIAL_SCHEMES = setOf("http", "https", "ws", "wss", "ftp", "file")

    private fun parse(input: String): Parsed? {
        // WHATWG: strip leading and trailing C0 controls and spaces, drop tabs and newlines.
        val s = input.trim { it <= ' ' }.filterNot { it == '\t' || it == '\n' || it == '\r' }
        val scheme = schemeOf(s) ?: return null
        if (scheme == "http" || scheme == "https") return parseHttp(s, scheme)
        // Other special schemes are only ever denied; their exact form does not matter.
        if (scheme in SPECIAL_SCHEMES) return Parsed(scheme, s, null, null)
        val rest = s.substring(scheme.length + 1)
        val hash = rest.indexOf('#')
        val beforeHash = if (hash >= 0) rest.substring(0, hash) else rest
        val q = beforeHash.indexOf('?')
        val path = if (q >= 0) beforeHash.substring(0, q) else beforeHash
        val query = if (q >= 0) beforeHash.substring(q + 1) else null
        val fragment = if (hash >= 0) rest.substring(hash + 1) else null
        val href = buildString {
            append(scheme).append(':')
            append(percentEncode(path, ""))
            if (query != null) append('?').append(percentEncode(query, " \"#<>"))
            if (fragment != null) append('#').append(percentEncode(fragment, " \"<>`"))
        }
        val opaque = if (path.startsWith("/")) null else percentEncode(path, "")
        return Parsed(scheme, href, null, opaque)
    }

    /** The lower-case scheme of `s` (without the colon), or null when it has none. */
    private fun schemeOf(s: String): String? {
        if (s.isEmpty() || !isAsciiAlpha(s[0])) return null
        for (i in 1 until s.length) {
            val c = s[i]
            if (c == ':') return s.substring(0, i).lowercase(Locale.ROOT)
            if (!(isAsciiAlpha(c) || c in '0'..'9' || c == '+' || c == '-' || c == '.')) return null
        }
        return null
    }

    private fun isAsciiAlpha(c: Char) = c in 'a'..'z' || c in 'A'..'Z'

    /** Percent-encodes C0 controls, non-ASCII and the characters in `extra`, as UTF-8. */
    private fun percentEncode(s: String, extra: String): String {
        val out = StringBuilder()
        var i = 0
        while (i < s.length) {
            val cp = s.codePointAt(i)
            val n = Character.charCount(cp)
            if (cp < 0x20 || cp > 0x7e || (cp < 0x80 && extra.indexOf(cp.toChar()) >= 0)) {
                for (b in String(Character.toChars(cp)).toByteArray(Charsets.UTF_8)) {
                    out.append('%').append(String.format(Locale.ROOT, "%02X", b.toInt() and 0xff))
                }
            } else {
                out.appendCodePoint(cp)
            }
            i += n
        }
        return out.toString()
    }

    // WHATWG forbidden domain code points that OkHttp lets through in a host.
    private const val FORBIDDEN_HOST_CHARS = "#%/:<>?@[\\]^|"

    private fun parseHttp(s: String, scheme: String): Parsed? {
        val url = s.toHttpUrlOrNull() ?: return null
        val host = url.host
        val serialisedHost = when {
            host.contains(':') -> "[$host]"
            rawHostIsBracketed(s, scheme) -> {
                // OkHttp turns [::ffff:a.b.c.d] into a.b.c.d; WHATWG keeps the
                // IPv6 form, which is another origin.
                val v4 = parseIpv4(host) ?: return null
                String.format(Locale.ROOT, "[::ffff:%x:%x]", (v4 ushr 16) and 0xffff, v4 and 0xffff)
            }
            else -> {
                if (host.isEmpty() || host.any { it <= ' ' || it.code == 0x7f || FORBIDDEN_HOST_CHARS.indexOf(it) >= 0 }) {
                    return null
                }
                if (endsInNumber(host)) ipv4String(parseIpv4(host) ?: return null) else host
            }
        }
        val defaultPort = if (scheme == "https") 443 else 80
        val origin = if (url.port == defaultPort) "$scheme://$serialisedHost" else "$scheme://$serialisedHost:${url.port}"
        val href = buildString {
            append(origin).append(url.encodedPath)
            url.encodedQuery?.let { append('?').append(it) }
            url.encodedFragment?.let { append('#').append(it) }
        }
        val creds = url.encodedUsername.isNotEmpty() || url.encodedPassword.isNotEmpty()
        return Parsed(scheme, href, HttpParts(origin, creds, url.encodedPath), null)
    }

    /** True when the host written in `s` (after `scheme:`) is an IPv6 literal. */
    private fun rawHostIsBracketed(s: String, scheme: String): Boolean {
        var i = scheme.length + 1
        while (i < s.length && (s[i] == '/' || s[i] == '\\')) i++
        var end = i
        while (end < s.length && s[end] != '/' && s[end] != '\\' && s[end] != '?' && s[end] != '#') end++
        val authority = s.substring(i, end)
        return authority.substring(authority.lastIndexOf('@') + 1).startsWith("[")
    }

    // WHATWG "ends in a number".
    private fun endsInNumber(host: String): Boolean {
        val parts = host.split('.').toMutableList()
        if (parts.last().isEmpty()) {
            if (parts.size == 1) return false
            parts.removeAt(parts.size - 1)
        }
        val last = parts.last()
        if (last.isNotEmpty() && last.all { it in '0'..'9' }) return true
        return parseIpv4Number(last) != null
    }

    // WHATWG IPv4 parser; null on failure. The result is the address as an unsigned 32-bit value in an Int.
    private fun parseIpv4(host: String): Int? {
        val parts = host.split('.').toMutableList()
        if (parts.last().isEmpty() && parts.size > 1) parts.removeAt(parts.size - 1)
        if (parts.size > 4) return null
        val numbers = parts.map { parseIpv4Number(it) ?: return null }
        val byte = BigInteger.valueOf(255)
        for (i in 0 until numbers.size - 1) if (numbers[i] > byte) return null
        val limit = BigInteger.valueOf(256).pow(5 - numbers.size)
        if (numbers.last() >= limit) return null
        var v = numbers.last().toLong()
        for (i in 0 until numbers.size - 1) v += numbers[i].toLong() shl (8 * (3 - i))
        return v.toInt()
    }

    private fun parseIpv4Number(part: String): BigInteger? {
        if (part.isEmpty()) return null
        var s = part
        var radix = 10
        if (s.length >= 2 && (s.startsWith("0x") || s.startsWith("0X"))) {
            s = s.substring(2)
            radix = 16
        } else if (s.length >= 2 && s[0] == '0') {
            s = s.substring(1)
            radix = 8
        }
        if (s.isEmpty()) return BigInteger.ZERO
        if (!s.all { Character.digit(it, radix) >= 0 && it.code < 0x80 }) return null
        return BigInteger(s, radix)
    }

    private fun ipv4String(v: Int): String =
        "${(v ushr 24) and 0xff}.${(v ushr 16) and 0xff}.${(v ushr 8) and 0xff}.${v and 0xff}"
}
