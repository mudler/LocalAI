import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { nodesApi } from '../utils/api'
import { useOperations } from './useOperations'
import { useUpdates } from '../contexts/UpdatesContext'
import { beginRun, failedIds, runSequential, summarize } from '../utils/bulkUpgrade'
import { runNodeRow } from '../utils/updateRun'

// The run of an update across nodes: one node after another, each result
// shown when it ends. A node is done only by the rule of utils/updateRun (the
// operation ended without an error and a fresh check shows the node on the new
// build). A node that fails does not stop the others. Retry runs the failed
// ones again. `entries` are the lines of utils/nodeUpdates.planEntries.
export function useNodeUpdateRun({ entries, onSettled }) {
  const { t } = useTranslation('updates')
  const updates = useUpdates()
  const { operations } = useOperations()
  const opsRef = useRef(operations)
  opsRef.current = operations
  const recheckRef = useRef(updates && updates.recheck)
  recheckRef.current = updates && updates.recheck
  const settledRef = useRef(onSettled)
  settledRef.current = onSettled
  const [run, setRun] = useState(null)
  const runRef = useRef(null)
  const stopRef = useRef(false)
  const alive = useRef(true)
  useEffect(() => { alive.current = true; return () => { alive.current = false } }, [])

  const wordsOf = useCallback((res) => {
    switch (res.code) {
      case 'start': return t('row.startFailed', { message: res.error })
      case 'unchanged': return t('row.unchanged')
      case 'unconfirmed': return t('row.unconfirmed')
      default: return t('row.failed', { message: res.error || t('row.noReason') })
    }
  }, [t])

  const runOne = useCallback(async (item) => {
    if (stopRef.current) return { ok: false, skipped: true }
    const res = await runNodeRow({
      entry: item,
      start: async (e) => { const out = await nodesApi.upgradeBackend(e.nodeId, e.backend); return out && (out.jobID || out.uuid) },
      getOperations: () => opsRef.current,
      recheck: () => (recheckRef.current ? recheckRef.current() : Promise.reject(new Error('no check'))),
    })
    return res.ok ? { ok: true } : { ok: false, message: wordsOf(res) }
  }, [wordsOf])

  const launch = useCallback((items, ids) => {
    const next = beginRun(runRef.current, items, ids)
    if (!next) return false
    stopRef.current = false
    runRef.current = next
    setRun(next)
    runSequential(next, runOne, {
      onUpdate: (r) => {
        runRef.current = r
        if (alive.current) setRun(r)
        if (r.phase === 'finished' && settledRef.current) settledRef.current(r)
      },
    })
    return true
  }, [runOne])

  const items = entries.map(e => ({ ...e, id: e.key, name: e.nodeName, startable: true }))
  const start = useCallback(() => launch(items, new Set(items.map(i => i.id))), [launch, items])
  // Without `ids`, every failed node; with them (a node's own Retry), those.
  const retry = useCallback((ids) => {
    const cur = runRef.current
    if (!cur) return false
    return launch(cur.items, new Set(Array.isArray(ids) ? ids : failedIds(cur)))
  }, [launch])
  // Nodes that have not started are skipped; the one in flight finishes.
  const stop = useCallback(() => { stopRef.current = true }, [])

  return {
    run,
    items,
    running: Boolean(run && run.phase === 'running'),
    summary: summarize(run),
    failed: failedIds(run),
    start,
    retry,
    stop,
  }
}
