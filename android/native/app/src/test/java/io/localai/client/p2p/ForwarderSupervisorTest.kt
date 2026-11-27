// SPDX-License-Identifier: MIT
package io.localai.client.p2p

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

/** Runs execute() at once and post() when the test advances the clock. */
class ManualScheduler : Scheduler {
    var now = 0L
    val delays = mutableListOf<Long>()
    private class Task(val at: Long, val block: () -> Unit) { var cancelled = false }
    private val tasks = mutableListOf<Task>()

    override fun post(delayMs: Long, block: () -> Unit): Cancellable {
        delays += delayMs
        val t = Task(now + delayMs, block)
        tasks += t
        return Cancellable { t.cancelled = true }
    }

    override fun execute(block: () -> Unit) = block()

    fun advance(ms: Long) {
        val until = now + ms
        while (true) {
            val next = tasks.filter { !it.cancelled && it.at <= until }.minByOrNull { it.at } ?: break
            tasks.remove(next)
            now = next.at
            next.block()
        }
        now = until
    }

    fun pending(): Int = tasks.count { !it.cancelled }
}

class FakeEngine : P2PEngine {
    class Start(val peer: String, val listenAddr: String, val relays: String, val addrs: String, val hints: String, val onState: (String) -> Unit) {
        var stops = 0
        val handle = object : P2PEngine.Handle {
            override fun stop() { stops++ }
        }
    }

    val starts = mutableListOf<Start>()
    var failures = 0
    val alwaysFail = mutableSetOf<String>()
    var emitReady = true

    override fun version() = "p2pmobile/4"
    override fun pairJson(identityPath: String, link: String, deviceName: String, allowOpenRelay: Boolean) = """{"ok":true,"result":{}}"""
    override fun peerId(identityPath: String) = "12D3KooWExample"

    override fun startForward(
        identityPath: String,
        peer: String,
        listenAddr: String,
        relaysJson: String,
        addrsJson: String,
        hintsJson: String,
        onState: (String) -> Unit,
    ): P2PEngine.Handle {
        if (listenAddr in alwaysFail) throw IllegalStateException("boom")
        if (failures > 0) {
            failures--
            throw IllegalStateException("boom")
        }
        val s = Start(peer, listenAddr, relaysJson, addrsJson, hintsJson, onState)
        starts += s
        // The library sends "ready" before StartForward returns.
        if (emitReady) onState("""{"state":"ready","addr":"$listenAddr"}""")
        return s.handle
    }

    fun startsAt(addr: String) = starts.filter { it.listenAddr == addr }
}

class ForwarderSupervisorTest {
    private lateinit var engine: FakeEngine
    private lateinit var sched: ManualScheduler
    private lateinit var sup: ForwarderSupervisor
    private val lines = mutableListOf<Pair<String, String>>()

    private val a = Forward("a", "Home", "peerA", "127.0.0.2", 41001, listOf("/ip4/192.0.2.1/tcp/4001"), listOf("/dns4/relay.example.com/tcp/4001"))
    private val b = Forward("b", "Office", "peerB", "127.0.0.3", 41002, emptyList(), emptyList(), listOf("/ip4/192.0.2.7/udp/4001/quic-v1/p2p/12D3KooWHint"))

    @Before
    fun setUp() {
        engine = FakeEngine()
        sched = ManualScheduler()
        lines.clear()
        sup = ForwarderSupervisor(engine, "/data/p2p/identity.key", sched) { id, line -> lines += id to line }
    }

    private fun stateOf(id: String) = sup.lastStates()[id]

    @Test
    fun startsOneForwarderPerServerOnItsLoopbackAddress() {
        sup.setForwards(listOf(a, b))
        assertEquals(listOf("127.0.0.2:41001", "127.0.0.3:41002"), engine.starts.map { it.listenAddr })
        assertEquals("peerA", engine.starts[0].peer)
        assertEquals("""["/dns4/relay.example.com/tcp/4001"]""", engine.starts[0].relays)
        assertEquals("""["/ip4/192.0.2.1/tcp/4001"]""", engine.starts[0].addrs)
        assertEquals("[]", engine.starts[1].relays)
        assertEquals("[]", engine.starts[0].hints)
        assertEquals("""["/ip4/192.0.2.7/udp/4001/quic-v1/p2p/12D3KooWHint"]""", engine.starts[1].hints)
        assertTrue(stateOf("a")!!.contains("\"ready\""))
        assertTrue(lines.contains("a" to """{"state":"starting"}"""))
    }

    @Test
    fun sameListAgainStartsNothingNew() {
        sup.setForwards(listOf(a, b))
        sup.setForwards(listOf(a, b))
        sup.setForwards(listOf(a.copy(name = "Renamed"), b))
        assertEquals(2, engine.starts.size)
        assertEquals(0, engine.starts.sumOf { it.stops })
    }

    @Test
    fun removedServerIsStopped() {
        sup.setForwards(listOf(a, b))
        sup.setForwards(listOf(a))
        assertEquals(0, engine.starts[0].stops)
        assertEquals(1, engine.starts[1].stops)
        assertFalse(sup.lastStates().containsKey("b"))
    }

    @Test
    fun changedPortStopsThenStartsOnTheNewAddress() {
        sup.setForwards(listOf(a))
        sup.setForwards(listOf(a.copy(port = 41005)))
        assertEquals(1, engine.starts[0].stops)
        assertEquals(listOf("127.0.0.2:41001", "127.0.0.2:41005"), engine.starts.map { it.listenAddr })
    }

    @Test
    fun throwingStartRetriesWithBackoffAndSuccessResetsIt() {
        engine.failures = 3
        sup.setForwards(listOf(a))
        assertEquals("""{"state":"failed","error":"boom"}""", stateOf("a"))
        sched.advance(1_000)
        sched.advance(2_000)
        sched.advance(4_000)
        assertEquals(listOf(1_000L, 2_000L, 4_000L), sched.delays)
        assertEquals(1, engine.starts.size)
        assertTrue(stateOf("a")!!.contains("\"ready\""))

        // The forwarder closes by itself: failed, then the first delay again.
        engine.starts[0].onState("""{"state":"closed"}""")
        assertTrue(stateOf("a")!!.startsWith("""{"state":"failed""""))
        assertEquals(1, engine.starts[0].stops)
        sched.advance(1_000)
        assertEquals(listOf(1_000L, 2_000L, 4_000L, 1_000L), sched.delays)
        assertEquals(2, engine.starts.size)
    }

    @Test
    fun oneThrowingServerDoesNotStopTheOthers() {
        engine.alwaysFail += "127.0.0.2:41001"
        sup.setForwards(listOf(a, b))
        assertEquals(listOf("127.0.0.3:41002"), engine.starts.map { it.listenAddr })
        assertTrue(stateOf("a")!!.contains("failed"))
        assertTrue(stateOf("b")!!.contains("ready"))
        sched.advance(10_000)
        assertEquals(0, engine.starts[0].stops)
    }

    @Test
    fun routesLinesPassOnAndKeepTheLastState() {
        sup.setForwards(listOf(a))
        engine.starts[0].onState("""{"state":"up","path":"direct"}""")
        val routes = """{"state":"routes","addrs":["/ip4/203.0.113.9/tcp/4001"],"relays":[],"hints":[]}"""
        engine.starts[0].onState(routes)
        assertTrue(lines.contains("a" to routes))
        assertEquals("""{"state":"up","path":"direct"}""", stateOf("a"))
        assertEquals(0, engine.starts[0].stops)
    }

    @Test
    fun downLinesDoNotRestart() {
        sup.setForwards(listOf(a))
        engine.starts[0].onState("""{"state":"down","error":"no route"}""")
        assertEquals("""{"state":"down","error":"no route"}""", stateOf("a"))
        assertEquals(0, sched.pending())
        assertEquals(0, engine.starts[0].stops)
        assertEquals(1, engine.starts.size)
    }

    @Test
    fun restartAllStopsAndStartsEachOnceOnTheSameAddress() {
        sup.setForwards(listOf(a, b))
        sup.restartAll()
        assertEquals(1, engine.starts[0].stops)
        assertEquals(1, engine.starts[1].stops)
        assertEquals(4, engine.starts.size)
        assertEquals(listOf("127.0.0.2:41001", "127.0.0.3:41002"), engine.starts.drop(2).map { it.listenAddr })
    }

    @Test
    fun restartSaysStartingAndWaitsForTheWebuiBeforeItReleasesThePort() {
        // Another app can bind the loopback address once the old forwarder
        // stops: "starting" must be out, and handled, before that.
        val order = mutableListOf<String>()
        val s = ForwarderSupervisor(
            engine, "/data/p2p/identity.key", sched,
            beforeRelease = { order += "release:stops=${engine.starts.sumOf { it.stops }}" },
        ) { id, line -> order += "$id:${stateOfLine(line)}" }
        s.setForwards(listOf(a))
        order.clear()
        s.restartAll()
        assertEquals(listOf("a:starting", "release:stops=0", "a:starting", "a:ready"), order)
        assertEquals(1, engine.starts[0].stops)

        // stopAll: "closed" first, then the port goes.
        order.clear()
        s.stopAll()
        assertEquals(listOf("a:closed", "release:stops=1"), order)
        assertEquals(1, engine.starts[1].stops)
    }

    @Test
    fun restartWithoutARunningForwarderDoesNotWait() {
        engine.failures = 1
        var waits = 0
        val s = ForwarderSupervisor(engine, "/data/p2p/identity.key", sched, beforeRelease = { waits++ }) { _, _ -> }
        s.setForwards(listOf(a))
        s.restartAll()
        assertEquals(0, waits)
    }

    @Test
    fun restartAllCancelsAPendingRetryAndStartsAtOnce() {
        engine.failures = 1
        sup.setForwards(listOf(a))
        assertEquals(1, sched.pending())
        sup.restartAll()
        assertEquals(0, sched.pending())
        assertEquals(1, engine.starts.size)
    }

    @Test
    fun linesOfAStoppedForwarderAreIgnored() {
        sup.setForwards(listOf(a))
        val old = engine.starts[0]
        sup.restartAll()
        old.onState("""{"state":"closed"}""")
        old.onState("""{"state":"down","error":"old"}""")
        assertTrue(stateOf("a")!!.contains("ready"))
        assertEquals(0, sched.pending())
    }

    @Test
    fun stopAllIsSafeTwice() {
        sup.setForwards(listOf(a, b))
        sup.stopAll()
        sup.stopAll()
        assertEquals(listOf(1, 1), engine.starts.map { it.stops })
        assertEquals("""{"state":"closed"}""", stateOf("a"))
        assertEquals("""{"state":"closed"}""", stateOf("b"))
        sup.restartAll()
        assertEquals(2, engine.starts.size)
    }

    @Test
    fun stopAllCancelsRetries() {
        engine.failures = 5
        sup.setForwards(listOf(a))
        sup.stopAll()
        sched.advance(120_000)
        assertEquals(0, engine.starts.size)
    }

    @Test
    fun removingAServerCancelsItsRetry() {
        engine.failures = 5
        sup.setForwards(listOf(a))
        sup.setForwards(emptyList())
        sched.advance(120_000)
        assertEquals(0, engine.starts.size)
    }

    @Test
    fun nonLoopbackListenAddressesAreNeverPassedToTheLibrary() {
        val bad = listOf("0.0.0.0", "192.0.2.10", "198.51.100.7", "localhost", "::1", "127.0.0.0", "127.255.255.255", "127.0.0.256", "127.0.0", "127.0.0.1.5", " 127.0.0.2", "127.00.0.2", "")
        sup.setForwards(bad.mapIndexed { i, h -> a.copy(id = "x$i", host = h) } + b.copy(id = "p", port = 0) + b.copy(id = "q", port = 65536))
        assertEquals(0, engine.starts.size)
        for (i in bad.indices) assertEquals("""{"state":"failed","error":"listen address must be loopback"}""", stateOf("x$i"))
        assertTrue(stateOf("p")!!.contains("failed"))
        assertEquals(0, sched.pending())
    }

    @Test
    fun loopbackHostRule() {
        for (h in listOf("127.0.0.1", "127.0.0.2", "127.1.2.3", "127.255.255.254")) assertTrue(h, isLoopbackHost(h))
        for (h in listOf("127.0.0.0", "127.255.255.255", "128.0.0.1", "127.0.0.01", "127.-1.0.1", "127.0.0.1 ", "localhost")) assertFalse(h, isLoopbackHost(h))
    }

    @Test
    fun errorTextIsEscapedInTheFailedLine() {
        engine.alwaysFail += "127.0.0.2:41001"
        val sup2 = ForwarderSupervisor(object : P2PEngine by engine {
            override fun startForward(
                identityPath: String,
                peer: String,
                listenAddr: String,
                relaysJson: String,
                addrsJson: String,
                hintsJson: String,
                onState: (String) -> Unit,
            ): P2PEngine.Handle = throw IllegalStateException("bad \"quote\"\nline")
        }, "/id", sched) { id, line -> lines += id to line }
        sup2.setForwards(listOf(a))
        assertEquals("""{"state":"failed","error":"bad \"quote\"\nline"}""", sup2.lastStates()["a"])
    }
}
