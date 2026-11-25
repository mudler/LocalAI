// SPDX-License-Identifier: MIT
package io.localai.client.webui

import android.graphics.Bitmap
import android.net.Uri
import android.net.http.SslError
import android.os.Message
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

    override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
        val origin = host.serverOrigin ?: return true
        val url = request.url.toString()
        return when (val d = WebuiPolicy.decideNavigation(url, origin, subframe = !request.isForMainFrame)) {
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
        if (request.isForMainFrame) {
            // shouldOverrideUrlLoading already routed link and redirect
            // navigations; form posts do not pass through it, so check again.
            return if (WebuiPolicy.decideNavigation(url, origin, subframe = false) == WebuiPolicy.Navigation.Allow) null else refusedResponse()
        }
        if (!isDocumentRequest(request.requestHeaders ?: emptyMap())) return null
        return if (WebuiPolicy.decideNavigation(url, origin, subframe = true) == WebuiPolicy.Navigation.Allow) null else refusedResponse()
    }

    override fun onPageStarted(view: WebView, url: String?, favicon: Bitmap?) {
        failed = false
        host.pageStarted(view, url)
    }

    override fun onPageFinished(view: WebView, url: String?) {
        if (!failed) host.pageLoaded(view)
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

/** The client of a window catcher: takes the first URL, loads nothing. */
internal class WindowCatcherClient(private val onUrl: (String) -> Unit) : WebViewClient() {
    private var done = false

    override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
        take(view, request.url.toString())
        return true
    }

    override fun shouldInterceptRequest(view: WebView, request: WebResourceRequest): WebResourceResponse = refusedResponse()

    override fun onPageStarted(view: WebView, url: String?, favicon: Bitmap?) {
        // A navigation shouldOverrideUrlLoading did not see (a form post): read
        // its URL and stop; shouldInterceptRequest refuses the request itself.
        view.stopLoading()
        if (url != null && url != "about:blank") take(view, url)
    }

    /** Destroys a catcher that never saw a URL (window.open() with none). */
    fun expire(view: WebView) {
        if (done) return
        done = true
        view.destroy()
    }

    private fun take(view: WebView, url: String) {
        if (done) return
        done = true
        onUrl(url)
        view.post { view.destroy() }
    }
}
