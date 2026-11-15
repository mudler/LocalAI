import { useCallback, useEffect, useRef, useState } from 'react'
import { modelsApi } from '../utils/api'
import { classifyImportJob } from '../utils/importJob'
import { remoteSearchParams, remoteAsks, remoteErrorKind, importBody, defaultVariantFile } from '../utils/remoteModels'

// How many remote estimates run at once. The server bounds its own upstream
// calls too; this keeps a page of rows from taking every browser connection.
const ESTIMATE_CONCURRENCY = 3
// After a provider says "slow down", wait this long before asking again.
const RATE_LIMIT_PAUSE_MS = 60_000
const JOB_POLL_MS = 1000

// The sources of the online search: the installed ones with their filters, and
// the person's enabled set. A server with no list (an older one) or a failed
// call leaves Hugging Face alone, which is what the panel always showed.
export function useSearchSources() {
  const [overview, setOverview] = useState(null)
  const [busy, setBusy] = useState(false)
  const alive = useRef(true)
  useEffect(() => () => { alive.current = false }, [])
  useEffect(() => {
    const ctl = new AbortController()
    modelsApi.searchSources({ signal: ctl.signal })
      .then(data => { if (!ctl.signal.aborted) setOverview(data) })
      .catch(() => {})
    return () => ctl.abort()
  }, [])
  // Stores the enabled set and keeps the list the server answers with.
  const setEnabled = useCallback(async (ids) => {
    setBusy(true)
    try {
      const data = await modelsApi.setSearchSources(ids)
      if (alive.current) setOverview(data)
      return null
    } catch (err) {
      return err
    } finally {
      if (alive.current) setBusy(false)
    }
  }, [])
  return { overview, busy, setEnabled }
}

const IDLE = { phase: 'idle', results: [], providers: [], error: '', unsupported: false, dropped: 0 }

// The searches of the online sources, one request each, so a slow or failed
// source never holds back another. `asks` lists the sources to ask now:
// { id, limit, filters, filterValues, reloadKey }. A source that is not
// listed, or that cannot answer a query with no term (see remoteAsks), is
// idle. `term` is already debounced by the caller.
//
// Results of the previous query stay on screen while the next one loads, so the
// table does not flash empty on every keystroke. A request is made again only
// when its own query changed.
export function useRemoteSearches({ asks, term, ggufOnly, sources }) {
  const [state, setState] = useState({})
  const live = useRef(new Map())

  const key = JSON.stringify([asks.map(a => [a.id, a.limit, a.filterValues, a.reloadKey]), term, ggufOnly])
  useEffect(() => {
    const wanted = new Set(asks.map(a => a.id))
    for (const [id, entry] of live.current) {
      if (!wanted.has(id)) {
        entry.ctl.abort()
        live.current.delete(id)
        setState(prev => ({ ...prev, [id]: IDLE }))
      }
    }
    for (const ask of asks) {
      const askKey = JSON.stringify([ask.limit, ask.filterValues, ask.reloadKey, term, ggufOnly])
      const prev = live.current.get(ask.id)
      if (prev && prev.key === askKey) continue
      prev?.ctl.abort()
      if (!remoteAsks(ask.id, term, sources)) {
        live.current.delete(ask.id)
        setState(p => ({ ...p, [ask.id]: IDLE }))
        continue
      }
      const ctl = new AbortController()
      live.current.set(ask.id, { key: askKey, ctl })
      setState(p => ({ ...p, [ask.id]: { ...(p[ask.id] || IDLE), phase: 'loading', error: '' } }))
      modelsApi.search(remoteSearchParams({ provider: ask.id, term, limit: ask.limit, ggufOnly, filters: ask.filters, filterValues: ask.filterValues }), { signal: ctl.signal })
        .then(data => {
          if (ctl.signal.aborted) return
          const status = (data?.providers || []).find(p => p.name === ask.id)
          setState(p => ({
            ...p,
            [ask.id]: {
              phase: status && status.ok === false ? 'failed' : 'done',
              results: data?.results || [],
              providers: data?.providers || [],
              error: status?.ok === false ? (status.error || '') : '',
              // The server did not ask the source: it cannot list its newest models.
              unsupported: !!status?.unsupported,
              dropped: status?.dropped || 0,
            },
          }))
        })
        .catch(err => {
          if (ctl.signal.aborted) return
          setState(p => ({ ...p, [ask.id]: { ...(p[ask.id] || IDLE), phase: 'failed', error: err?.message || String(err) } }))
        })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- `key` holds everything the effect reads from asks, term and ggufOnly
  }, [key])
  useEffect(() => () => { live.current.forEach(e => e.ctl.abort()); live.current.clear() }, [])

  return (id) => state[id] || IDLE
}

// What the inspector and the rows know about the repositories seen so far:
// the file listing (when a row is opened or installed) and the estimate (when
// a row scrolls into view), both keyed by provider:id. An answer is kept for
// the life of the page, so going back to a row costs nothing.
export function useRemoteDetails() {
  const [files, setFiles] = useState({})
  const [estimates, setEstimates] = useState({})
  const [pending, setPending] = useState(() => new Set())
  const filesRef = useRef(files)
  filesRef.current = files
  const asked = useRef(new Set())
  const queue = useRef([])
  const running = useRef(0)
  const pausedUntil = useRef(0)
  const alive = useRef(true)
  useEffect(() => () => { alive.current = false }, [])

  const settle = useCallback((key) => {
    setPending(prev => {
      if (!prev.has(key)) return prev
      const next = new Set(prev)
      next.delete(key)
      return next
    })
  }, [])

  const pump = useCallback(() => {
    while (running.current < ESTIMATE_CONCURRENCY && queue.current.length > 0) {
      if (Date.now() < pausedUntil.current) {
        // Everything queued waits out the pause: it is not an error of the row.
        const wait = pausedUntil.current - Date.now()
        setTimeout(() => alive.current && pump(), wait)
        return
      }
      const { key, uri, contexts } = queue.current.shift()
      running.current += 1
      modelsApi.remoteEstimate(uri, '', contexts)
        .then(est => { if (alive.current) setEstimates(prev => ({ ...prev, [key]: est })) })
        .catch(err => {
          if (remoteErrorKind(err) === 'rateLimited') {
            pausedUntil.current = Date.now() + RATE_LIMIT_PAUSE_MS
            // Ask again after the pause; the row stays "sizing".
            queue.current.push({ key, uri, contexts })
            return
          }
          if (alive.current) setEstimates(prev => ({ ...prev, [key]: { error: remoteErrorKind(err), estimates: {} } }))
        })
        .finally(() => {
          running.current -= 1
          if (alive.current && !queue.current.some(q => q.key === key)) settle(key)
          pump()
        })
    }
  }, [settle])

  // An estimate of the recommended variant, and of every variant with it. Asked
  // once per key; asked again only after a rate limit.
  const requestEstimate = useCallback((key, uri, contexts) => {
    if (asked.current.has(key)) return
    asked.current.add(key)
    setPending(prev => new Set(prev).add(key))
    queue.current.push({ key, uri, contexts })
    pump()
  }, [pump])

  // The lookups in flight, by key. A second ask for a listing that is still
  // loading (an install pressed right after the row was selected) waits for
  // that lookup instead of getting an empty answer.
  const inflight = useRef(new Map())
  const requestFiles = useCallback((key, uri) => {
    const cur = filesRef.current[key]
    if (cur?.data) return Promise.resolve(cur.data)
    if (inflight.current.has(key)) return inflight.current.get(key)
    if (cur && !cur.error) return Promise.resolve(cur.data)
    setFiles(prev => ({ ...prev, [key]: { loading: true } }))
    const lookup = modelsApi.remoteFiles(uri)
      .then(data => {
        if (alive.current) setFiles(prev => ({ ...prev, [key]: { data } }))
        return data
      })
      .catch(err => {
        if (alive.current) setFiles(prev => ({ ...prev, [key]: { error: remoteErrorKind(err), message: err?.message || '' } }))
        return null
      })
      .finally(() => { inflight.current.delete(key) })
    inflight.current.set(key, lookup)
    return lookup
  }, [])

  return { files, estimates, pending, requestFiles, requestEstimate }
}

// Imports started from the table, tracked by the job id the server answered
// with. The gallery matches progress by model name; an import's name is what
// the importer derived, which a row cannot know, so only the id is reliable.
export function useRemoteInstalls({ onDone }) {
  const [jobs, setJobs] = useState({})
  const ctls = useRef(new Map())
  const doneRef = useRef(onDone)
  doneRef.current = onDone

  useEffect(() => {
    const live = ctls.current
    return () => { live.forEach(c => c.abort()); live.clear() }
  }, [])

  const track = useCallback((key, jobID) => {
    const ctl = new AbortController()
    ctls.current.get(key)?.abort()
    ctls.current.set(key, ctl)
    const loop = async () => {
      let failures = 0
      while (!ctl.signal.aborted) {
        await new Promise(r => setTimeout(r, JOB_POLL_MS))
        if (ctl.signal.aborted) return
        let data
        try {
          data = await modelsApi.getJobStatus(jobID)
          failures = 0
        } catch (err) {
          failures += 1
          if (failures >= 10) {
            setJobs(prev => ({ ...prev, [key]: { ...prev[key], state: 'failed', error: err?.message || '' } }))
            return
          }
          continue
        }
        const status = classifyImportJob(data)
        const progress = Number.isFinite(data?.progress) ? data.progress : 0
        if (status.state === 'failed') {
          setJobs(prev => ({ ...prev, [key]: { ...prev[key], state: 'failed', error: status.message, progress } }))
          return
        }
        if (status.state === 'done') {
          setJobs(prev => ({ ...prev, [key]: { ...prev[key], state: 'done', name: status.name, progress: 100 } }))
          doneRef.current?.(key, status.name)
          return
        }
        setJobs(prev => ({ ...prev, [key]: { ...prev[key], state: 'running', progress } }))
      }
    }
    loop()
  }, [])

  // Starts the import of one variant. Resolves with an error when the server
  // refused it (an ambiguous backend, say); a started job is tracked.
  const start = useCallback(async (row, files, file) => {
    setJobs(prev => ({ ...prev, [row.name]: { state: 'starting', progress: 0 } }))
    try {
      const res = await modelsApi.importUri(importBody(row, files, file || defaultVariantFile(files)))
      const jobID = res?.uuid || res?.ID
      if (!jobID) throw new Error('No job ID returned from server')
      setJobs(prev => ({ ...prev, [row.name]: { state: 'running', jobID, progress: 0 } }))
      track(row.name, jobID)
      return null
    } catch (err) {
      const ambiguous = err?.status === 400 && err?.body?.error === 'ambiguous import'
      setJobs(prev => ({ ...prev, [row.name]: { state: 'failed', error: err?.message || String(err), ambiguous } }))
      return err
    }
  }, [track])

  const clear = useCallback((key) => {
    ctls.current.get(key)?.abort()
    ctls.current.delete(key)
    setJobs(prev => {
      const next = { ...prev }
      delete next[key]
      return next
    })
  }, [])

  return { jobs, start, clear }
}
