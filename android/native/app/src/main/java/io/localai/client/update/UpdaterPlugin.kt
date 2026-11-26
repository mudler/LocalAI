// SPDX-License-Identifier: MIT
package io.localai.client.update

import android.annotation.SuppressLint
import android.content.Intent
import android.content.pm.PackageInfo
import android.content.pm.PackageManager
import android.os.Build
import android.provider.Settings
import android.util.Log
import androidx.core.net.toUri
import com.getcapacitor.JSObject
import com.getcapacitor.Plugin
import com.getcapacitor.PluginCall
import com.getcapacitor.PluginMethod
import com.getcapacitor.annotation.CapacitorPlugin
import io.localai.client.BuildConfig
import okhttp3.OkHttpClient
import java.io.File
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

/**
 * Self-update from the release feed (contract: UpdaterPlugin in src/plugins/definitions.ts).
 *
 * The web side decides whether an update is offered (manifest checks); this side
 * only fetches, verifies and hands the file to the system installer. It never
 * installs by itself: install() runs only when the web side calls it, which it
 * does from the user's tap.
 */
@CapacitorPlugin(name = "Updater")
class UpdaterPlugin : Plugin() {
    // One thread: downloads are long and must not block Capacitor's shared plugin
    // thread, and running fetch, download, verify and install in order keeps two of
    // them from touching the update file at once.
    private val worker: ExecutorService = Executors.newSingleThreadExecutor()
    private lateinit var downloader: ApkDownloader
    private lateinit var gate: UpdateGate
    private val statusListener: (String, String?) -> Unit = { status, message ->
        val event = JSObject().put("status", status)
        if (message != null) event.put("message", message)
        notifyListeners("status", event)
    }

    override fun load() {
        val client = OkHttpClient.Builder()
            .connectTimeout(15, TimeUnit.SECONDS)
            .readTimeout(30, TimeUnit.SECONDS)
            .build()
        downloader = ApkDownloader(client, File(context.cacheDir, "updates"), allowHttp = feedIsHttp())
        gate = UpdateGate(
            downloader = downloader,
            ownId = context.packageName,
            installedSigners = { signersOf(installedInfo()) },
            readArchive = { file -> archiveInfo(file)?.let { Candidate(it.packageName.orEmpty(), signersOf(it)) } },
            installer = { file -> Installer(context).install(file) },
        )
        InstallStatusReceiver.listener = statusListener
    }

    override fun handleOnDestroy() {
        if (InstallStatusReceiver.listener === statusListener) InstallStatusReceiver.listener = null
        worker.shutdownNow()
    }

    @PluginMethod
    fun fetchText(call: PluginCall) {
        val url = call.getString("url")
        val maxBytes = call.getInt("maxBytes") ?: 0
        if (url == null || !ApkDownloader.underFeed(url, BuildConfig.UPDATE_FEED_BASE)) return call.reject("not a feed URL", "url")
        if (maxBytes <= 0) return call.reject("maxBytes must be positive", "size")
        background(call) {
            val (status, text) = downloader.fetchText(url, maxBytes)
            call.resolve(JSObject().put("status", status).put("text", text))
        }
    }

    @PluginMethod
    fun download(call: PluginCall) {
        val url = call.getString("url")
        val sha256 = call.getString("sha256") ?: ""
        val size = call.data.optLong("size", -1)
        if (url == null || !ApkDownloader.underFeed(url, BuildConfig.UPDATE_FEED_BASE)) return call.reject("not a feed URL", "url")
        background(call) {
            var lastPercent = -1L
            val file = downloader.download(url, sha256, size) { done, total ->
                val percent = done * 100 / total
                if (percent != lastPercent) {
                    lastPercent = percent
                    notifyListeners("progress", JSObject().put("done", done).put("total", total))
                }
            }
            call.resolve(JSObject().put("file", file.absolutePath))
        }
    }

    @PluginMethod
    fun verifySigner(call: PluginCall) {
        val path = call.getString("file") ?: return call.reject("no file", "file")
        background(call) {
            when (val outcome = gate.verify(path)) {
                UpdateGate.Outcome.Done -> call.resolve(JSObject().put("sameSigner", true))
                is UpdateGate.Outcome.Refused ->
                    if (outcome.code == "signer") {
                        call.resolve(JSObject().put("sameSigner", false))
                    } else {
                        call.reject("not the downloaded update", outcome.code)
                    }
            }
        }
    }

    @PluginMethod
    fun canInstall(call: PluginCall) {
        call.resolve(JSObject().put("allowed", context.packageManager.canRequestPackageInstalls()))
    }

    @PluginMethod
    fun openInstallSettings(call: PluginCall) {
        val intent = Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, "package:${context.packageName}".toUri())
        try {
            (activity ?: context).startActivity(
                if (activity == null) intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK) else intent,
            )
            call.resolve()
        } catch (e: Exception) {
            call.reject("cannot open the install settings", "settings", e)
        }
    }

    @PluginMethod
    fun install(call: PluginCall) {
        val path = call.getString("file") ?: return call.reject("no file", "file")
        if (!context.packageManager.canRequestPackageInstalls()) {
            return call.reject("installing updates is not allowed for this app", "not-allowed")
        }
        background(call) {
            // The gate checks the signer again, so no path, however the web side got
            // to it, installs a file signed by someone else.
            when (val outcome = gate.install(path)) {
                UpdateGate.Outcome.Done -> call.resolve()
                is UpdateGate.Outcome.Refused -> call.reject(
                    if (outcome.code == "signer") "signed by another developer" else "not the downloaded update",
                    outcome.code,
                )
            }
        }
    }

    @PluginMethod
    fun appInfo(call: PluginCall) {
        call.resolve(
            JSObject()
                .put("applicationId", context.packageName)
                .put("versionName", BuildConfig.VERSION_NAME)
                .put("versionCode", BuildConfig.VERSION_CODE)
                .put("sdkInt", Build.VERSION.SDK_INT)
                .put("feedBase", BuildConfig.UPDATE_FEED_BASE),
        )
    }

    private fun background(call: PluginCall, block: () -> Unit) {
        try {
            worker.execute {
                try {
                    block()
                } catch (e: DownloadRefused) {
                    call.reject(e.reason, e.reason)
                } catch (e: Exception) {
                    Log.w(TAG, "updater call failed", e)
                    call.reject(e.message ?: e.javaClass.simpleName, "io", e)
                }
            }
        } catch (e: Exception) {
            call.reject("the updater is stopped", "io", e)
        }
    }

    private fun installedInfo(): PackageInfo? = try {
        val pm = context.packageManager
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            pm.getPackageInfo(context.packageName, PackageManager.PackageInfoFlags.of(SIGNING_FLAGS.toLong()))
        } else {
            @Suppress("DEPRECATION")
            pm.getPackageInfo(context.packageName, SIGNING_FLAGS)
        }
    } catch (e: PackageManager.NameNotFoundException) {
        null
    }

    private fun archiveInfo(file: File): PackageInfo? {
        val pm = context.packageManager
        return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            pm.getPackageArchiveInfo(file.absolutePath, PackageManager.PackageInfoFlags.of(SIGNING_FLAGS.toLong()))
        } else {
            pm.getPackageArchiveInfo(file.absolutePath, SIGNING_FLAGS)
        }
    }

    // Before API 28 only the GET_SIGNATURES list exists. The whole list is
    // compared, not its first entry, which is what this lint check warns about.
    @SuppressLint("PackageManagerGetSignatures")
    private fun signersOf(info: PackageInfo?): Set<String> {
        if (info == null) return emptySet()
        val certs = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
            val signing = info.signingInfo ?: return emptySet()
            if (signing.hasMultipleSigners()) signing.apkContentsSigners else signing.signingCertificateHistory
        } else {
            @Suppress("DEPRECATION")
            info.signatures
        }
        return certs.orEmpty().map { SignerCheck.fingerprint(it.toByteArray()) }.toSet()
    }

    private fun feedIsHttp() = BuildConfig.UPDATE_FEED_BASE.startsWith("http://")

    private companion object {
        const val TAG = "Updater"

        @Suppress("DEPRECATION")
        @SuppressLint("PackageManagerGetSignatures")
        val SIGNING_FLAGS: Int =
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) PackageManager.GET_SIGNING_CERTIFICATES else PackageManager.GET_SIGNATURES
    }
}
