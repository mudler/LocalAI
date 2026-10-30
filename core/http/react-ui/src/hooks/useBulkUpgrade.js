import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  beginRun, failedIds, initialSelection, runSequential, selectAllItems, summarize, toggleItem,
} from '../utils/bulkUpgrade'

// The "upgrade all" run of a page. The run lives here, in the page, not in the
// dialog: closing the dialog never cancels it and the page can still show the
// progress. `items` are the candidates ({ id, name, from, to, startable,
// reason }); `runOne(item, signal)` upgrades one and resolves { ok, message }.
export function useBulkUpgrade({ items, runOne }) {
  const [open, setOpen] = useState(false)
  const [picked, setPicked] = useState(null)
  const [run, setRun] = useState(null)
  // The ref answers a second click in the same tick, before React re-renders.
  const runRef = useRef(null)
  const runOneRef = useRef(runOne)
  runOneRef.current = runOne
  const abort = useRef(null)

  useEffect(() => {
    abort.current = new AbortController()
    return () => abort.current.abort()
  }, [])

  // Until the person picks, every item that can start is selected.
  const selected = useMemo(() => {
    const base = picked || initialSelection(items)
    return new Set(items.filter(i => i.startable && base.has(i.id)).map(i => i.id))
  }, [picked, items])

  const launch = useCallback((source, ids) => {
    const next = beginRun(runRef.current, source, ids)
    if (!next) return false
    runRef.current = next
    setRun(next)
    runSequential(next, (item, signal) => runOneRef.current(item, signal), {
      signal: abort.current.signal,
      onUpdate: (r) => { runRef.current = r; setRun(r) },
    })
    return true
  }, [])

  const start = useCallback(() => launch(items, selected), [launch, items, selected])
  const retry = useCallback(() => {
    const current = runRef.current
    if (!current) return false
    return launch(current.items, new Set(failedIds(current)))
  }, [launch])
  // Back to the selection, once nothing is running.
  const reset = useCallback(() => {
    if (runRef.current && runRef.current.phase === 'running') return
    runRef.current = null
    setRun(null)
    setPicked(null)
  }, [])

  return {
    open,
    show: () => setOpen(true),
    hide: () => setOpen(false),
    items,
    selected,
    toggle: (id) => setPicked(toggleItem(selected, items, id)),
    selectAll: () => setPicked(selectAllItems(items)),
    selectNone: () => setPicked(new Set()),
    run,
    running: Boolean(run && run.phase === 'running'),
    summary: summarize(run),
    failed: failedIds(run),
    start,
    retry,
    reset,
  }
}
