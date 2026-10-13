import { useCallback, useEffect, useRef, useState } from 'react'
import { createOperationBook } from '../catalog/catalogState'
import { runLifecycle } from './lifecycleFlow'

// Runs user actions that change an add-on, one operation identity per action.
//
// `run(key, { addonId, submit })`: `key` names the action (for example
// "start:notes:3"). The same key keeps the same operation identity until the
// action reached a final answer, so a retry replays and is never a second
// operation. `submit(operationId, signal)` performs the single write.
//
// state[key] is { phase: 'working' } while it runs and then
// { outcome: 'completed'|'failed'|'unknown'|'denied'|'invalid'|'unavailable'|'auth' }.
export function useAddonActions(api, { onSettled } = {}) {
  const book = useRef(null)
  if (book.current === null) book.current = createOperationBook()
  const abort = useRef(null)
  const settled = useRef(onSettled)
  settled.current = onSettled
  const [state, setState] = useState({})

  useEffect(() => {
    abort.current = new AbortController()
    return () => abort.current.abort()
  }, [])

  const run = useCallback(async (key, { addonId, submit }) => {
    const signal = abort.current.signal
    const operationId = book.current.idFor(key)
    setState(s => ({ ...s, [key]: { phase: 'working', operationId } }))
    let result
    try {
      const out = await runLifecycle(api, { operationId, addonId, signal, submit: () => submit(operationId, signal) })
      if (out.outcome === 'completed' || out.outcome === 'failed') book.current.done(key)
      result = { outcome: out.outcome, operationId }
    } catch (e) {
      if (e && e.name === 'AbortError') return null
      const kind = e && e.kind ? e.kind : 'unavailable'
      // A refusal is final. An owner or network failure proved nothing, so the
      // identity is kept and a retry replays the same operation.
      if (kind === 'denied' || kind === 'invalid' || kind === 'auth') book.current.done(key)
      result = { outcome: kind === 'network' ? 'unavailable' : kind, operationId, error: e }
    }
    setState(s => ({ ...s, [key]: result }))
    if (settled.current) settled.current(key, result)
    return result
  }, [api])

  const clear = useCallback((key) => {
    setState(s => {
      const next = { ...s }
      delete next[key]
      return next
    })
  }, [])

  return { state, run, clear, book: book.current }
}
