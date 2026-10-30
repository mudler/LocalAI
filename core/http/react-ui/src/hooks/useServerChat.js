import { useCallback, useEffect, useRef, useState } from 'react'
import { chatsApi } from '../utils/api'
import { eventPayload, retryDelay } from '../utils/streamRetry'
import { emptyLive, toolStart, toolEnd, cardResult, cardFromMessages, liveCardWanted } from '../utils/chatLive'
import { notifyTurnEnd } from './useWorkspaceFeed'
import { pushUsage } from '../utils/chatFoot'

// Every event kind the server sends. EventSource needs one listener per name.
const EVENT_KINDS = [
  'turn_start', 'text', 'reasoning', 'tool_start', 'tool_end', 'checkpoint',
  'turn_end', 'error', 'pending_delivered', 'sub_agent_started', 'sub_agent_done', 'compacted',
  'usage', 'trigger_proposed', 'files',
]

const RECONNECT_MS = 1000 // first wait; it doubles up to 30 s
const SETTLE_READS = 8
const SETTLE_MS = 250

// State of the chats the server keeps: the list, one open chat with its
// transcript and waiting messages, and the live events of a running turn.
//
// Events are live only. The transcript is the record, so the hook reads it
// again whenever something that changes it happens (a turn starts or ends, a
// waiting message reaches the model) and whenever the stream reconnects.
//
// The live turn stays on screen until the transcript that holds it has been
// read, and both change in one step (chatLive.js draws them as one list), so
// nothing flickers, shows twice or remounts at the end of a turn. An MCP App is
// shown as soon as its tool call ends: the event names the transcript line of
// the call and the hook reads that one line for the input and the result.
export function useServerChat() {
  const [chats, setChats] = useState([])
  // Unknown until the list answers; only an explicit false shows the notice.
  const [persistent, setPersistent] = useState(true)
  const [files, setFiles] = useState({ on: true, reason: '' })
  const [activeId, setActiveId] = useState(null)
  const [detail, setDetail] = useState(null)
  const [messages, setMessages] = useState([])
  const [live, setLive] = useState(emptyLive)
  const [notes, setNotes] = useState([])
  // What the last model request used ({ used, limit, completion,
  // tokens_per_second }). The server does not keep it: a reload has none until
  // the next request.
  const [usage, setUsage] = useState(null)
  // The usage of the requests seen since the chat was opened (the last 10),
  // for the average speed of the status line.
  const [usageHistory, setUsageHistory] = useState([])
  // How long the turn the page saw start waited for its first streamed piece,
  // in ms. Null until then, and for a turn that started before the page
  // connected.
  const [firstTokenMs, setFirstTokenMs] = useState(null)
  // Counts the sub-agent starts and ends, the trigger proposals and the
  // changes to the chat's files, so the panels that list them read the server
  // again.
  const [ticks, setTicks] = useState({ subAgents: 0, proposals: 0, files: 0 })
  const [error, setErrorText] = useState('')
  // Why the last turn failed ({ code, model, retry, detail, text }), when the
  // server said. It lives and dies with the error text.
  const [failure, setFailure] = useState(null)
  const setError = useCallback((message) => {
    setErrorText(message)
    if (!message) setFailure(null)
  }, [])
  const [loading, setLoading] = useState(false)

  const sourceRef = useRef(null)
  const reconnectRef = useRef(null)
  const activeRef = useRef(null)
  // Whether the last read of the open chat said a turn runs.
  const runningRef = useRef(false)
  const attemptRef = useRef(0)
  // Counts the turns started, so that the end of one turn does not clear the
  // live view of the next.
  const epochRef = useRef(0)
  // The pace of the streamed pieces, read by the status line at its own rate
  // so a fast stream does not render it for every piece. Times in ms:
  //   turnAt     the turn_start the page saw
  //   firstAt    the first piece of that turn
  //   reqAt      the first piece of the current model request
  //   lastAt     the latest piece
  //   reqChunks  the pieces of the current request
  const paceRef = useRef({ turnAt: 0, firstAt: 0, reqAt: 0, lastAt: 0, reqChunks: 0 })
  const piece = useCallback(() => {
    const p = paceRef.current
    const now = Date.now()
    if (p.turnAt && !p.firstAt) {
      p.firstAt = now
      setFirstTokenMs(now - p.turnAt)
    }
    if (!p.reqAt) { p.reqAt = now; p.reqChunks = 0 }
    p.lastAt = now
    p.reqChunks++
  }, [])
  // A tool call or the end of a request starts a new request: its pace
  // starts again.
  const newRequest = () => { const p = paceRef.current; p.reqAt = 0; p.lastAt = 0; p.reqChunks = 0 }

  const refreshList = useCallback(async () => {
    try {
      // Sub-agent runs belong to their parent; the list shows chats only. The
      // server leaves the runs out before it applies the limit (200 is its
      // maximum); the filter stays for a server that does not know kind.
      const data = await chatsApi.list(200, 'chat')
      setChats((data.conversations || []).filter(c => c.kind !== 'subagent'))
      setPersistent(data.persistent !== false)
      setFiles({ on: data.files !== false, reason: data.files_reason || '' })
    } catch (e) {
      setError(e.message)
    }
  }, [setError])

  // Reads the chat again. It answers 'gone' when the server no longer has it
  // (deleted in another tab), which ends the stream: there is nothing to follow.
  // With endEpoch (the epoch of a turn that ended) the live view is dropped in
  // the same step as the transcript is replaced, unless a newer turn has started.
  const refreshChat = useCallback(async (id, endEpoch) => {
    const endLive = () => {
      if (endEpoch !== undefined && endEpoch === epochRef.current) setLive(emptyLive())
    }
    try {
      const [d, m] = await Promise.all([chatsApi.get(id), chatsApi.messages(id)])
      if (activeRef.current !== id) return 'stale'
      setDetail(d)
      runningRef.current = Boolean(d?.running)
      setMessages(m.messages || [])
      endLive()
      return 'ok'
    } catch (e) {
      if (activeRef.current !== id) return 'stale'
      setError(e.message)
      endLive()
      return e?.status === 404 ? 'gone' : 'error'
    }
  }, [setError])

  const refreshDetail = useCallback(async (id) => {
    try {
      const d = await chatsApi.get(id)
      if (activeRef.current === id) setDetail(d)
    } catch (e) {
      if (activeRef.current === id) setError(e.message)
    }
  }, [setError])

  const closeStream = useCallback(() => {
    clearTimeout(reconnectRef.current)
    reconnectRef.current = null
    if (sourceRef.current) {
      sourceRef.current.close()
      sourceRef.current = null
    }
  }, [])

  const connect = useCallback((id) => {
    closeStream()
    const source = new EventSource(chatsApi.eventsUrl(id))
    sourceRef.current = source

    const on = {
      turn_start: () => {
        epochRef.current++
        setError('')
        paceRef.current = { turnAt: Date.now(), firstAt: 0, reqAt: 0, lastAt: 0, reqChunks: 0 }
        setFirstTokenMs(null)
        setLive(emptyLive())
        setDetail(d => (d ? { ...d, running: true } : d))
        refreshChat(id)
      },
      text: (e) => { piece(); setLive(l => ({ ...l, text: l.text + (e.text || ''), phase: 'text' })) },
      reasoning: (e) => { piece(); setLive(l => ({ ...l, reasoning: l.reasoning + (e.text || ''), phase: 'reasoning' })) },
      tool_start: (e) => { newRequest(); setLive(l => ({ ...toolStart(l, e.tool), phase: 'tool' })) },
      tool_end: (e) => {
        setLive(l => ({ ...toolEnd(l, e.tool), phase: 'after-tool' }))
        if (!liveCardWanted(e.tool)) return
        // The event holds the reference of the app only. The line of the call has
        // the input and the result: read that one line, then show the app.
        const seq = e.tool.seq
        chatsApi.card(id, seq)
          .then((lines) => cardFromMessages(lines, seq), () => null)
          .then((card) => { if (activeRef.current === id) setLive(l => cardResult(l, seq, card)) })
      },
      pending_delivered: () => refreshChat(id),
      compacted: (e) => setNotes(n => [...n, { kind: 'compacted', before: e.before, after: e.after }]),
      sub_agent_started: () => setTicks(t => ({ ...t, subAgents: t.subAgents + 1 })),
      sub_agent_done: (e) => {
        setNotes(n => [...n, { kind: 'subAgentDone', title: e.text || '' }])
        setTicks(t => ({ ...t, subAgents: t.subAgents + 1 }))
      },
      usage: (e) => {
        if (!e.usage) return
        newRequest()
        setUsage(e.usage)
        setUsageHistory(h => pushUsage(h, e.usage))
      },
      trigger_proposed: () => setTicks(t => ({ ...t, proposals: t.proposals + 1 })),
      files: () => setTicks(t => ({ ...t, files: t.files + 1 })),
      error: (e) => {
        if (!e.text) return
        setError(e.text)
        // Only the error that ended the turn has a code.
        if (e.code) setFailure({ code: e.code, model: e.model || '', retry: e.retry || 0, detail: e.detail || '', text: e.text })
      },
      turn_end: async () => {
        notifyTurnEnd()
        // The server publishes the end of a turn just before it records it, so
        // a read at once can still say running. Read again, a few times, until
        // the record agrees, unless a new turn started meanwhile.
        const epoch = epochRef.current
        for (let i = 0; i < SETTLE_READS; i++) {
          const d = await refreshChat(id, epoch)
          if (d !== 'ok' || epochRef.current !== epoch || !runningRef.current) break
          await new Promise(r => setTimeout(r, SETTLE_MS))
        }
        refreshList()
      },
    }
    for (const kind of EVENT_KINDS) {
      source.addEventListener(kind, (ev) => {
        // The browser fires its own "error" event when the connection breaks:
        // it has no data and is not the server's error event.
        const payload = eventPayload(ev)
        if (payload) on[kind]?.(payload)
      })
    }
    source.onopen = () => { attemptRef.current = 0 }
    source.onerror = () => {
      // The stream carries only what happens after it connects, so a reconnect
      // reads the transcript again to close the gap.
      source.close()
      if (sourceRef.current === source) sourceRef.current = null
      if (activeRef.current !== id) return
      reconnectRef.current = setTimeout(async () => {
        if (activeRef.current !== id) return
        // Read first: a chat deleted in another tab answers 404 and the stream stops.
        const state = await refreshChat(id)
        if (state === 'gone' || activeRef.current !== id) return
        connect(id)
      }, retryDelay(attemptRef.current++, RECONNECT_MS))
    }
  }, [closeStream, refreshChat, refreshList, setError, piece])

  const readPace = useCallback(() => ({ ...paceRef.current }), [])

  const select = useCallback(async (id) => {
    activeRef.current = id
    setActiveId(id)
    epochRef.current++
    setLive(emptyLive())
    setNotes([])
    setUsage(null)
    setUsageHistory([])
    setFirstTokenMs(null)
    paceRef.current = { turnAt: 0, firstAt: 0, reqAt: 0, lastAt: 0, reqChunks: 0 }
    setError('')
    setDetail(null)
    setMessages([])
    attemptRef.current = 0
    if (!id) {
      closeStream()
      return
    }
    setLoading(true)
    connect(id)
    await refreshChat(id)
    setLoading(false)
  }, [closeStream, connect, refreshChat, setError])

  useEffect(() => {
    refreshList()
    return () => {
      activeRef.current = null
      closeStream()
    }
  }, [refreshList, closeStream])

  const createChat = useCallback(async (model, agentId) => {
    setError('')
    const created = await chatsApi.create(model, undefined, agentId)
    await refreshList()
    return created.id
  }, [refreshList, setError])

  // Sends go out one after another, so two messages typed quickly reach the
  // server in the order they were typed and cannot swap on the wire.
  const sendChain = useRef(Promise.resolve())
  const send = useCallback((id, text, attached = []) => {
    setError('')
    const run = async () => {
      const res = await chatsApi.send(id, text, attached)
      if (res.started) setDetail(d => (d ? { ...d, running: true } : d))
      await refreshChat(id)
      return res
    }
    const result = sendChain.current.then(run, run)
    sendChain.current = result.catch(() => {})
    return result
  }, [refreshChat, setError])

  const act = useCallback(async (id, call) => {
    setError('')
    try {
      await call()
    } catch (e) {
      setError(e.message)
    }
    await refreshDetail(id)
  }, [refreshDetail, setError])

  const editPending = useCallback((id, pid, text) => act(id, () => chatsApi.editPending(id, pid, text)), [act])
  const cancelPending = useCallback((id, pid) => act(id, () => chatsApi.cancelPending(id, pid)), [act])
  const stop = useCallback((id) => act(id, () => chatsApi.stop(id)), [act])
  const sendNow = useCallback((id) => act(id, () => chatsApi.sendNow(id)), [act])

  // A title or a model answers 409 while a turn runs; settings apply from the
  // next turn. The caller gets the error to show where the change was made.
  const update = useCallback(async (id, fields) => {
    const out = await chatsApi.update(id, fields)
    if (activeRef.current === id) setDetail(d => (d ? { ...d, ...out } : d))
    await refreshList()
    return out
  }, [refreshList])

  // Renaming answers 409 while a turn runs; the error shows like any other.
  const rename = useCallback(async (id, title) => {
    setError('')
    try {
      await update(id, { title })
    } catch (e) {
      setError(e.message)
      await refreshList()
    }
  }, [update, refreshList, setError])

  // Regenerate and edit cut the transcript and start a new turn: read it again.
  const restart = useCallback(async (id, call) => {
    setError('')
    try {
      const res = await call()
      if (res?.started) setDetail(d => (d ? { ...d, running: true } : d))
    } catch (e) {
      setError(e.message)
    }
    await refreshChat(id)
  }, [refreshChat, setError])
  const regenerate = useCallback((id) => restart(id, () => chatsApi.regenerate(id)), [restart])
  const editMessage = useCallback((id, seq, text) => restart(id, () => chatsApi.editMessage(id, seq, text)), [restart])

  const remove = useCallback(async (id) => {
    await chatsApi.delete(id)
    if (activeRef.current === id) await select(null)
    await refreshList()
  }, [refreshList, select])

  return {
    chats, persistent, files, activeId, detail, messages, live, notes, error, failure, loading, usage, usageHistory, firstTokenMs, readPace, ticks,
    running: Boolean(detail?.running),
    lastTurn: detail?.last_turn || '',
    pending: detail?.pending || [],
    select, createChat, send, editPending, cancelPending, stop, sendNow, remove, rename, update, regenerate, editMessage, refreshList,
    clearError: () => setError(''),
  }
}
