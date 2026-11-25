// SPDX-License-Identifier: MIT
package io.localai.client.p2p

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import android.util.Log

/**
 * Decides when a change of the default network must restart the forwarders.
 * The library cannot follow a network change by itself on Android (its
 * address watcher cannot read the interfaces, and the server peer is never
 * redialled), so the app stops and starts every forwarder instead.
 *
 * The first report after registration is the current network and is only
 * remembered. Then a new network id, a network that comes back after a loss,
 * or a change of the transport set schedules one restart [debounceMs] later;
 * a later change within that window moves it. A loss alone schedules nothing.
 */
class NetworkPolicy(private val debounceMs: Long = 1000) {
    private var seen = false
    private var lastId: Long? = null
    private var lastTransports: Set<String> = emptySet()
    private var lost = false
    private var restartAt: Long? = null

    /** [networkId] null means the default network was lost. Returns the time to restart at, or null for no change. */
    fun onDefaultNetwork(networkId: Long?, transports: Set<String>, nowMs: Long): Long? {
        if (!seen) {
            seen = true
            lastId = networkId
            lastTransports = transports
            lost = networkId == null
            return null
        }
        if (networkId == null) {
            lost = true
            return null
        }
        val changed = lost || networkId != lastId || transports != lastTransports
        lost = false
        lastId = networkId
        lastTransports = transports
        if (!changed) return null
        val at = nowMs + debounceMs
        restartAt = at
        return at
    }

    /** True once, when the scheduled restart is due. */
    fun onTimer(nowMs: Long): Boolean {
        val at = restartAt ?: return false
        if (nowMs < at) return false
        restartAt = null
        return true
    }
}

/** Watches the default network and calls [onRestart] on the main thread when [NetworkPolicy] says so. */
class NetworkWatcher(context: Context, private val onRestart: () -> Unit) {
    private val cm = context.getSystemService(ConnectivityManager::class.java)
    private val handler = Handler(Looper.getMainLooper())
    private val policy = NetworkPolicy()
    private var registered = false

    private val fire = Runnable {
        if (policy.onTimer(SystemClock.elapsedRealtime())) onRestart()
    }

    private val callback = object : ConnectivityManager.NetworkCallback() {
        override fun onAvailable(network: Network) {
            report(network.networkHandle, transportsOf(cm.getNetworkCapabilities(network)))
        }

        override fun onCapabilitiesChanged(network: Network, caps: NetworkCapabilities) {
            report(network.networkHandle, transportsOf(caps))
        }

        override fun onLost(network: Network) {
            report(null, emptySet())
        }
    }

    fun start() {
        if (registered) return
        try {
            cm.registerDefaultNetworkCallback(callback, handler)
            registered = true
        } catch (e: RuntimeException) {
            Log.w("P2P", "cannot watch network changes", e)
        }
    }

    fun stop() {
        handler.removeCallbacks(fire)
        if (!registered) return
        registered = false
        try {
            cm.unregisterNetworkCallback(callback)
        } catch (_: IllegalArgumentException) {
            // Already unregistered.
        }
    }

    private fun report(id: Long?, transports: Set<String>) {
        val now = SystemClock.elapsedRealtime()
        val at = policy.onDefaultNetwork(id, transports, now) ?: return
        handler.removeCallbacks(fire)
        handler.postDelayed(fire, maxOf(0L, at - now))
    }

    private fun transportsOf(caps: NetworkCapabilities?): Set<String> {
        if (caps == null) return emptySet()
        val out = HashSet<String>()
        if (caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)) out += "wifi"
        if (caps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR)) out += "cellular"
        if (caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN)) out += "vpn"
        if (caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET)) out += "ethernet"
        return out
    }
}
