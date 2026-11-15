import { useEffect, useState } from 'react'
import { hostGrantsApi } from '../../utils/addonsClient'
import { initialTicks, normalizePreview } from './hostGrantsState'

// Asks the owner what a library entry would need, from the package it
// verifies itself (the library's own listing is not trusted for this). It
// reads and grants nothing. Returns { status: 'loading'|'ready'|'error',
// slow, preview, error, ticked, setTick }. `slow` turns true when the owner
// has not answered after SLOW_AFTER_MS: it is downloading and verifying the
// package, which a page should say instead of a bare spinner.
export const SLOW_AFTER_MS = 3000

export function useHostPreview(entry, { enabled = true, api = hostGrantsApi } = {}) {
  const key = entry ? `${entry.sourceId}/${entry.id}/${entry.version}` : ''
  const [state, setState] = useState({ key: '', status: 'loading', preview: null, error: null, ticked: [] })
  const [attempt, setAttempt] = useState(0)
  const [slowKey, setSlowKey] = useState('')
  useEffect(() => {
    if (!enabled || !entry) return undefined
    const ctl = new AbortController()
    const timer = setTimeout(() => setSlowKey(`${key}#${attempt}`), SLOW_AFTER_MS)
    api.preview({ sourceId: entry.sourceId, addonId: entry.id, version: entry.version }, ctl.signal)
      .then(raw => {
        const preview = normalizePreview(raw)
        setState({ key, status: 'ready', preview, error: null, ticked: initialTicks(preview) })
      })
      .catch(error => {
        if (error && error.name === 'AbortError') return
        setState({ key, status: 'error', preview: null, error, ticked: [] })
      })
    return () => { clearTimeout(timer); ctl.abort() }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, enabled, attempt, api])
  const mine = state.key === key
  const setTick = (name, on) => setState(s => ({ ...s, ticked: on ? [...new Set([...s.ticked, name])] : s.ticked.filter(n => n !== name) }))
  const retry = () => { setState({ key: '', status: 'loading', preview: null, error: null, ticked: [] }); setAttempt(n => n + 1) }
  const loading = !mine || state.status === 'loading'
  const slow = loading && slowKey === `${key}#${attempt}`
  return mine ? { ...state, slow, setTick, retry } : { status: 'loading', slow, preview: null, error: null, ticked: [], setTick, retry }
}
