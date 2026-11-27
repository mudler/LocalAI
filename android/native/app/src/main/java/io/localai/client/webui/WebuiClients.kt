// SPDX-License-Identifier: MIT
package io.localai.client.webui

import android.graphics.Bitmap
import android.net.Uri
import android.net.http.SslError
import android.os.Message
import android.os.SystemClock
import android.webkit.ClientCertRequest
import android.webkit.GeolocationPermissions
import android.webkit.PermissionRequest
import android.webkit.RenderProcessGoneDetail
import android.webkit.SslErrorHandler
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebView
import android.webkit.WebViewClient
import java.io.ByteArrayInputStream

/** An empty 403 answer: what a refused request gets instead of the network. */
internal fun refusedResponse(): WebResourceResponse =
    WebResourceResponse("text/plain", "utf-8", 403, "Forbidden", emptyMap(), ByteArrayInputStream(ByteArray(0)))

/**
 * True when a request loads a document into a frame. WebView does not say so
 * directly; Sec-Fetch-Dest does when the WebView passes it on, and a frame
 * load otherwise asks for HTML first.
 */
internal fun isDocumentRequest(headers: Map<String, String>): Boolean {
    var dest: String? = null
    var accept: String? = null
    for ((k, v) in headers) {
        when {
            k.equals("Sec-Fetch-Dest", ignoreCase = true) -> dest = v.trim().lowercase()
            k.equals("Accept", ignoreCase = true) -> accept = v.lowercase()
        }
    }
    if (dest != null) return dest == "iframe" || dest == "frame" || dest == "document" || dest == "embed" || dest == "object"
    return accept?.contains("text/html") == true
}

/** Navigation rules, load events and error handling of the WebUI WebView. */
internal class WebuiViewClient(private val host: WebuiHost) : WebViewClient() {
    // One load-failed event per page load.
    private var failed = false

    // Server redirects come here too, with isRedirect set (Android 7 and later):
    // the redirect of a login route to the identity provider is what starts a
    // sign-in (OAuthSignInFlow), so it must be told apart from a link.
    override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
        val origin = host.serverOrigin ?: return true
        val url = request.url.toString()
        val subframe = !request.isForMainFrame
        val flow = host.oauthFlow
        val now = SystemClock.elapsedRealtime()
        val d = when {
            flow == null -> WebuiPolicy.decideNavigation(url, origin, subframe)
            request.isRedirect -> flow.onRedirect(url, subframe, now)
            else -> flow.onNavigate(url, subframe, now)
        }
        return when (d) {
            WebuiPolicy.Navigation.Allow -> false
            is WebuiPolicy.Navigation.External -> {
                host.openExternal(d.url)
                true
            }
            is WebuiPolicy.Navigation.Deny -> true
        }
    }

    // Runs on a WebView thread, not the UI thread.
    override fun shouldInterceptRequest(view: WebView, request: WebResourceRequest): WebResourceResponse? {
        val origin = host.serverOrigin ?: return refusedResponse()
        val url = request.url.toString()
        // The WebUI signs out with a fetch POST, then moves to its sign-in
        // page: the one sign of an explicit sign-out the app can see without
        // a JavaScript interface. The request goes on unchanged.
        if (DeviceSignIn.isSignOutRequest(request.method, url, origin)) host.signOutRequested(view, origin)
        if (request.isForMainFrame) {
            // shouldOverrideUrlLoading already routed link and redirect
            // navigations; form posts do not pass through it, so check again.
            // An active sign-in lets its identity provider origins through
            // (the sign-in form of the provider is a post), and adds none.
            val flow = host.oauthFlow
            val d = flow?.onBeforeRequest(url, subframe = false, nowMs = SystemClock.elapsedRealtime())
                ?: WebuiPolicy.decideNavigation(url, origin, subframe = false)
            return if (d == WebuiPolicy.Navigation.Allow) null else refusedResponse()
        }
        if (!isDocumentRequest(request.requestHeaders ?: emptyMap())) return null
        return if (WebuiPolicy.decideNavigation(url, origin, subframe = true) == WebuiPolicy.Navigation.Allow) null else refusedResponse()
    }

    override fun onPageStarted(view: WebView, url: String?, favicon: Bitmap?) {
        failed = false
        host.pageStarted(view, url)
    }

    override fun onPageFinished(view: WebView, url: String?) {
        if (!failed) host.pageLoaded(view, url)
    }

    // Once per main-frame commit: page loads and history API changes alike,
    // so the WebUI's client-side move to its sign-in page is seen too.
    override fun doUpdateVisitedHistory(view: WebView, url: String?, isReload: Boolean) {
        host.urlChanged(view, url)
    }

    override fun onReceivedError(view: WebView, request: WebResourceRequest, error: WebResourceError) {
        if (!request.isForMainFrame || failed) return
        failed = true
        host.pageFailed(view, error.errorCode, error.description?.toString() ?: "")
    }

    override fun onReceivedSslError(view: WebView, handler: SslErrorHandler, error: SslError) {
        // Never bypassed, never "continue anyway".
        handler.cancel()
        if (failed) return
        if (WebuiPolicy.httpOrigin(error.url ?: "") != host.serverOrigin) return
        failed = true
        host.pageFailed(view, ERROR_FAILED_SSL_HANDSHAKE, "certificate error ${error.primaryError}")
    }

    override fun onReceivedClientCertRequest(view: WebView, request: ClientCertRequest) {
        request.cancel()
    }

    override fun onRenderProcessGone(view: WebView, detail: RenderProcessGoneDetail): Boolean {
        host.renderProcessGone(view, if (detail.didCrash()) "crashed" else "killed")
        // Handled: the WebView is destroyed and the app keeps running.
        return true
    }
}

/** Windows, permissions and file pickers of the WebUI WebView. */
internal class WebuiChromeClient(private val host: WebuiHost) : WebChromeClient() {
    /**
     * window.open and target=_blank. WebView does not tell the target URL, so
     * a catcher WebView that is never shown, runs no script and loads nothing
     * receives the first navigation, hands its URL to decideWindowOpen and is
     * destroyed. No window is ever created for the page.
     */
    override fun onCreateWindow(view: WebView, isDialog: Boolean, isUserGesture: Boolean, resultMsg: Message): Boolean {
        val catcher = host.newWindowCatcher() ?: return false
        val transport = resultMsg.obj as? WebView.WebViewTransport ?: run {
            catcher.destroy()
            return false
        }
        transport.webView = catcher
        resultMsg.sendToTarget()
        return true
    }

    override fun onPermissionRequest(request: PermissionRequest) {
        host.permissionRequested(request)
    }

    override fun onPermissionRequestCanceled(request: PermissionRequest) {
        host.permissionCanceled(request)
    }

    override fun onGeolocationPermissionsShowPrompt(origin: String?, callback: GeolocationPermissions.Callback) {
        callback.invoke(origin, false, false)
    }

    override fun onShowFileChooser(view: WebView, callback: ValueCallback<Array<Uri>>, params: FileChooserParams): Boolean =
        host.showFileChooser(callback, params)
}

/**
 * The life of one window catcher: the first URL goes to `onUrl`, and the
 * catcher is released exactly once, after its URL or when it expires.
 * `release` must not use View.post: the catcher is never attached to a
 * window, so a runnable posted on it would never run.
 */
internal class CatcherOnce(private val onUrl: (String) -> Unit, private val release: () -> Unit) {
    private var done = false

    fun take(url: String) {
        if (done) return
        done = true
        onUrl(url)
        release()
    }

    /** A catcher that never saw a URL (window.open() with none). */
    fun expire() {
        if (done) return
        done = true
        release()
    }
}

/** The client of a window catcher: takes the first URL, loads nothing. */
internal class WindowCatcherClient(private val once: CatcherOnce) : WebViewClient() {
    override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
        once.take(request.url.toString())
        return true
    }

    override fun shouldInterceptRequest(view: WebView, request: WebResourceRequest): WebResourceResponse = refusedResponse()

    override fun onPageStarted(view: WebView, url: String?, favicon: Bitmap?) {
        // A navigation shouldOverrideUrlLoading did not see (a form post): read
        // its URL and stop; shouldInterceptRequest refuses the request itself.
        view.stopLoading()
        if (url != null && url != "about:blank") once.take(url)
    }
}
