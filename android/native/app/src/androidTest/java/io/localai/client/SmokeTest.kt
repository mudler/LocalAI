// SPDX-License-Identifier: MIT
package io.localai.client

import android.app.Activity
import android.os.SystemClock
import android.view.View
import android.view.ViewGroup
import android.webkit.WebView
import androidx.test.core.app.ActivityScenario
import androidx.test.espresso.matcher.ViewMatchers.isAssignableFrom
import androidx.test.espresso.web.assertion.WebViewAssertions.webMatches
import androidx.test.espresso.web.model.Atoms
import androidx.test.espresso.web.sugar.Web.WebInteraction
import androidx.test.espresso.web.sugar.Web.onWebView
import androidx.test.espresso.web.webdriver.DriverAtoms.findElement
import androidx.test.espresso.web.webdriver.DriverAtoms.getText
import androidx.test.espresso.web.webdriver.DriverAtoms.webClick
import androidx.test.espresso.web.webdriver.Locator
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.filters.LargeTest
import androidx.test.platform.app.InstrumentationRegistry
import com.getcapacitor.CapacitorWebView
import io.localai.client.p2p.GoP2PEngine
import org.hamcrest.Matchers.equalTo
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File

/**
 * Small smoke tests on an emulator, run by CI (.github/workflows/android-client.yaml,
 * job emulator-smoke) against android/test/fixtures/fake-localai.mjs, which CI maps
 * to the emulator's 127.0.0.1:18080 with `adb reverse`.
 *
 * Each test starts from an empty server list: the client config file is deleted
 * before the activity starts.
 */
@RunWith(AndroidJUnit4::class)
@LargeTest
class SmokeTest {
    private var scenario: ActivityScenario<MainActivity>? = null

    @Before
    fun emptyServerList() {
        val files = InstrumentationRegistry.getInstrumentation().targetContext.filesDir
        files.listFiles { f -> f.name.startsWith(CONFIG_FILE) }?.forEach { it.delete() }
    }

    @After
    fun closeApp() {
        scenario?.close()
    }

    @Test
    fun appStarts() {
        launch()
        eventually("the first-connect heading") {
            shell()
                .withElement(findElement(Locator.CSS_SELECTOR, "[data-screen=\"first-connect\"] h1"))
                .check(webMatches(getText(), equalTo(FIRST_CONNECT_TITLE)))
        }
    }

    @Test
    fun addUrlServer() {
        launch()
        addFakeServer()
        eventually("the server saved in the config file") {
            val text = configFile().readText()
            assertTrue("config does not name $FAKE_ORIGIN: $text", text.contains("\"$FAKE_ORIGIN\""))
        }
    }

    @Test
    fun webuiLoads() {
        launch()
        addFakeServer()
        eventually("the WebUI view on $FAKE_ORIGIN/app titled Fake LocalAI", timeoutMs = 40_000) {
            val (url, title) = onMain { activity ->
                val view = webViews(activity.window.decorView).firstOrNull { it !is CapacitorWebView }
                    ?: throw AssertionError("no WebUI WebView yet")
                view.url to view.title
            }
            assertTrue("WebUI view URL is $url", url?.startsWith("$FAKE_ORIGIN/app") == true)
            assertEquals("Fake LocalAI", title)
        }
    }

    @Test
    fun p2pLibraryLoads() {
        assertTrue("the APK was built without the P2P library (p2pmobile.aar)", BuildConfig.P2P_LIBRARY)
        // The app's own path to the gomobile library; this loads libgojni.so.
        assertEquals("p2pmobile/4", GoP2PEngine().version())
    }

    // ---- helpers ------------------------------------------------------------

    private fun launch() {
        scenario = ActivityScenario.launch(MainActivity::class.java)
    }

    /** The Capacitor WebView that runs the app's own UI (the WebUI view is a plain WebView). */
    private fun shell(): WebInteraction<Void> = onWebView(isAssignableFrom(CapacitorWebView::class.java))

    /**
     * Runs a function body in the app's UI and returns its value.
     *
     * The body goes in as a function definition. Espresso-Web passes a bare body
     * as a string that the page compiles with `new Function`, and the app's CSP
     * (`script-src 'self'`, android/index.html) refuses that as eval. A function
     * definition is inlined into the injected script, so the CSP does not apply.
     */
    private fun js(body: String): Any? = shell().perform(Atoms.script("function() {\n$body\n}")).get().value

    private fun click(testId: String) {
        shell().withElement(findElement(Locator.CSS_SELECTOR, "[data-testid=\"$testId\"]")).perform(webClick())
    }

    /** Waits until the button exists and is enabled, then clicks it. */
    private fun clickWhenEnabled(testId: String, timeoutMs: Long = 20_000) {
        eventually("button $testId enabled", timeoutMs) {
            val ok = js("var b = document.querySelector('[data-testid=\"$testId\"]'); return !!b && !b.disabled;")
            assertEquals("button $testId enabled", true, ok)
        }
        click(testId)
    }

    /** First connect: Add by address, the fake server's address, Test (passes), Connect. */
    private fun addFakeServer() {
        clickWhenEnabled("first-connect-address")
        eventually("the address field") {
            // React keeps the value in state: set it through the native setter and
            // send an input event, as typing would.
            val done = js(
                """
                var i = document.querySelector('input[inputmode="url"]');
                if (!i) return false;
                var set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set;
                set.call(i, '$FAKE_ORIGIN');
                i.dispatchEvent(new Event('input', { bubbles: true }));
                return i.value === '$FAKE_ORIGIN';
                """.trimIndent(),
            )
            assertEquals("address filled", true, done)
        }
        clickWhenEnabled("test-button")
        eventually("a passing test result", timeoutMs = 30_000) {
            val state = js("var r = document.querySelector('[data-testid=\"test-result\"]'); return r ? r.getAttribute('data-state') : null;")
            assertEquals("test result state", "ok", state)
        }
        clickWhenEnabled("form-submit", timeoutMs = 30_000)
    }

    private fun configFile() = File(InstrumentationRegistry.getInstrumentation().targetContext.filesDir, CONFIG_FILE)

    private fun <T> onMain(block: (Activity) -> T): T {
        var result: Result<T>? = null
        scenario!!.onActivity { a -> result = runCatching { block(a) } }
        return result!!.getOrThrow()
    }

    private fun webViews(v: View): List<WebView> = when (v) {
        is WebView -> listOf(v)
        is ViewGroup -> (0 until v.childCount).flatMap { webViews(v.getChildAt(it)) }
        else -> emptyList()
    }

    /** Retries `block` until it passes or `timeoutMs` ends; the UI and the network are asynchronous. */
    private fun <T> eventually(what: String, timeoutMs: Long = 20_000, block: () -> T): T {
        val end = SystemClock.uptimeMillis() + timeoutMs
        while (true) {
            try {
                return block()
            } catch (e: Throwable) {
                if (SystemClock.uptimeMillis() >= end) throw AssertionError("timed out after $timeoutMs ms waiting for $what", e)
            }
            SystemClock.sleep(250)
        }
    }

    private companion object {
        const val FAKE_ORIGIN = "http://127.0.0.1:18080"
        const val CONFIG_FILE = "localai-client.json"
        const val FIRST_CONNECT_TITLE = "Connect to a LocalAI server"
    }
}
