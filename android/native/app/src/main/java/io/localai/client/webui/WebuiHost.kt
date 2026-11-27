// SPDX-License-Identifier: MIT
package io.localai.client.webui

import android.Manifest
import android.annotation.SuppressLint
import android.app.Activity
import android.app.AlertDialog
import android.app.DownloadManager
import android.content.ActivityNotFoundException
import android.content.Context
import android.content.pm.PackageManager
import android.graphics.Color
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import android.util.Log
import android.view.KeyEvent
import android.view.MotionEvent
import android.view.View
import android.view.ViewGroup
import android.webkit.CookieManager
import android.webkit.PermissionRequest
import android.webkit.URLUtil
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewRenderProcess
import android.webkit.WebViewRenderProcessClient
import android.widget.CheckBox
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.result.ActivityResultLauncher
import androidx.activity.result.contract.ActivityResultContracts
import androidx.core.content.ContextCompat
import androidx.core.graphics.Insets
import androidx.core.net.toUri
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat
import androidx.webkit.WebViewCompat
import io.localai.client.R
import io.localai.client.p2p.P2PRuntime
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors

/** What the WebUI view tells the client core (through the WebuiView plugin). */
interface WebuiEvents {
    fun loaded()
    fun loadFailed(code: Int, description: String)
    fun crashed(reason: String)
    fun unresponsive()
    fun responsive()
    fun permissionChoice(origin: String, choice: String, remember: Boolean)
    fun openExternal(url: String)
}

/**
 * The server WebUI in a second, locked native WebView above the Capacitor
 * WebView (the desktop's WebContentsView over the shell). It has no JavaScript
 * interface, no Capacitor bridge and no message listener; it stays on the one
 * server origin, and every decision comes from WebuiPolicy.
 *
 * Every method runs on the UI thread. `shell` is the Capacitor WebView, raised
 * above the WebUI view while a shell popover or dialog is open.
 */
class WebuiHost(
    private val activity: Activity,
    private val container: ViewGroup,
    private val events: WebuiEvents,
    private val shell: View? = null,
) {
    private val permissions = PermissionStoreHolder()
    private val main = Handler(Looper.getMainLooper())
    private val downloads: ExecutorService = Executors.newSingleThreadExecutor()
    private val downloadResolver = DownloadResolver()

    /** The automatic sign-in of a P2P server paired with a sign-in invite. */
    private val deviceSignIn = DeviceSignIn()

    private var webView: WebView? = null

    /** The view was paused when the app left the screen (a P2P server, "keep in background" off). */
    private var pausedForP2P = false

    // A forwarder state changed. P2PRuntime.stop() sends "closed" on the main
    // thread before the service releases the ports, so the check runs
    // synchronously there, before another app can take the address.
    private val p2pListener: (String, String) -> Unit = { _, _ ->
        if (Looper.myLooper() == Looper.getMainLooper()) enforceP2P() else main.post { enforceP2P() }
    }

    init {
        P2PRuntime.addListener(p2pListener)
    }

    /** The origin the view is locked to; read from WebView threads too. */
    @Volatile
    internal var serverOrigin: String? = null
        private set

    /**
     * The OAuth/OIDC sign-in of the view, bound to `serverOrigin`; read from
     * WebView threads too. A new origin gets a new one.
     */
    @Volatile
    internal var oauthFlow: OAuthSignInFlow? = null
        private set

    private fun lockTo(origin: String?) {
        oauthFlow?.reset()
        serverOrigin = origin
        oauthFlow = origin?.let { OAuthSignInFlow(it) }
    }
    private var profile: String? = null
    private var lastUrl: String? = null

    private var visible = false
    private var shellAbove = false
    private var topPx = 0
    private var bottomPx = 0
    private var barHeightPx = 0
    private var shellInFront: Boolean? = null

    @Volatile
    private var lastInputMs = -1L

    private var permissionDialog: AlertDialog? = null
    private var dialogRequest: PermissionRequest? = null
    private var runtimeRequest: Pair<PermissionRequest, List<String>>? = null
    private var fileCallback: ValueCallback<Array<Uri>>? = null

    private val registry = (activity as? ComponentActivity)?.activityResultRegistry

    private val runtimeLauncher: ActivityResultLauncher<Array<String>>? =
        registry?.register("webui-media-permissions", ActivityResultContracts.RequestMultiplePermissions()) { result ->
            val (request, types) = runtimeRequest ?: return@register
            runtimeRequest = null
            grantGranted(request, types) { result[it] == true || isGranted(it) }
        }

    private val fileLauncher: ActivityResultLauncher<android.content.Intent>? =
        registry?.register("webui-file-chooser", ActivityResultContracts.StartActivityForResult()) { result ->
            val cb = fileCallback ?: return@register
            fileCallback = null
            cb.onReceiveValue(WebChromeClient.FileChooserParams.parseResult(result.resultCode, result.data))
        }

    // ---- plugin methods ---------------------------------------------------------

    /**
     * Shows `url` of `origin` in `profile`. A new origin or profile gets a new
     * WebView: a profile can be set only once, before the first load.
     * Throws IllegalArgumentException for a bad origin, URL or profile name.
     */
    fun show(origin: String, url: String, profile: String) {
        Profiles.validate(profile)
        require(WebuiPolicy.httpOrigin(origin) == origin) { "origin is not an http(s) origin" }
        require(WebuiPolicy.httpOrigin(url) == origin) { "url is not on origin" }
        if (p2pBlocked(origin)) {
            // The client core waits for the forwarder; this is the native backstop.
            destroyWebView()
            lockTo(origin)
            this.profile = profile
            lastUrl = url
            events.loadFailed(-1, P2P_NOT_LISTENING)
            return
        }
        val current = webView
        if (current != null && origin == serverOrigin && profile == this.profile) {
            lastUrl = url
            // The app navigates: a sign-in the page started is over.
            oauthFlow?.reset()
            current.loadUrl(url)
            return
        }
        destroyWebView()
        lockTo(origin)
        this.profile = profile
        lastUrl = url
        if (!webViewRecentEnough()) {
            events.loadFailed(-1, "webview-too-old")
            return
        }
        val wv = try {
            createWebView(profile)
        } catch (e: RuntimeException) {
            // No WebView package, or one that fails to start.
            Log.w(TAG, "cannot create the WebUI WebView", e)
            events.loadFailed(-1, "webview-unavailable")
            return
        }
        webView = wv
        applyLayout()
        // A new view is a new open: a refusal of the last one is forgotten.
        deviceSignIn.reset()
        if (!signInOnOpen(wv, origin)) wv.loadUrl(url)
    }

    /** Loads `url` in the view when it is on the server origin; routes it like a link otherwise. */
    fun navigate(url: String) {
        val origin = serverOrigin ?: return
        when (val d = WebuiPolicy.decideNavigation(url, origin, subframe = false)) {
            WebuiPolicy.Navigation.Allow -> {
                if (p2pBlocked(origin)) {
                    suspendForP2P()
                    return
                }
                lastUrl = url
                // The app navigates: a sign-in the page started is over.
                oauthFlow?.reset()
                val wv = webView
                if (wv != null) wv.loadUrl(url) else reload()
            }
            is WebuiPolicy.Navigation.External -> events.openExternal(d.url)
            is WebuiPolicy.Navigation.Deny -> Log.i(TAG, "navigate refused: ${d.reason}")
        }
    }

    fun close() {
        destroyWebView()
        lockTo(null)
        profile = null
        lastUrl = null
    }

    /** Reloads the page, or brings the view back after its renderer was gone. */
    fun reload() {
        if (p2pBlocked(serverOrigin)) {
            suspendForP2P()
            return
        }
        val wv = webView
        if (wv != null) {
            wv.reload()
            return
        }
        val origin = serverOrigin ?: return
        val p = profile ?: return
        val url = lastUrl ?: return
        lockTo(null)
        show(origin, url, p)
    }

    fun setLayout(visible: Boolean, shellAbove: Boolean, topPx: Int, bottomPx: Int, barHeightPx: Int) {
        this.visible = visible
        this.shellAbove = shellAbove
        this.topPx = topPx.coerceAtLeast(0)
        this.bottomPx = bottomPx.coerceAtLeast(0)
        this.barHeightPx = barHeightPx.coerceAtLeast(0)
        applyLayout()
    }

    fun goBack(): Boolean {
        val wv = webView ?: return false
        if (!wv.canGoBack()) return false
        wv.goBack()
        return true
    }

    fun currentUrl(): String? = webView?.url

    fun setPermissionStore(json: String) = permissions.set(json)

    /** The activity is leaving the screen: write sign-in cookies to disk now. */
    fun onPause() {
        webView?.let { Profiles.cookieManager(it).flush() }
        CookieManager.getInstance().flush()
        // Without "keep in background" the forwarder of a P2P server stops
        // after a grace time; the page should not keep working on its own
        // until the app is back and the forwarder listens again.
        val wv = webView ?: return
        if (P2PGuard.listening(serverOrigin, P2PRuntime.forwards, P2PRuntime.lastStates()) != null && !P2PRuntime.keepInBackground()) {
            wv.onPause()
            pausedForP2P = true
        }
    }

    /** The activity is on screen again: resume a paused view only while its forwarder listens. */
    fun onResume() {
        if (!pausedForP2P) return
        pausedForP2P = false
        val wv = webView ?: return
        if (p2pBlocked(serverOrigin)) suspendForP2P() else wv.onResume()
    }

    /** True when `origin` is a P2P server of this app whose forwarder does not listen now. */
    private fun p2pBlocked(origin: String?): Boolean =
        P2PGuard.listening(origin, P2PRuntime.forwards, P2PRuntime.lastStates()) == false

    private fun enforceP2P() {
        if (webView != null && p2pBlocked(serverOrigin)) suspendForP2P()
    }

    /**
     * Removes the page of a P2P server whose forwarder stopped listening, so
     * it sends nothing to an app that may take the address. Origin, profile
     * and URL stay, like after a renderer crash; the client core loads the
     * page again once the forwarder listens.
     */
    private fun suspendForP2P() {
        pausedForP2P = false
        if (webView == null) return
        destroyWebView()
        events.loadFailed(-1, P2P_NOT_LISTENING)
    }

    fun onDestroy() {
        P2PRuntime.removeListener(p2pListener)
        downloads.shutdownNow()
        close()
        runtimeLauncher?.unregister()
        fileLauncher?.unregister()
    }

    // ---- the WebView --------------------------------------------------------------

    private fun webViewRecentEnough(): Boolean {
        val pkg = WebViewCompat.getCurrentWebViewPackage(activity) ?: return false
        val major = pkg.versionName?.substringBefore('.')?.toIntOrNull() ?: return true
        return major >= MIN_CHROMIUM_MAJOR
    }

    @SuppressLint("SetJavaScriptEnabled", "ClickableViewAccessibility")
    private fun createWebView(profile: String): WebView {
        val wv = WebView(activity)
        // Before anything else touches it: the profile is fixed at the first load.
        Profiles.apply(wv, profile)
        lockSettings(wv.settings, javaScript = true)
        wv.settings.domStorageEnabled = true
        wv.settings.setSupportMultipleWindows(true)
        wv.webViewClient = WebuiViewClient(this)
        wv.webChromeClient = WebuiChromeClient(this)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            wv.setWebViewRenderProcessClient(object : WebViewRenderProcessClient() {
                override fun onRenderProcessUnresponsive(view: WebView, renderer: WebViewRenderProcess?) {
                    if (view === webView) events.unresponsive()
                }

                override fun onRenderProcessResponsive(view: WebView, renderer: WebViewRenderProcess?) {
                    if (view === webView) events.responsive()
                }
            })
        }
        wv.setDownloadListener { url, userAgent, contentDisposition, mimeType, _ ->
            download(wv, url, userAgent, contentDisposition, mimeType)
        }
        // A touch or a key press counts as the user action a permission
        // request needs. The listeners never consume the event.
        wv.setOnTouchListener { _, e ->
            if (e.actionMasked == MotionEvent.ACTION_DOWN || e.actionMasked == MotionEvent.ACTION_UP) {
                lastInputMs = SystemClock.uptimeMillis()
            }
            false
        }
        wv.setOnKeyListener { _, _, e ->
            if (e.action == KeyEvent.ACTION_DOWN) lastInputMs = SystemClock.uptimeMillis()
            false
        }
        // The margins of applyLayout keep the view clear of the system bars
        // and the keyboard, so the page must not clear them a second time:
        // a WebView of M139 or later shrinks its visual viewport by the
        // keyboard inset it receives, and later ones also report the bars as
        // safe areas. The view gets these insets as zero (not consumed, so it
        // still sees every change).
        ViewCompat.setOnApplyWindowInsetsListener(wv) { v, insets ->
            applyLayout(insets)
            val cleared = WindowInsetsCompat.Builder(insets)
                .setInsets(WindowInsetsCompat.Type.ime() or WindowInsetsCompat.Type.systemBars() or WindowInsetsCompat.Type.displayCutout(), Insets.NONE)
                .build()
            ViewCompat.onApplyWindowInsets(v, cleared)
        }
        container.addView(wv, ViewGroup.MarginLayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
        shellInFront = null
        return wv
    }

    private fun lockSettings(s: WebSettings, javaScript: Boolean) {
        s.javaScriptEnabled = javaScript
        s.javaScriptCanOpenWindowsAutomatically = false
        s.allowFileAccess = false
        s.allowContentAccess = false
        @Suppress("DEPRECATION")
        s.allowFileAccessFromFileURLs = false
        @Suppress("DEPRECATION")
        s.allowUniversalAccessFromFileURLs = false
        s.mixedContentMode = WebSettings.MIXED_CONTENT_NEVER_ALLOW
        s.safeBrowsingEnabled = true
        s.setGeolocationEnabled(false)
        s.mediaPlaybackRequiresUserGesture = true
    }

    private fun destroyWebView() {
        dismissPermissionPrompt()
        runtimeRequest?.first?.deny()
        runtimeRequest = null
        fileCallback?.onReceiveValue(null)
        fileCallback = null
        // A sign-in never outlives its view.
        oauthFlow?.reset()
        val wv = webView ?: return
        webView = null
        Profiles.cookieManager(wv).flush()
        wv.stopLoading()
        container.removeView(wv)
        wv.destroy()
    }

    /**
     * Places the view: below the shell's top strip and the status bar, above
     * the shell's bottom strip, the bar and the navigation bar. While the
     * keyboard is open the shell hides the bar, so the view ends at the
     * keyboard and keeps no room for the bar. The container may already end
     * at the keyboard (see WebuiLayout); then the keyboard adds no margin.
     */
    private fun applyLayout(given: WindowInsetsCompat? = null) {
        val wv = webView ?: return
        wv.visibility = if (visible) View.VISIBLE else View.GONE
        val shellView = shell
        val wantShellInFront = shellAbove && shellView != null
        if (shellInFront != wantShellInFront) {
            shellInFront = wantShellInFront
            if (shellView != null && wantShellInFront) {
                // The shell draws its scrim over the WebUI, so it must be see-through.
                shellView.setBackgroundColor(Color.TRANSPARENT)
                shellView.bringToFront()
            } else {
                wv.bringToFront()
            }
        }
        val insets = given ?: ViewCompat.getRootWindowInsets(container)
        val statusTop = insets?.getInsets(WindowInsetsCompat.Type.statusBars() or WindowInsetsCompat.Type.displayCutout())?.top ?: 0
        val navBottom = insets?.getInsets(WindowInsetsCompat.Type.navigationBars())?.bottom ?: 0
        val imeOpen = insets?.isVisible(WindowInsetsCompat.Type.ime()) == true
        val imeBottom = if (imeOpen) insets?.getInsets(WindowInsetsCompat.Type.ime())?.bottom ?: 0 else 0
        val top = topPx + statusTop
        val bottom = WebuiLayout.bottomMargin(imeBottom, clearedBelowContainer(), bottomPx, barHeightPx, navBottom)
        val lp = wv.layoutParams as? ViewGroup.MarginLayoutParams ?: return
        if (lp.topMargin != top || lp.bottomMargin != bottom) {
            lp.topMargin = top
            lp.bottomMargin = bottom
            wv.layoutParams = lp
        }
    }

    /**
     * The part of the window below the container that is already kept clear.
     * Capacitor's SystemBars plugin pads the root view by the keyboard height
     * in the same insets pass that reaches the view, before the container is
     * laid out again, so the padding is read too, not only the position.
     */
    private fun clearedBelowContainer(): Int {
        val root = container.rootView
        val measured = if (container.isLaidOut && root.height > 0) {
            val at = IntArray(2)
            container.getLocationInWindow(at)
            root.height - (at[1] + container.height)
        } else {
            0
        }
        return maxOf(root.paddingBottom, measured)
    }

    // ---- callbacks from WebuiClients ------------------------------------------------

    internal fun openExternal(url: String) = events.openExternal(url)

    internal fun pageStarted(view: WebView, url: String?) {
        if (view !== webView || url == null) return
        // The session route takes a post only: a reload must not GET it.
        if (WebuiPolicy.httpOrigin(url) == serverOrigin && DeviceSignIn.pathOf(url) != DeviceSignIn.SESSION_PATH) lastUrl = url
    }

    /** The main frame committed `url` (a load or a history API change). */
    internal fun urlChanged(view: WebView, url: String?) {
        if (view !== webView || url == null) return
        val origin = serverOrigin ?: return
        if (WebuiPolicy.httpOrigin(url) != origin || p2pBlocked(origin)) return
        val peer = P2PGuard.forwardOf(origin, P2PRuntime.forwards)?.peer ?: return
        val store = P2PRuntime.credentials ?: return
        when (deviceSignIn.onUrl(url, store.has(peer), SystemClock.elapsedRealtime(), store.signedOut(peer))) {
            DeviceSignIn.Action.NONE -> Unit
            // Not from inside the callback: the navigation that called it is
            // still being committed. Later, not at once: a sign-out request
            // sent with this landing may still be on its way (see DeviceSignIn).
            DeviceSignIn.Action.POST -> main.postDelayed({
                if (view !== webView || serverOrigin != origin) return@postDelayed
                val onSignIn = DeviceSignIn.pathOf(view.url ?: "")?.let { DeviceSignIn.isSignInPath(it) } == true
                if (deviceSignIn.onPostDue(store.signedOut(peer), onSignIn)) postCredential(view, origin, peer)
            }, DeviceSignIn.SIGN_OUT_GRACE_MS)
            DeviceSignIn.Action.LEAVE -> main.post {
                if (view === webView && serverOrigin == origin) view.loadUrl(origin + DeviceSignIn.SIGN_IN_PATH)
            }
        }
    }

    /**
     * The page sent its sign-out request (seen by shouldInterceptRequest on a
     * WebView thread). The flag is set on the UI thread, before the landing on
     * the sign-in page that follows the answer, and before a post that
     * [urlChanged] put off ([DeviceSignIn.SIGN_OUT_GRACE_MS]) is due.
     */
    internal fun signOutRequested(view: WebView, origin: String) {
        main.post {
            if (view !== webView || serverOrigin != origin) return@post
            val peer = P2PGuard.forwardOf(origin, P2PRuntime.forwards)?.peer ?: return@post
            val store = P2PRuntime.credentials ?: return@post
            // A server without a credential has no automatic sign-in to stop.
            if (store.has(peer)) store.signOut(peer)
        }
    }

    /**
     * The first open of a P2P server after a pairing that gave a device
     * credential: posts it, so the view starts signed in. True when it did;
     * the server's answer redirects to `/`.
     */
    private fun signInOnOpen(wv: WebView, origin: String): Boolean {
        val peer = P2PGuard.forwardOf(origin, P2PRuntime.forwards)?.peer ?: return false
        val store = P2PRuntime.credentials ?: return false
        if (!deviceSignIn.onOpen(store.takeFresh(peer), store.has(peer), SystemClock.elapsedRealtime(), store.signedOut(peer))) return false
        return postCredential(wv, origin, peer)
    }

    /** Posts the credential of `peer` to the session route of `origin`. The cookie lands in the view's profile. */
    private fun postCredential(wv: WebView, origin: String, peer: String): Boolean {
        val credential = P2PRuntime.credentials?.load(peer) ?: return false
        oauthFlow?.reset()
        wv.postUrl(origin + DeviceSignIn.SESSION_PATH, DeviceSignIn.body(credential))
        return true
    }

    internal fun pageLoaded(view: WebView, url: String?) {
        if (view !== webView) return
        oauthFlow?.onLoad(url ?: view.url ?: "", SystemClock.elapsedRealtime())
        events.loaded()
    }

    internal fun pageFailed(view: WebView, code: Int, description: String) {
        if (view !== webView) return
        oauthFlow?.onLoadFailed(SystemClock.elapsedRealtime())
        events.loadFailed(code, description)
    }

    internal fun renderProcessGone(view: WebView, reason: String) {
        if (view !== webView) {
            container.removeView(view)
            view.destroy()
            return
        }
        // Keep origin, profile and URL: reload() brings the view back.
        dismissPermissionPrompt()
        oauthFlow?.reset()
        webView = null
        container.removeView(view)
        view.destroy()
        events.crashed(reason)
    }

    /** A hidden WebView that only reads the URL of a window the page asked for. */
    @SuppressLint("ClickableViewAccessibility")
    internal fun newWindowCatcher(): WebView? {
        val p = profile ?: return null
        return try {
            val catcher = WebView(activity)
            // A popup must live in the profile of its opener.
            Profiles.apply(catcher, p)
            lockSettings(catcher.settings, javaScript = false)
            // The main looper, not catcher.post: a view never attached to a
            // window keeps its posted runnables until it is attached, so they
            // would never run and every catcher would leak.
            val once = CatcherOnce(
                onUrl = { url -> routeWindowOpen(url) },
                release = { main.post { catcher.destroy() } },
            )
            catcher.webViewClient = WindowCatcherClient(once)
            main.postDelayed({ once.expire() }, CATCHER_TIMEOUT_MS)
            catcher
        } catch (e: RuntimeException) {
            Log.w(TAG, "cannot create a window catcher", e)
            null
        }
    }

    private fun routeWindowOpen(url: String) {
        val origin = serverOrigin ?: return
        when (val d = WebuiPolicy.decideWindowOpen(url, origin)) {
            is WebuiPolicy.WindowOpen.SameView -> navigate(d.url)
            is WebuiPolicy.WindowOpen.External -> events.openExternal(d.url)
            is WebuiPolicy.WindowOpen.None -> Log.i(TAG, "window refused: ${d.reason}")
        }
    }

    // ---- permissions -------------------------------------------------------------

    private fun hasRecentGesture(): Boolean {
        val last = lastInputMs
        if (last < 0) return false
        val now = SystemClock.uptimeMillis()
        return now >= last && now - last <= WebuiPolicy.GESTURE_WINDOW_MS
    }

    internal fun permissionRequested(request: PermissionRequest) {
        val origin = serverOrigin
        if (origin == null) {
            request.deny()
            return
        }
        val types = ArrayList<String>()
        for (r in request.resources) {
            when (r) {
                PermissionRequest.RESOURCE_AUDIO_CAPTURE -> types.add("audio")
                PermissionRequest.RESOURCE_VIDEO_CAPTURE -> types.add("video")
                else -> {
                    request.deny()
                    return
                }
            }
        }
        if (types.isEmpty()) {
            request.deny()
            return
        }
        val decision = WebuiPolicy.decidePermission(
            WebuiPolicy.PermissionRequest(
                permission = "media",
                requestingUrl = request.origin?.toString() ?: "",
                topUrl = webView?.url ?: "",
                mediaTypes = types,
                userGesture = hasRecentGesture(),
            ),
            origin,
            permissions.asMap(),
        )
        when (decision) {
            WebuiPolicy.Permission.DENY -> request.deny()
            WebuiPolicy.Permission.ALLOW -> grantWithAndroidPermission(request, types)
            WebuiPolicy.Permission.ASK -> ask(request, origin, types)
        }
    }

    internal fun permissionCanceled(request: PermissionRequest) {
        if (request === dialogRequest) {
            dialogRequest = null
            permissionDialog?.dismiss()
            permissionDialog = null
        }
        if (request === runtimeRequest?.first) runtimeRequest = null
    }

    private fun ask(request: PermissionRequest, origin: String, types: List<String>) {
        dismissPermissionPrompt()
        val message = when {
            types.contains("audio") && types.contains("video") -> R.string.webui_permission_microphone_camera
            types.contains("video") -> R.string.webui_permission_camera
            else -> R.string.webui_permission_microphone
        }
        val remember = CheckBox(activity).apply { setText(R.string.webui_permission_remember) }
        val pad = (20 * activity.resources.displayMetrics.density).toInt()
        val box = android.widget.FrameLayout(activity).apply {
            setPadding(pad, pad / 2, pad, 0)
            addView(remember)
        }
        dialogRequest = request
        permissionDialog = AlertDialog.Builder(activity)
            .setTitle(R.string.webui_permission_title)
            .setMessage(activity.getString(message, origin))
            .setView(box)
            .setPositiveButton(R.string.webui_permission_allow) { _, _ ->
                if (!answerStillValid(request, origin)) return@setPositiveButton
                events.permissionChoice(origin, "allow", remember.isChecked)
                grantWithAndroidPermission(request, types)
            }
            .setNegativeButton(R.string.webui_permission_deny) { _, _ ->
                if (!answerStillValid(request, origin)) return@setNegativeButton
                events.permissionChoice(origin, "deny", remember.isChecked)
                request.deny()
            }
            .setOnCancelListener {
                // Back or a tap outside: refuse this time, remember nothing.
                if (answerStillValid(request, origin)) request.deny()
            }
            .create()
        permissionDialog?.show()
    }

    private fun answerStillValid(request: PermissionRequest, origin: String): Boolean {
        if (request !== dialogRequest) return false
        dialogRequest = null
        permissionDialog = null
        if (serverOrigin != origin || webView == null) {
            request.deny()
            return false
        }
        return true
    }

    private fun dismissPermissionPrompt() {
        val pending = dialogRequest
        dialogRequest = null
        permissionDialog?.dismiss()
        permissionDialog = null
        pending?.deny()
    }

    private fun androidPermissionOf(type: String) = if (type == "video") Manifest.permission.CAMERA else Manifest.permission.RECORD_AUDIO

    private fun isGranted(permission: String) =
        ContextCompat.checkSelfPermission(activity, permission) == PackageManager.PERMISSION_GRANTED

    /** Grants the page only what Android granted the app, asking Android first when needed. */
    private fun grantWithAndroidPermission(request: PermissionRequest, types: List<String>) {
        val missing = types.map { androidPermissionOf(it) }.filterNot { isGranted(it) }
        if (missing.isEmpty()) {
            grantGranted(request, types) { true }
            return
        }
        val launcher = runtimeLauncher
        if (launcher == null || runtimeRequest != null) {
            grantGranted(request, types) { isGranted(it) }
            return
        }
        runtimeRequest = request to types
        try {
            launcher.launch(missing.distinct().toTypedArray())
        } catch (e: ActivityNotFoundException) {
            runtimeRequest = null
            grantGranted(request, types) { isGranted(it) }
        }
    }

    private fun grantGranted(request: PermissionRequest, types: List<String>, granted: (String) -> Boolean) {
        val resources = types.filter { granted(androidPermissionOf(it)) }.map {
            if (it == "video") PermissionRequest.RESOURCE_VIDEO_CAPTURE else PermissionRequest.RESOURCE_AUDIO_CAPTURE
        }
        if (resources.isEmpty()) request.deny() else request.grant(resources.toTypedArray())
    }

    // ---- files -------------------------------------------------------------------

    internal fun showFileChooser(callback: ValueCallback<Array<Uri>>, params: WebChromeClient.FileChooserParams): Boolean {
        val launcher = fileLauncher ?: return false
        fileCallback?.onReceiveValue(null)
        fileCallback = callback
        try {
            launcher.launch(params.createIntent())
        } catch (e: ActivityNotFoundException) {
            fileCallback = null
            callback.onReceiveValue(null)
        }
        return true
    }

    private fun download(wv: WebView, url: String, userAgent: String?, contentDisposition: String?, mimeType: String?) {
        val scheme = url.toUri().scheme?.lowercase()
        if (scheme == "blob" || scheme == "data") {
            // The data lives inside the page; v1 cannot hand it to the system.
            toast(R.string.webui_download_unsupported)
            return
        }
        if ((scheme != "http" && scheme != "https") || WebuiPolicy.httpOrigin(url) == null) return
        val name = WebuiPolicy.sanitizeFileName(URLUtil.guessFileName(url, contentDisposition, mimeType))
        val origin = serverOrigin
        // The sign-in cookie goes only to the server itself.
        val cookie = if (origin != null && WebuiPolicy.httpOrigin(url) == origin) Profiles.cookieManager(wv).getCookie(url) else null
        if (origin == null || cookie == null) {
            enqueue(url, null, name, userAgent, mimeType)
            return
        }
        // Redirects are followed first, off the UI thread, so the cookie never
        // reaches another host through DownloadManager's own redirects.
        downloads.execute {
            val t = downloadResolver.resolve(url, origin, cookie, userAgent)
            main.post { enqueue(t.url, t.cookie, name, userAgent, mimeType) }
        }
    }

    private fun enqueue(url: String, cookie: String?, name: String, userAgent: String?, mimeType: String?) {
        val scheme = url.toUri().scheme?.lowercase()
        if (scheme != "http" && scheme != "https") {
            toast(R.string.webui_download_failed)
            return
        }
        try {
            val req = DownloadManager.Request(url.toUri())
            cookie?.let { req.addRequestHeader("Cookie", it) }
            if (!userAgent.isNullOrEmpty()) req.addRequestHeader("User-Agent", userAgent)
            if (!mimeType.isNullOrEmpty()) req.setMimeType(mimeType)
            req.setTitle(name)
            req.setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                req.setDestinationInExternalPublicDir(Environment.DIRECTORY_DOWNLOADS, name)
            } else {
                // Before Android 10 the shared Downloads folder needs a storage
                // permission the app does not ask for; the app's own folder does not.
                req.setDestinationInExternalFilesDir(activity, Environment.DIRECTORY_DOWNLOADS, name)
            }
            val dm = activity.getSystemService(Context.DOWNLOAD_SERVICE) as DownloadManager
            dm.enqueue(req)
            Toast.makeText(activity, activity.getString(R.string.webui_download_started, name), Toast.LENGTH_SHORT).show()
        } catch (e: RuntimeException) {
            // SecurityException, IllegalStateException (no storage), IllegalArgumentException.
            Log.w(TAG, "download refused", e)
            toast(R.string.webui_download_failed)
        }
    }

    private fun toast(res: Int) = Toast.makeText(activity, res, Toast.LENGTH_SHORT).show()

    private companion object {
        const val TAG = "WebuiHost"

        /** The oldest WebView the WebUI is checked against. */
        const val MIN_CHROMIUM_MAJOR = 111

        const val CATCHER_TIMEOUT_MS = 10_000L

        /** The load failure the view reports when it refuses a P2P origin whose forwarder does not listen. */
        const val P2P_NOT_LISTENING = "p2p-not-listening"
    }
}
