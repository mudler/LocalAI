// SPDX-License-Identifier: MIT
package io.localai.client.webui

import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Checks the Kotlin WebUI policy against launcher/test/fixtures/webui-policy.json,
 * which the launcher test suite writes from the TypeScript functions. The
 * fixture wins: a change to the TypeScript policy without the same change here
 * fails this test. Reasons are not compared, only actions, routes and URLs.
 */
class WebuiPolicyParityTest {
    private val fixture: JSONObject by lazy {
        val stream = javaClass.getResourceAsStream("/webui-policy.json")
            ?: throw AssertionError("webui-policy.json is not on the test classpath")
        JSONObject(stream.bufferedReader(Charsets.UTF_8).use { it.readText() })
    }

    private fun cases(name: String): List<JSONObject> {
        val arr: JSONArray = fixture.getJSONArray(name)
        return (0 until arr.length()).map { arr.getJSONObject(it) }
    }

    @Test
    fun fixtureSchemaIsKnown() {
        assertEquals(1, fixture.getInt("schema"))
    }

    @Test
    fun navigation() {
        val all = cases("navigation")
        assertTrue("no navigation cases", all.isNotEmpty())
        var checked = 0
        for (c in all) {
            val url = c.getString("url")
            val origin = c.getString("origin")
            val frame = c.getString("frame")
            val expect = c.getJSONObject("expect")
            val label = "navigation url=${JSONObject.quote(url)} origin=$origin frame=$frame"
            val got = WebuiPolicy.decideNavigation(url, origin, subframe = frame == "sub")
            when (val action = expect.getString("action")) {
                "allow" -> assertEquals(label, WebuiPolicy.Navigation.Allow, got)
                "external" -> assertEquals(label, WebuiPolicy.Navigation.External(expect.getString("url")), got)
                "deny" -> assertTrue("$label: expected deny, got $got", got is WebuiPolicy.Navigation.Deny)
                else -> throw AssertionError("$label: unknown action $action")
            }
            checked++
        }
        assertEquals("every navigation case checked", all.size, checked)
    }

    @Test
    fun windowOpen() {
        val all = cases("windowOpen")
        assertTrue("no windowOpen cases", all.isNotEmpty())
        var checked = 0
        for (c in all) {
            val url = c.getString("url")
            val origin = c.getString("origin")
            val expect = c.getJSONObject("expect")
            val label = "windowOpen url=${JSONObject.quote(url)} origin=$origin"
            val got = WebuiPolicy.decideWindowOpen(url, origin)
            when (val route = expect.getString("route")) {
                "same-view" -> assertEquals(label, WebuiPolicy.WindowOpen.SameView(expect.getString("url")), got)
                "external" -> assertEquals(label, WebuiPolicy.WindowOpen.External(expect.getString("url")), got)
                "none" -> assertTrue("$label: expected none, got $got", got is WebuiPolicy.WindowOpen.None)
                else -> throw AssertionError("$label: unknown route $route")
            }
            checked++
        }
        assertEquals("every windowOpen case checked", all.size, checked)
    }

    @Test
    fun permission() {
        val all = cases("permission")
        assertTrue("no permission cases", all.isNotEmpty())
        var checked = 0
        for (c in all) {
            val r = c.getJSONObject("request")
            val origin = c.getString("origin")
            val types = r.optJSONArray("mediaTypes")
            val request = WebuiPolicy.PermissionRequest(
                permission = r.getString("permission"),
                requestingUrl = r.getString("requestingUrl"),
                topUrl = r.getString("topUrl"),
                mediaTypes = if (types == null) emptyList() else (0 until types.length()).map { types.getString(it) },
                userGesture = r.getBoolean("userGesture"),
            )
            val store = storeOf(c.getJSONObject("store"))
            val label = "permission request=$r origin=$origin store=${c.getJSONObject("store")}"
            val want = when (val e = c.getString("expect")) {
                "allow" -> WebuiPolicy.Permission.ALLOW
                "deny" -> WebuiPolicy.Permission.DENY
                "ask" -> WebuiPolicy.Permission.ASK
                else -> throw AssertionError("$label: unknown decision $e")
            }
            assertEquals(label, want, WebuiPolicy.decidePermission(request, origin, store))
            checked++
        }
        assertEquals("every permission case checked", all.size, checked)
    }

    @Test
    fun fileName() {
        val all = cases("fileName")
        assertTrue("no fileName cases", all.isNotEmpty())
        var checked = 0
        for (c in all) {
            val name = c.getString("name")
            val label = "fileName name=${JSONObject.quote(name)}"
            val got = if (c.has("fallback")) {
                WebuiPolicy.sanitizeFileName(name, c.getString("fallback"))
            } else {
                WebuiPolicy.sanitizeFileName(name)
            }
            assertEquals(label, c.getString("expect"), got)
            checked++
        }
        assertEquals("every fileName case checked", all.size, checked)
    }

    @Test
    fun fixtureCaseCounts() {
        // A fixture that loses cases silently would make the parity weaker; the
        // counts move only together with launcher/test/webuiPolicyCases.ts.
        assertEquals(40, cases("navigation").size)
        assertEquals(12, cases("windowOpen").size)
        assertEquals(18, cases("permission").size)
        assertEquals(18, cases("fileName").size)
    }

    @Test
    fun whatwgHostFormsBeyondTheFixture() {
        // Forms Chromium canonicalises that java.net.URI and OkHttp alone do not.
        val o = "http://127.0.0.1:41873"
        assertEquals(o, WebuiPolicy.httpOrigin("http://0x7f.1:41873/app"))
        assertEquals(o, WebuiPolicy.httpOrigin("http://0177.0.0.1:41873/app"))
        assertEquals(o, WebuiPolicy.httpOrigin("http://127.0.0.1.:41873/app"))
        assertEquals(o, WebuiPolicy.httpOrigin("  http://127.0.0.1:41873/app\t"))
        assertEquals(o, WebuiPolicy.httpOrigin("HTTP://127.0.0.1:41873/app"))
        // An IPv4-mapped IPv6 literal is another origin for Chromium.
        assertEquals("http://[::ffff:7f00:1]:41873", WebuiPolicy.httpOrigin("http://[::ffff:127.0.0.1]:41873/app"))
        assertEquals(WebuiPolicy.Navigation.External("http://[::ffff:7f00:1]:41873/app"),
            WebuiPolicy.decideNavigation("http://[::ffff:127.0.0.1]:41873/app", o, subframe = false))
        // Hosts that are not valid IPv4 but end in a number are not URLs.
        assertEquals(null, WebuiPolicy.httpOrigin("http://1.2.3.4.5/"))
        assertEquals(null, WebuiPolicy.httpOrigin("http://256.256.256.256/"))
        assertEquals(null, WebuiPolicy.httpOrigin("http://example.09/"))
        assertEquals(null, WebuiPolicy.httpOrigin("http://a<b.example/"))
        assertEquals(null, WebuiPolicy.httpOrigin("http://user@ai.example.com/"))
        assertEquals("https://ai.example.com", WebuiPolicy.httpOrigin("https://ai.example.com"))
        // A same-origin page outside /app opens in the browser.
        assertEquals(WebuiPolicy.WindowOpen.External("https://ai.example.com/swagger/index.html"),
            WebuiPolicy.decideWindowOpen("https://ai.example.com/swagger/index.html", "https://ai.example.com"))
        assertEquals(WebuiPolicy.WindowOpen.External("https://ai.example.com/api"),
            WebuiPolicy.decideWindowOpen("https://AI.example.com:443/api", "https://ai.example.com"))
    }

    private fun storeOf(o: JSONObject): Map<String, Map<String, String>> =
        o.keys().asSequence().associateWith { origin ->
            val choices = o.getJSONObject(origin)
            choices.keys().asSequence().associateWith { choices.getString(it) }
        }
}
