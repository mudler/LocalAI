import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { engineSettingsApi } from '../utils/api'
import { normalizeSchema } from '../utils/engineSettings'

// The engines and their schemas do not change while the app runs, so one
// answer serves every editor opened in the tab. A failed request is not kept:
// Retry asks again.
const engineByBackend = new Map()
const schemaByEngine = new Map()

function cached(map, key, load) {
  if (!map.has(key)) {
    const p = load()
    map.set(key, p)
    p.catch(() => { if (map.get(key) === p) map.delete(key) })
  }
  return map.get(key)
}

function loadEngine(backend) {
  return cached(engineByBackend, backend, () => engineSettingsApi.list({ backend })
    .then(data => (Array.isArray(data?.engines) && data.engines.length ? data.engines[0] : null)))
}

function loadSchema(engine) {
  return cached(schemaByEngine, engine, () => engineSettingsApi.settingsSchema(engine).then(normalizeSchema))
}

/**
 * The engine whose runtime settings apply to a model backend, asked from the
 * server (GET /api/engines?backend=). Returns { status: 'idle'|'loading'|'ready'|'error',
 * engine } with engine null when no engine serves the backend.
 */
export function useEngineForBackend(backend, enabled = true) {
  const key = backend === undefined || backend === null ? '' : String(backend).trim()
  const [state, setState] = useState({ status: enabled ? 'loading' : 'idle', engine: null, key })

  useEffect(() => {
    if (!enabled) return undefined
    let live = true
    setState(s => (s.key === key && s.status === 'ready' ? s : { status: 'loading', engine: null, key }))
    loadEngine(key)
      .then(engine => { if (live) setState({ status: 'ready', engine, key }) })
      .catch(() => { if (live) setState({ status: 'error', engine: null, key }) })
    return () => { live = false }
  }, [key, enabled])

  return enabled ? state : { status: 'idle', engine: null, key }
}

/**
 * The settings schema of an engine, normalised (utils/engineSettings
 * normalizeSchema). Returns { status: 'idle'|'loading'|'ready'|'error', schema, error, retry }.
 */
export function useEngineSchema(engine) {
  const [state, setState] = useState({ status: engine ? 'loading' : 'idle', schema: null, error: null })
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    if (!engine) return undefined
    let live = true
    setState(s => (s.status === 'ready' && s.schema?.id === engine ? s : { status: 'loading', schema: null, error: null }))
    loadSchema(engine)
      .then(schema => { if (live) setState({ status: 'ready', schema, error: null }) })
      .catch(err => { if (live) setState({ status: 'error', schema: null, error: err?.message || String(err) }) })
    return () => { live = false }
  }, [engine, attempt])

  const retry = useCallback(() => setAttempt(a => a + 1), [])
  return { ...state, retry }
}

/**
 * The command line a model config runs as, asked again 400 ms after the last
 * change. Returns { status: 'idle'|'loading'|'ready'|'error', command, error }.
 * The last command stays on screen while a new one loads.
 */
export function useCommandPreview(engine, config, enabled = true, delay = 400) {
  const key = useMemo(() => JSON.stringify(config ?? null), [config])
  const [state, setState] = useState({ status: 'idle', command: '', error: null })
  const abortRef = useRef(null)

  useEffect(() => {
    if (!enabled || !engine) return undefined
    setState(s => ({ ...s, status: 'loading' }))
    const timer = setTimeout(() => {
      abortRef.current?.abort()
      const ctl = new AbortController()
      abortRef.current = ctl
      engineSettingsApi.commandPreview(engine, JSON.parse(key), { signal: ctl.signal })
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
  }, [engine, key, enabled, delay])

  useEffect(() => () => abortRef.current?.abort(), [])
  return state
}
