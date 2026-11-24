import { useCallback, useEffect, useReducer, useRef } from 'react'
import { updateRunsApi } from '../utils/api'
import { initialRunState, isRunning, requestItemOf, runsUnavailable, startItems, upgradeRunReducer } from '../utils/upgradeRun'

const POLL_MS = 2000

// The refusal of the server: { code, key, message } in the body of the answer.
const codeOf = (e) => (e && e.body && typeof e.body.code === 'string' ? e.body.code : '')
const messageOf = (e) => (e && e.body && typeof e.body.message === 'string' && e.body.message) || (e && e.message) || ''
// With no run going, a run started elsewhere (another tab, an automatic pass)
// is looked for now and then.
const IDLE_MS = 15000

// The update run of the Updates page. The server owns the run; this hook asks
// for the plan of a review, starts the run, reads it every two seconds while it
// runs, and sends cancel, retry and dismiss. Closing the page does not stop a
// run, and a reload finds it again: the page reads the newest run that is not
// dismissed (or the one named by `runId`) from the server.
//
// onEnded() is called once when a run this hook watched stops running, so the
// page can read the lists again.
export function useUpgradeRun({ runId = '', onEnded, pollMs = POLL_MS, idleMs = IDLE_MS } = {}) {
  const [state, dispatch] = useReducer(upgradeRunReducer, initialRunState)
  const stateRef = useRef(state)
  stateRef.current = state
  const endedRef = useRef(onEnded)
  endedRef.current = onEnded
  const alive = useRef(true)
  useEffect(() => { alive.current = true; return () => { alive.current = false } }, [])
  const send = useCallback((action) => { if (alive.current) dispatch(action) }, [])

  // The run to show: the one the address names, else the first the server
  // lists. The list leaves out dismissed runs and puts the running one first,
  // so a run that ended stays on the page, also after a reload, until it is
  // dismissed.
  const load = useCallback(async () => {
    try {
      if (runId) {
        send({ type: 'run', run: await updateRunsApi.get(runId) })
      } else {
        const out = await updateRunsApi.list(1)
        const runs = out && Array.isArray(out.runs) ? out.runs : []
        send({ type: 'run', run: runs[0] || null })
      }
    } catch (e) {
      send({ type: 'loadFailed', unavailable: runsUnavailable(e) })
    }
  }, [runId, send])
  useEffect(() => { load() }, [load])

  const run = state.run
  const running = isRunning(run)
  const runKey = run ? run.id : ''

  // Looks for a run that runs now and shows it when it is not the shown one.
  // The idle poll calls it, and so does the page right after an action that
  // can start a run of one (a model row's Upgrade or Retry, an add-on's
  // approved upgrade), so the run shows at once and not at the next idle look.
  const refresh = useCallback(async () => {
    try {
      const out = await updateRunsApi.active()
      const next = out && Array.isArray(out.runs) ? out.runs.find(isRunning) : null
      const cur = stateRef.current.run
      if (next && (!cur || cur.id !== next.id)) send({ type: 'run', run: next })
    } catch { /* nothing to show */ }
  }, [send])

  // Poll the run while it runs; otherwise look for a new one now and then.
  useEffect(() => {
    if (!state.loaded) return undefined
    let timer = null
    const tick = async () => {
      if (running) {
        try {
          send({ type: 'run', run: await updateRunsApi.get(runKey), from: 'poll' })
        } catch { /* the next tick asks again */ }
      } else {
        await refresh()
      }
    }
    timer = setInterval(tick, running ? pollMs : idleMs)
    return () => clearInterval(timer)
  }, [state.loaded, running, runKey, run, pollMs, idleMs, send, refresh])

  // The end of a run the page watched: the lists change now.
  const wasRunning = useRef(false)
  useEffect(() => {
    if (wasRunning.current && !running && endedRef.current) endedRef.current(run)
    wasRunning.current = running
  }, [running, run])

  const openReview = useCallback(async (rows, { single = false } = {}) => {
    send({ type: 'reviewOpen', single })
    try {
      send({ type: 'reviewReady', preview: await updateRunsApi.preview(rows.map(requestItemOf)) })
    } catch (e) {
      send({ type: 'reviewFailed', error: runsUnavailable(e) ? '' : messageOf(e), code: runsUnavailable(e) ? 'unavailable' : codeOf(e) })
    }
  }, [send])

  const start = useCallback(async () => {
    const items = startItems(stateRef.current.review)
    if (items.length === 0 || stateRef.current.busy) return null
    send({ type: 'busy', busy: 'start' })
    try {
      const out = await updateRunsApi.start(items)
      send({ type: 'started', run: out })
      return out
    } catch (e) {
      send({ type: 'startFailed', code: codeOf(e), error: messageOf(e) })
      // Another tab or an automatic pass started a run: show it.
      if (codeOf(e) === 'run_active') load()
      return null
    }
  }, [send, load])

  // One call on the shown run that answers the run to show: the same run, or
  // for a retry a new run (the old one stays finished).
  const act = useCallback(async (busy, call) => {
    const cur = stateRef.current.run
    if (!cur || stateRef.current.busy) return
    send({ type: 'busy', busy })
    try {
      const out = await call(cur.id)
      if (out && typeof out === 'object' && out.id) send({ type: 'run', run: out })
      send({ type: 'busy', busy: '' })
      return out && out.id ? out : null
    } catch (e) {
      send({ type: 'error', error: messageOf(e), code: codeOf(e) })
      return null
    }
  }, [send])

  const cancel = useCallback(() => act('cancel', id => updateRunsApi.cancel(id)), [act])
  const cancelItem = useCallback((key) => act('cancel', id => updateRunsApi.cancelItem(id, key)), [act])
  const retry = useCallback(() => act('retry', id => updateRunsApi.retry(id)), [act])
  const dismiss = useCallback(async () => {
    const cur = stateRef.current.run
    if (!cur || stateRef.current.busy) return
    send({ type: 'busy', busy: 'dismiss' })
    try {
      await updateRunsApi.dismiss(cur.id)
      send({ type: 'dismissed' })
    } catch (e) {
      send({ type: 'error', error: messageOf(e), code: codeOf(e) })
    }
  }, [send])

  return {
    run,
    running,
    loaded: state.loaded,
    unavailable: state.unavailable,
    busy: state.busy,
    error: state.error,
    errorCode: state.errorCode,
    review: state.review,
    openReview,
    closeReview: useCallback(() => send({ type: 'reviewClose' }), [send]),
    toggle: useCallback((key) => send({ type: 'toggle', key }), [send]),
    selectAll: useCallback(() => send({ type: 'selectAll' }), [send]),
    selectNone: useCallback(() => send({ type: 'selectNone' }), [send]),
    start,
    refresh,
    cancel,
    cancelItem,
    retry,
    dismiss,
  }
}
