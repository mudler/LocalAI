import { useCallback, useEffect, useRef, useState } from 'react'
import { createActionBook, effectOutcome } from './actionBook'
import { runLifecycle, settleOperation } from './lifecycleFlow'

// Runs user actions that change an add-on, one operation identity per action.
//
// `run(key, { addonId, submit, verify })`: `key` names the action (for example
// "start:notes:3") and `addonId` is the subject. The same key keeps the same
// operation identity for retries until the action reached a final answer;
// then the identity is dropped, so the next click is a new operation and is
// never answered from an old one. `submit(operationId, signal)` performs the
// single write. `verify(signal)` optionally reads the add-on afterwards and
// resolves false when a completed status did not change it.
//
// state[key] is { phase: 'working' } while it runs and then
// { outcome: 'completed'|'failed'|'unknown'|'not_applied'|'denied'|'invalid'|'unavailable'|'auth'|'blocked' }.
//
// unknown[addonId] is set while an outcome is unknown: the earlier operation
// may still be applied, so no other action on that add-on is submitted until
// `checkStatus(addonId)` reads it as final or the user calls `dismiss(addonId)`.
export function useAddonActions(api, { onSettled, adapter } = {}) {
  const book = useRef(null)
  if (book.current === null) book.current = createActionBook()
  const abort = useRef(null)
  const settled = useRef(onSettled)
  settled.current = onSettled
  const [state, setState] = useState({})
  const [unknown, setUnknown] = useState({})
  const [flows, setFlows] = useState({})

  useEffect(() => {
    abort.current = new AbortController()
    return () => abort.current.abort()
  }, [])

  const setUnknownFor = useCallback((addonId, value) => {
    setUnknown(u => {
      const next = { ...u }
      if (value) next[addonId] = value
      else delete next[addonId]
      return next
    })
  }, [])

  // Turns a lifecycle outcome into the final result and updates the book.
  const conclude = useCallback(async (addonId, key, operationId, outcome, verify, signal) => {
    let verified
    if (outcome === 'completed' && verify) {
      try { verified = Boolean(await verify(signal)) } catch (e) {
        if (e && e.name === 'AbortError') throw e
        verified = undefined // an unreadable list proves nothing either way
      }
    }
    const final = effectOutcome(outcome, verified)
    if (final === 'unknown') {
      book.current.markUnknown(addonId, key)
      setUnknownFor(addonId, { key, operationId })
    } else {
      book.current.resolve(addonId, key)
      setUnknownFor(addonId, null)
    }
    return { outcome: final, operationId }
  }, [setUnknownFor])

  const run = useCallback(async (key, { addonId, submit, verify }) => {
    const signal = abort.current.signal
    const begun = book.current.begin(addonId, key, { verify })
    if (begun.blocked) {
      const result = { outcome: 'blocked', operationId: begun.operationId }
      setState(s => ({ ...s, [key]: result }))
      return result
    }
    const operationId = begun.operationId
    setUnknownFor(addonId, null)
    setState(s => ({ ...s, [key]: { phase: 'working', operationId } }))
    let result
    try {
      const out = await runLifecycle(api, { operationId, addonId, signal, submit: () => submit(operationId, signal) })
      result = await conclude(addonId, key, operationId, out.outcome, verify, signal)
    } catch (e) {
      if (e && e.name === 'AbortError') return null
      const kind = e && e.kind ? e.kind : 'unavailable'
      // A refusal is final. An owner or network failure proved nothing, so the
      // identity is kept and a retry replays the same operation.
      if (kind === 'denied' || kind === 'invalid' || kind === 'auth') book.current.resolve(addonId, key)
      result = { outcome: kind === 'network' ? 'unavailable' : kind, operationId, error: e }
    }
    setState(s => ({ ...s, [key]: result }))
    if (settled.current) settled.current(key, result)
    return result
  }, [api, conclude, setUnknownFor])

  // Reads the unknown operation once, by its own identity. Never submits.
  const checkStatus = useCallback(async (addonId) => {
    const held = book.current.pending(addonId)
    if (!held || !held.unknown) return null
    const signal = abort.current.signal
    const { key, operationId } = held
    setState(s => ({ ...s, [key]: { phase: 'working', operationId } }))
    let result
    try {
      const out = await settleOperation(api, { operationId, addonId, signal, waits: [] })
      result = await conclude(addonId, key, operationId, out.outcome, held.meta && held.meta.verify, signal)
    } catch (e) {
      if (e && e.name === 'AbortError') return null
      // A lost read leaves the outcome as unknown.
      result = { outcome: e && e.kind === 'denied' ? 'denied' : 'unknown', operationId, error: e }
    }
    setState(s => ({ ...s, [key]: result }))
    if (settled.current) settled.current(key, result)
    return result
  }, [api, conclude])

  // The user gives up on an unknown outcome. The next action starts a new
  // operation; the page says the earlier one may still be applied.
  const dismiss = useCallback((addonId) => {
    const held = book.current.pending(addonId)
    if (!held) return
    book.current.resolve(addonId, held.key)
    setUnknownFor(addonId, null)
    setState(s => {
      const next = { ...s }
      delete next[held.key]
      return next
    })
  }, [setUnknownFor])

  // Install an add-on and turn it on as one action of the user. Each step is
  // an ordinary action with its own operation identity, so an unknown outcome
  // keeps the rules above. flows[entry.key] says where it is: step 'install'
  // or 'turn-on' while it runs, 'done' at the end, or 'failed' with the step
  // that failed and the outcome. `skipInstall` turns on an add-on that is
  // already installed (the retry after a failed turn on).
  const installAndTurnOn = useCallback(async (entry, { skipInstall = false } = {}) => {
    const flow = `flow:${entry.key}`
    const mark = (value) => setFlows(f => ({ ...f, [flow]: value }))
    if (!skipInstall) {
      mark({ step: 'install' })
      const installed = await run(`install:${entry.key}`, {
        addonId: entry.id,
        submit: (operationId, signal) => adapter.submitLifecycle({ operationId, action: 'install', addonId: entry.id, sourceId: entry.sourceId, version: entry.version }, signal),
        verify: async (signal) => (await api.installed(signal)).some(a => a.id === entry.id),
      })
      if (!installed) return null
      if (installed.outcome !== 'completed') { mark({ step: 'failed', failedStep: 'install', outcome: installed.outcome, error: installed.error }); return installed }
    }
    mark({ step: 'turn-on' })
    // Generation 0 sets no precondition: the add-on was just installed.
    const on = await run(`enable:${entry.id}:0`, {
      addonId: entry.id,
      submit: (operationId, signal) => adapter.submitLifecycle({ operationId, action: 'enable', addonId: entry.id, generation: 0 }, signal),
      verify: async (signal) => (await api.installed(signal)).some(a => a.id === entry.id && a.enabled === true),
    })
    if (!on) return null
    mark(on.outcome === 'completed' ? { step: 'done' } : { step: 'failed', failedStep: 'turn-on', outcome: on.outcome, error: on.error })
    return on
  }, [run, api, adapter])

  const clearFlow = useCallback((entry) => {
    setFlows(f => {
      const next = { ...f }
      delete next[`flow:${entry.key}`]
      return next
    })
  }, [])

  const clear = useCallback((key) => {
    setState(s => {
      const next = { ...s }
      delete next[key]
      return next
    })
  }, [])

  return { state, unknown, flows, run, installAndTurnOn, clearFlow, checkStatus, dismiss, clear, book: book.current }
}
