import { useCallback, useEffect, useRef, useState } from 'react'
import { chatsApi } from '../utils/api'
import { eventPayload, retryDelay } from '../utils/streamRetry'

// Every event kind the server sends. EventSource needs one listener per name.
const EVENT_KINDS = [
  'turn_start', 'text', 'reasoning', 'tool_start', 'tool_end', 'checkpoint',
  'turn_end', 'error', 'pending_delivered', 'sub_agent_done', 'compacted',
]

const RECONNECT_MS = 1000 // first wait; it doubles up to 30 s

const emptyLive = () => ({ text: '', reasoning: '', tools: [] })

// State of the chats the server keeps: the list, one open chat with its
// transcript and waiting messages, and the live events of a running turn.
//
// Events are live only. The transcript is the record, so the hook reads it
// again whenever something that changes it happens (a turn starts or ends, a
// waiting message reaches the model) and whenever the stream reconnects.
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
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const sourceRef = useRef(null)
  const reconnectRef = useRef(null)
  const activeRef = useRef(null)
  const attemptRef = useRef(0)

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
  }, [])

  // Reads the chat again. It answers 'gone' when the server no longer has it
  // (deleted in another tab), which ends the stream: there is nothing to follow.
  const refreshChat = useCallback(async (id) => {
    try {
      const [d, m] = await Promise.all([chatsApi.get(id), chatsApi.messages(id)])
      if (activeRef.current !== id) return 'stale'
      setDetail(d)
      setMessages(m.messages || [])
      return 'ok'
    } catch (e) {
      if (activeRef.current !== id) return 'stale'
      setError(e.message)
      return e?.status === 404 ? 'gone' : 'error'
    }
  }, [])

  const refreshDetail = useCallback(async (id) => {
    try {
      const d = await chatsApi.get(id)
      if (activeRef.current === id) setDetail(d)
    } catch (e) {
      if (activeRef.current === id) setError(e.message)
    }
  }, [])

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
        setDetail(d => (d ? { ...d, running: true } : d))
        refreshChat(id)
      },
      text: (e) => setLive(l => ({ ...l, text: l.text + (e.text || '') })),
      reasoning: (e) => setLive(l => ({ ...l, reasoning: l.reasoning + (e.text || '') })),
      tool_start: (e) => setLive(l => ({
        ...l, tools: [...l.tools, { name: e.tool?.name || '', summary: e.tool?.summary || '', state: 'running' }],
      })),
      tool_end: (e) => setLive(l => {
        const tools = [...l.tools]
        for (let i = tools.length - 1; i >= 0; i--) {
          if (tools[i].state === 'running' && tools[i].name === (e.tool?.name || '')) {
            tools[i] = { ...tools[i], state: e.tool?.ok ? 'done' : 'failed', output: e.tool?.output || '' }
            break
          }
        }
        return { ...l, tools }
      }),
      pending_delivered: () => refreshChat(id),
      compacted: (e) => setNotes(n => [...n, { kind: 'compacted', before: e.before, after: e.after }]),
      sub_agent_done: (e) => setNotes(n => [...n, { kind: 'subAgentDone', title: e.text || '' }]),
      error: (e) => { if (e.text) setError(e.text) },
      turn_end: () => {
        setLive(emptyLive())
        refreshChat(id)
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
  }, [closeStream, refreshChat, refreshList])

  const select = useCallback(async (id) => {
    activeRef.current = id
    setActiveId(id)
    setLive(emptyLive())
    setNotes([])
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
  }, [closeStream, connect, refreshChat])

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
  }, [refreshList])

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
  }, [refreshChat])

  const act = useCallback(async (id, call) => {
    setError('')
    try {
      await call()
    } catch (e) {
      setError(e.message)
    }
    await refreshDetail(id)
  }, [refreshDetail])

  const editPending = useCallback((id, pid, text) => act(id, () => chatsApi.editPending(id, pid, text)), [act])
  const cancelPending = useCallback((id, pid) => act(id, () => chatsApi.cancelPending(id, pid)), [act])
  const stop = useCallback((id) => act(id, () => chatsApi.stop(id)), [act])
  const sendNow = useCallback((id) => act(id, () => chatsApi.sendNow(id)), [act])

  const remove = useCallback(async (id) => {
    await chatsApi.delete(id)
    if (activeRef.current === id) await select(null)
    await refreshList()
  }, [refreshList, select])

  return {
    chats, persistent, files, activeId, detail, messages, live, notes, error, loading,
    running: Boolean(detail?.running),
    lastTurn: detail?.last_turn || '',
    pending: detail?.pending || [],
    select, createChat, send, editPending, cancelPending, stop, sendNow, remove, refreshList,
    clearError: () => setError(''),
  }
}
