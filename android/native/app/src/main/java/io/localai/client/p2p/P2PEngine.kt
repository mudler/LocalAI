// SPDX-License-Identifier: MIT
package io.localai.client.p2p

import java.lang.reflect.InvocationTargetException
import java.lang.reflect.Method
import java.lang.reflect.Proxy

/** The P2P library seen by the app. Tests use a fake; the app uses [GoP2PEngine]. Every call blocks. */
interface P2PEngine {
    fun version(): String

    /** Redeems an invite; returns the library's JSON answer. Throws on failure. */
    fun pair(identityPath: String, link: String, deviceName: String, allowOpenRelay: Boolean): String

    fun peerId(identityPath: String): String

    /** Listens on [listenAddr]; [onState] gets the library's JSON state lines on any thread. */
    fun startForward(
        identityPath: String,
        peer: String,
        listenAddr: String,
        relaysJson: String,
        addrsJson: String,
        onState: (String) -> Unit,
    ): Handle

    interface Handle {
        fun stop()
    }
}

/**
 * The gomobile library (io.localai.p2p.p2pmobile.P2pmobile). It is reached by
 * reflection because the AAR is built separately: an app built without it must
 * still compile, and then reports P2P as unavailable.
 */
class GoP2PEngine : P2PEngine {
    private class Api(val pair: Method, val peerId: Method, val startForward: Method, val version: Method, val callback: Class<*>)

    private val api: Api by lazy {
        try {
            val cls = Class.forName("io.localai.p2p.p2pmobile.P2pmobile")
            val cb = Class.forName("io.localai.p2p.p2pmobile.StateCallback")
            val s = String::class.java
            Api(
                pair = cls.getMethod("pair", s, s, s, java.lang.Boolean.TYPE),
                peerId = cls.getMethod("peerID", s),
                startForward = cls.getMethod("startForward", s, s, s, s, s, cb),
                version = cls.getMethod("version"),
                callback = cb,
            )
        } catch (e: ReflectiveOperationException) {
            throw P2PUnavailableException(e)
        } catch (e: LinkageError) {
            throw P2PUnavailableException(e)
        }
    }

    override fun version(): String = invoke(api.version) as String

    override fun pair(identityPath: String, link: String, deviceName: String, allowOpenRelay: Boolean): String =
        invoke(api.pair, identityPath, link, deviceName, allowOpenRelay) as String

    override fun peerId(identityPath: String): String = invoke(api.peerId, identityPath) as String

    override fun startForward(
        identityPath: String,
        peer: String,
        listenAddr: String,
        relaysJson: String,
        addrsJson: String,
        onState: (String) -> Unit,
    ): P2PEngine.Handle {
        val cb = Proxy.newProxyInstance(api.callback.classLoader, arrayOf(api.callback)) { proxy, m, args ->
            when (m.name) {
                "onState" -> {
                    // A Java exception must not travel back into the Go callback.
                    try {
                        onState(args?.getOrNull(0) as? String ?: "")
                    } catch (e: Exception) {
                        android.util.Log.w("P2P", "state callback failed", e)
                    }
                    null
                }
                "hashCode" -> System.identityHashCode(proxy)
                "equals" -> proxy === args?.getOrNull(0)
                "toString" -> "P2PStateCallback"
                else -> null
            }
        }
        val fwd = invoke(api.startForward, identityPath, peer, listenAddr, relaysJson, addrsJson, cb)
            ?: throw IllegalStateException("the P2P library returned no forwarder")
        val stop = fwd.javaClass.getMethod("stop")
        return object : P2PEngine.Handle {
            override fun stop() {
                invoke(stop, target = fwd)
            }
        }
    }

    private fun invoke(m: Method, vararg args: Any?, target: Any? = null): Any? = try {
        m.invoke(target, *args)
    } catch (e: InvocationTargetException) {
        when (val c = e.targetException) {
            is Exception -> throw c
            is LinkageError -> throw P2PUnavailableException(c)
            else -> throw c
        }
    } catch (e: LinkageError) {
        throw P2PUnavailableException(e)
    }
}

/** The native library is missing or failed to load. */
class P2PUnavailableException(cause: Throwable) : Exception("the P2P library is not available", cause)
