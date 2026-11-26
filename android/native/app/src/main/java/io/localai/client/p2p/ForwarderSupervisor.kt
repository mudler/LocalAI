// SPDX-License-Identifier: MIT
package io.localai.client.p2p

import java.util.concurrent.RejectedExecutionException
import java.util.concurrent.ScheduledThreadPoolExecutor
import java.util.concurrent.TimeUnit

/** One saved P2P server to forward: connections to [host]:[port] go to [peer]. */
data class Forward(
    val id: String,
    val name: String,
    val peer: String,
    val host: String,
    val port: Int,
    val addrs: List<String>,
    val relays: List<String>,
    /** Public relays of the invite that only help a hole punch: they never carry data. */
    val hints: List<String> = emptyList(),
)

fun interface Cancellable {
    fun cancel()
}

interface Scheduler {
    fun post(delayMs: Long, block: () -> Unit): Cancellable
    fun execute(block: () -> Unit)
}

/** One background thread: every supervisor step and every library call runs on it, in order. */
class ExecutorScheduler(name: String) : Scheduler {
    private val pool = ScheduledThreadPoolExecutor(1) { r -> Thread(r, name).apply { isDaemon = true } }.apply {
        executeExistingDelayedTasksAfterShutdownPolicy = false
        removeOnCancelPolicy = true
    }

    // After shutdown (the service is gone) new work is dropped, not thrown:
    // execute is also called from the library's callback threads.
    override fun post(delayMs: Long, block: () -> Unit): Cancellable = try {
        val f = pool.schedule(block, delayMs, TimeUnit.MILLISECONDS)
        Cancellable { f.cancel(false) }
    } catch (_: RejectedExecutionException) {
        Cancellable { }
    }

    override fun execute(block: () -> Unit) {
        try {
            pool.execute(block)
        } catch (_: RejectedExecutionException) {
            // Shut down.
        }
    }

    /** Lets queued steps (the final stop) run, drops delayed retries. */
    fun shutdown() = pool.shutdown()
}

private val LOOPBACK = Regex("""127\.(0|[1-9][0-9]{0,2})\.(0|[1-9][0-9]{0,2})\.(0|[1-9][0-9]{0,2})""")

/**
 * True for a dotted IPv4 address in 127.0.0.0/8 other than the network and
 * broadcast addresses. The library listens on whatever it is given, so this is
 * the only check that keeps a forwarder off the network.
 */
fun isLoopbackHost(host: String): Boolean {
    val m = LOOPBACK.matchEntire(host) ?: return false
    val parts = m.groupValues.drop(1).map { it.toInt() }
    if (parts.any { it > 255 }) return false
    return host != "127.0.0.0" && host != "127.255.255.255"
}

internal fun jsonString(s: String): String {
    val b = StringBuilder("\"")
    for (c in s) {
        when {
            c == '"' -> b.append("\\\"")
            c == '\\' -> b.append("\\\\")
            c == '\n' -> b.append("\\n")
            c == '\r' -> b.append("\\r")
            c == '\t' -> b.append("\\t")
            c < ' ' || c in '\u2028'..'\u2029' -> b.append("\\u").append(c.code.toString(16).padStart(4, '0'))
            else -> b.append(c)
        }
    }
    return b.append('"').toString()
}

private fun jsonArray(list: List<String>) = list.joinToString(",", "[", "]") { jsonString(it) }

internal const val STARTING_LINE = """{"state":"starting"}"""
internal const val CLOSED_LINE = """{"state":"closed"}"""

internal fun failedLine(error: String) = """{"state":"failed","error":${jsonString(error)}}"""

private val STATE = Regex(""""state"\s*:\s*"([a-z]+)"""")

internal fun stateOfLine(line: String): String? = STATE.find(line)?.groupValues?.get(1)

/**
 * Keeps one forwarder per saved P2P server. A start that throws reports
 * "failed" and is retried after [Backoff.delayMs]; a successful start resets
 * the delay. A forwarder that closes by itself is restarted the same way.
 * "down" lines do not restart anything: the library dials again for each
 * connection. Each server is independent, so one that fails does not stop
 * the others.
 *
 * [beforeRelease] runs on the scheduler thread after a "starting" or
 * "closed" line and before a running forwarder is stopped. The service
 * uses it to wait until the main thread has handled that line.
 */
class ForwarderSupervisor(
    private val engine: P2PEngine,
    private val identityPath: String,
    private val scheduler: Scheduler,
    private val beforeRelease: () -> Unit = {},
    private val onState: (id: String, line: String) -> Unit,
) {
    private class Slot(var forward: Forward) {
        var handle: P2PEngine.Handle? = null
        var retry: Cancellable? = null
        var failures = 0
        var generation = 0L
    }

    // Slots change only on the scheduler thread; the lock is for state lines,
    // which the library sends from its own threads.
    private val lock = Any()
    private val slots = LinkedHashMap<String, Slot>()
    private val states = LinkedHashMap<String, String>()
    private var generations = 0L

    /** Starts new ids, stops removed ids, restarts changed ones. */
    fun setForwards(list: List<Forward>) = scheduler.execute { apply(list) }

    /** Stop then start every forwarder on the same address (a network change). */
    fun restartAll() = scheduler.execute {
        for ((id, slot) in snapshot()) restart(id, slot)
    }

    fun stopAll() = scheduler.execute {
        for ((id, slot) in snapshot()) {
            // "closed" before the port is released, as in restart().
            val running = synchronized(lock) { slot.handle != null }
            record(id, CLOSED_LINE)
            if (running) beforeRelease()
            drop(id, slot)
        }
    }

    fun lastStates(): Map<String, String> = synchronized(lock) { LinkedHashMap(states) }

    private fun snapshot() = synchronized(lock) { slots.entries.map { it.key to it.value } }

    private fun apply(list: List<Forward>) {
        val wanted = LinkedHashMap<String, Forward>()
        for (f in list) if (f.id.isNotEmpty()) wanted[f.id] = f
        for ((id, slot) in snapshot()) {
            if (id !in wanted) {
                drop(id, slot)
                synchronized(lock) { states.remove(id) }
            }
        }
        for ((id, f) in wanted) {
            val slot = synchronized(lock) { slots[id] }
            when {
                slot == null -> {
                    val s = Slot(f)
                    synchronized(lock) { slots[id] = s }
                    start(id, s)
                }
                // A new name changes only the notification text.
                slot.forward.copy(name = f.name) != f -> {
                    slot.forward = f
                    restart(id, slot)
                }
                else -> slot.forward = f
            }
        }
    }

    private fun drop(id: String, slot: Slot) {
        val h = synchronized(lock) {
            slots.remove(id)
            slot.generation = ++generations
            slot.retry?.cancel()
            slot.retry = null
            slot.handle.also { slot.handle = null }
        }
        h?.let { stopQuietly(it) }
    }

    private fun restart(id: String, slot: Slot) {
        val h = synchronized(lock) {
            slot.retry?.cancel()
            slot.retry = null
            slot.failures = 0
            slot.generation = ++generations
            slot.handle.also { slot.handle = null }
        }
        if (h != null) {
            // Any app can bind the loopback address once the old forwarder
            // lets it go: report "starting" and let the WebUI view drop the
            // page first, so no request reaches another app's listener.
            record(id, STARTING_LINE)
            beforeRelease()
            stopQuietly(h)
        }
        start(id, slot)
    }

    private fun start(id: String, slot: Slot) {
        val f = slot.forward
        val gen = synchronized(lock) {
            if (slots[id] !== slot) return
            slot.generation = ++generations
            slot.generation
        }
        if (!isLoopbackHost(f.host) || f.port !in 1..65535) {
            // A bad address is a caller bug; retrying would not fix it.
            record(id, failedLine("listen address must be loopback"))
            return
        }
        record(id, STARTING_LINE)
        val handle = try {
            engine.startForward(identityPath, f.peer, "${f.host}:${f.port}", jsonArray(f.relays), jsonArray(f.addrs), jsonArray(f.hints)) { line ->
                onLine(id, gen, line)
            }
        } catch (e: Exception) {
            fail(id, slot, gen, e.message ?: e.javaClass.simpleName)
            return
        }
        val stale = synchronized(lock) {
            if (slots[id] === slot && slot.generation == gen) {
                slot.handle = handle
                slot.failures = 0
                false
            } else {
                true
            }
        }
        if (stale) stopQuietly(handle)
    }

    private fun fail(id: String, slot: Slot, gen: Long, error: String) {
        val delay = synchronized(lock) {
            if (slots[id] !== slot || slot.generation != gen) return
            Backoff.delayMs(slot.failures++)
        }
        record(id, failedLine(error))
        val retry = scheduler.post(delay) {
            val due = synchronized(lock) {
                (slots[id] === slot && slot.generation == gen && slot.handle == null).also { if (it) slot.retry = null }
            }
            if (due) start(id, slot)
        }
        synchronized(lock) {
            if (slots[id] === slot && slot.generation == gen) slot.retry = retry else retry.cancel()
        }
    }

    private fun onLine(id: String, gen: Long, line: String) {
        val current = synchronized(lock) { slots[id]?.takeIf { it.generation == gen } } ?: return
        if (stateOfLine(line) == "routes") {
            // The routes of the server are no state: pass them on and keep
            // the last state, which says whether the forwarder listens.
            onState(id, line)
            return
        }
        if (stateOfLine(line) != "closed") {
            record(id, line, gen)
            return
        }
        // Closed without our Stop: the listener or the host went away.
        scheduler.execute {
            val h = synchronized(lock) {
                if (slots[id] !== current || current.generation != gen) return@execute
                current.handle.also { current.handle = null }
            }
            h?.let { stopQuietly(it) }
            fail(id, current, gen, "the forwarder closed")
        }
    }

    private fun record(id: String, line: String, gen: Long? = null) {
        synchronized(lock) {
            if (gen != null && slots[id]?.generation != gen) return
            states[id] = line
        }
        onState(id, line)
    }

    private fun stopQuietly(h: P2PEngine.Handle) {
        try {
            h.stop()
        } catch (_: Exception) {
            // Stop is best effort; the forwarder is forgotten either way.
        }
    }
}
