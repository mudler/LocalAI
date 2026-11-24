import { useAuth } from '../../context/AuthContext'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { createActionBook, effectOutcome } from './actionBook'
import { runLifecycle, settleOperation } from './lifecycleFlow'
import { runInstallAndTurnOn } from './installAndTurnOn'
import { hostGrantsApi } from '../../utils/addonsClient'

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

  const { user, isAdmin, sessionEpoch } = useAuth()
  const actor = JSON.stringify([user?.id, isAdmin, sessionEpoch])
  const authority = useRef(null)
  if (authority.current?.actor !== actor) {
    authority.current = { actor, controller: new AbortController() }
  }
  const origin = authority.current
  const current = useCallback(() => authority.current === origin && !origin.controller.signal.aborted, [origin])
  const book = useRef(null)
  if (book.current?.actor !== actor) { book.current = createActionBook(); book.current.actor = actor }
  const guardedApi = useMemo(() => ({ ...api, operationStatus: (...args) => {
    if (!current()) throw new DOMException('Aborted', 'AbortError')
    return api.operationStatus(...args)
  } }), [api, current])
  const settled = useRef(onSettled)
  settled.current = onSettled
  const [storedState, saveState] = useState({ actor, value: {} })
  const state = storedState.actor === actor ? storedState.value : {}
  const setState = useCallback(value => {
    if (!current()) return
    saveState(old => ({ actor, value: typeof value === 'function' ? value(old.actor === actor ? old.value : {}) : value }))
  }, [actor, current])
  const [storedUnknown, saveUnknown] = useState({ actor, value: {} })
  const unknown = storedUnknown.actor === actor ? storedUnknown.value : {}
  const setUnknown = useCallback(value => {
    if (!current()) return
    saveUnknown(old => ({ actor, value: typeof value === 'function' ? value(old.actor === actor ? old.value : {}) : value }))
  }, [actor, current])
  const [storedFlows, saveFlows] = useState({ actor, value: {} })
  const flows = storedFlows.actor === actor ? storedFlows.value : {}
  const setFlows = useCallback(value => {
    if (!current()) return
    saveFlows(old => ({ actor, value: typeof value === 'function' ? value(old.actor === actor ? old.value : {}) : value }))
  }, [actor, current])

  useEffect(() => {
    if (origin.controller.signal.aborted) origin.controller = new AbortController()
    return () => origin.controller.abort()
  }, [origin])

  const setUnknownFor = useCallback((addonId, value) => {
    setUnknown(u => {
      const next = { ...u }
      if (value) next[addonId] = value
      else delete next[addonId]
      return next
    })
  }, [setUnknown])

  // Turns a lifecycle outcome into the final result and updates the book.
  const conclude = useCallback(async (addonId, key, operationId, outcome, verify, signal) => {
    if (!current()) return null
    let verified
    if (outcome === 'completed' && verify) {
      try { verified = Boolean(await verify(signal)) } catch (e) {
        if (e && e.name === 'AbortError') throw e
        verified = undefined // an unreadable list proves nothing either way
      }
    }
    if (!current()) return null
    const final = effectOutcome(outcome, verified)
    if (final === 'unknown') {
      book.current.markUnknown(addonId, key)
      setUnknownFor(addonId, { key, operationId })
    } else {
      book.current.resolve(addonId, key)
      setUnknownFor(addonId, null)
    }
    return { outcome: final, operationId }
  }, [setUnknownFor, current])

  const run = useCallback(async (key, { addonId, submit, verify }) => {
    if (!current()) return null
    const signal = origin.controller.signal
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
      const out = await runLifecycle(guardedApi, { operationId, addonId, signal, submit: () => submit(operationId, signal) })
      if (!current()) return null
      result = await conclude(addonId, key, operationId, out.outcome, verify, signal)
    } catch (e) {
      if (!current() || (e && e.name === 'AbortError')) return null
      const kind = e && e.kind ? e.kind : 'unavailable'
      // A refusal is final. An owner or network failure proved nothing, so the
      // identity is kept and a retry replays the same operation.
      if (kind === 'denied' || kind === 'invalid' || kind === 'auth') book.current.resolve(addonId, key)
      result = { outcome: kind === 'network' ? 'unavailable' : kind, operationId, error: e }
    }
    if (!current()) return null
    setState(s => ({ ...s, [key]: result }))
    if (settled.current) settled.current(key, result)
    return result
  }, [guardedApi, conclude, setUnknownFor, current, setState, origin])

  // Reads the unknown operation once, by its own identity. Never submits.
  const checkStatus = useCallback(async (addonId) => {
    if (!current()) return null
    const held = book.current.pending(addonId)
    if (!held || !held.unknown) return null
    if (!current()) return null
    const signal = origin.controller.signal
    const { key, operationId } = held
    setState(s => ({ ...s, [key]: { phase: 'working', operationId } }))
    let result
    try {
      const out = await settleOperation(guardedApi, { operationId, addonId, signal, waits: [] })
      if (!current()) return null
      result = await conclude(addonId, key, operationId, out.outcome, held.meta && held.meta.verify, signal)
    } catch (e) {
      if (!current() || (e && e.name === 'AbortError')) return null
      // A lost read leaves the outcome as unknown.
      result = { outcome: e && e.kind === 'denied' ? 'denied' : 'unknown', operationId, error: e }
    }
    if (!current()) return null
    setState(s => ({ ...s, [key]: result }))
    if (settled.current) settled.current(key, result)
    return result
  }, [guardedApi, conclude, current, setState, origin])

  // The user gives up on an unknown outcome. The next action starts a new
  // operation; the page says the earlier one may still be applied.
  const dismiss = useCallback((addonId) => {
    if (!current()) return null
    const held = book.current.pending(addonId)
    if (!held) return
    book.current.resolve(addonId, held.key)
    setUnknownFor(addonId, null)
    setState(s => {
      const next = { ...s }
      delete next[held.key]
      return next
    })
  }, [setUnknownFor, current, setState])

  // Install an add-on and turn it on as one action of the user. Each step is
  // an ordinary action with its own operation identity, so an unknown outcome
  // keeps the rules above. flows[entry.key] says where it is: step 'install'
  // or 'turn-on' while it runs, 'done' at the end, or 'failed' with the step
  // that failed and the outcome. `skipInstall` turns on an add-on that is
  // already installed (the retry after a failed turn on).
  const installAndTurnOn = useCallback((entry, options = {}) => {
    if (!current()) return Promise.resolve(null)
    const flow = `flow:${entry.key}`
    return runInstallAndTurnOn({ run, api, adapter, hostGrants: hostGrantsApi, mark: (value) => setFlows(f => ({ ...f, [flow]: value })) }, entry, options)
  }, [run, api, adapter, current, setFlows])

  const clearFlow = useCallback((entry) => {
    setFlows(f => {
      const next = { ...f }
      delete next[`flow:${entry.key}`]
      return next
    })
  }, [setFlows])

  const clear = useCallback((key) => {
    setState(s => {
      const next = { ...s }
      delete next[key]
      return next
    })
  }, [setState])

  return { state, unknown, flows, run, installAndTurnOn, clearFlow, checkStatus, dismiss, clear, book: book.current }
}
