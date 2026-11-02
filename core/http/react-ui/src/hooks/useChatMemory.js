// SPDX-License-Identifier: MIT
import { useCallback, useEffect, useRef, useState } from 'react'
import { chatsApi } from '../utils/api'
import { createMemoryReader } from '../utils/chatMemory'

export function useChatMemory(chatId, tick) {
  const [state, setState] = useState({ chatId: null, rows: [], error: '', retrying: null })
  const session = useRef(null)
  useEffect(() => {
    if (!chatId) return
    const current = { chatId, alive: true, retrying: null, retry: new AbortController() }
    const update = patch => { if (current.alive) setState(s => ({ ...s, ...patch, chatId })) }
    current.update = update
    current.reader = createMemoryReader(signal => chatsApi.memory(chatId, { signal }),
      rows => update({ rows, error: '' }), () => update({ error: 'load' }))
    session.current = current
    update({ rows: [], error: '', retrying: null })
    current.reader.refresh()
    // Poll even an empty visible panel: capture creation itself may miss fanout.
    const poll = setInterval(() => {
      if (document.visibilityState === 'visible') current.reader.refresh()
    }, 15000)
    const visible = () => { if (document.visibilityState === 'visible') current.reader.refresh() }
    document.addEventListener('visibilitychange', visible)
    return () => {
      current.alive = false
      current.reader.dispose()
      current.retry.abort()
      clearInterval(poll)
      document.removeEventListener('visibilitychange', visible)
      if (session.current === current) session.current = null
    }
  }, [chatId])
  useEffect(() => { if (session.current?.chatId === chatId) session.current.reader.refresh() }, [chatId, tick])
  const refresh = useCallback(() => session.current?.reader.refresh(), [])
  const retry = useCallback(async id => {
    const current = session.current
    if (!current?.alive || current.retrying || !state.rows.some(row => row.id === id && row.retry_eligible === true)) return
    current.retrying = id
    current.update({ retrying: id, error: '' })
    try {
      await chatsApi.retryMemory(current.chatId, id, { signal: current.retry.signal })
      if (current.alive) await current.reader.refresh()
    } catch (error) {
      if (current.alive && error.name !== 'AbortError') {
        await current.reader.refresh()
        current.update({ error: [403, 404, 409].includes(error.status) ? String(error.status) : 'retry' })
      }
    } finally {
      current.retrying = null
      current.update({ retrying: null })
    }
  }, [state.rows])
  return { ...(state.chatId === chatId ? state : { rows: [], error: '', retrying: null }), refresh, retry }
}
