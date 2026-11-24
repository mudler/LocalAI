import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { llamaCppApi } from '../utils/api'
import { normalizeSchema } from '../utils/llamaCppRuntime'

// The settings schema does not change while the app runs, so one answer serves
// every editor opened in the tab. A failed request is not kept: Retry asks again.
let schemaPromise = null

function loadSchema() {
  if (!schemaPromise) {
    schemaPromise = llamaCppApi.settingsSchema().then(normalizeSchema)
    schemaPromise.catch(() => { schemaPromise = null })
  }
  return schemaPromise
}

/**
 * The llama.cpp settings schema. `enabled` false does not ask (a model on
 * another backend). Returns { status: 'idle'|'loading'|'ready'|'error', items, error, retry }.
 */
export function useLlamaCppSchema(enabled = true) {
  const [state, setState] = useState({ status: enabled ? 'loading' : 'idle', items: [], error: null })
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    if (!enabled) return undefined
    let live = true
    setState(s => (s.status === 'ready' ? s : { status: 'loading', items: [], error: null }))
    loadSchema()
      .then(items => { if (live) setState({ status: 'ready', items, error: null }) })
      .catch(err => { if (live) setState({ status: 'error', items: [], error: err?.message || String(err) }) })
    return () => { live = false }
  }, [enabled, attempt])

  const retry = useCallback(() => setAttempt(a => a + 1), [])
  return { ...state, retry }
}

/**
 * The llama-server command a model config runs as, asked again 400 ms after
 * the last change. Returns { status: 'idle'|'loading'|'ready'|'error', command, error }.
 * The last command stays on screen while a new one loads.
 */
export function useCommandPreview(config, enabled = true, delay = 400) {
  const key = useMemo(() => JSON.stringify(config ?? null), [config])
  const [state, setState] = useState({ status: 'idle', command: '', error: null })
  const abortRef = useRef(null)

  useEffect(() => {
    if (!enabled) return undefined
    setState(s => ({ ...s, status: 'loading' }))
    const timer = setTimeout(() => {
      abortRef.current?.abort()
      const ctl = new AbortController()
      abortRef.current = ctl
      llamaCppApi.commandPreview(JSON.parse(key), { signal: ctl.signal })
        .then(data => {
          if (ctl.signal.aborted) return
          setState({ status: 'ready', command: String(data?.command ?? ''), error: null })
        })
        .catch(err => {
          if (ctl.signal.aborted || err?.name === 'AbortError') return
          setState(s => ({ status: 'error', command: s.command, error: err?.message || String(err) }))
        })
    }, delay)
    return () => clearTimeout(timer)
  }, [key, enabled, delay])

  useEffect(() => () => abortRef.current?.abort(), [])
  return state
}
