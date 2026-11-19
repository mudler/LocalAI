import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { modelsApi } from '../utils/api'
import { useOperations } from './useOperations'

// What the Updates page and the Models page do to a model row: open the
// upgrade confirmation, start the upgrade it confirms, retry a failed one,
// cancel one that has not stopped the model yet, remove the old version an
// upgrade kept (after a confirmation) and set or release the hold. Each is a
// click; the read model is read again after each, so the row shows the new
// state at once.
//
// `readModels` is the UpdatesContext reader.
export function useModelUpgradeActions({ readModels }) {
  const { t } = useTranslation('updates')
  const { cancelOperation, refetch: refetchOperations } = useOperations()
  const [confirming, setConfirming] = useState(null)
  const [removing, setRemoving] = useState(null)
  const [pendingNames, setPendingNames] = useState(() => new Set())
  const [announcement, setAnnouncement] = useState('')

  const mark = useCallback((name, on) => setPendingNames((prev) => {
    const next = new Set(prev)
    if (on) next.add(name)
    else next.delete(name)
    return next
  }), [])

  const after = useCallback(async () => {
    await Promise.allSettled([readModels ? readModels() : null, refetchOperations ? refetchOperations() : null])
  }, [readModels, refetchOperations])

  // Runs one call for a row, with its pending mark and its words.
  const run = useCallback(async (row, call, okWords) => {
    mark(row.name, true)
    try {
      const out = await call()
      if (okWords) setAnnouncement(okWords(out))
      return out
    } catch (e) {
      setAnnouncement(t('model.live.failed', { name: row.name, message: (e && e.message) || t('row.noReason') }))
      return null
    } finally {
      mark(row.name, false)
      await after()
    }
  }, [after, mark, t])

  const upgrade = useCallback((row) => setConfirming(row), [])

  // The dialog's confirm. It throws so the dialog can show a refusal (a 409
  // that names keys that still need a choice).
  const start = useCallback(async (row, body) => {
    mark(row.name, true)
    try {
      await modelsApi.upgrade(row.name, body)
      setAnnouncement(t('model.live.started', { name: row.name }))
    } finally {
      mark(row.name, false)
      await after()
    }
  }, [after, mark, t])

  const retry = useCallback((row) => run(row, () => modelsApi.retryUpgrade(row.name), () => t('model.live.started', { name: row.name })), [run, t])

  const cancel = useCallback((row) => { if (row.jobID) cancelOperation(row.jobID) }, [cancelOperation])

  const removeOld = useCallback((row) => setRemoving(row), [])
  const confirmRemoveOld = useCallback(async () => {
    const row = removing
    if (!row) return
    await run(row, () => modelsApi.removeOld(row.name), (out) => t('model.live.removed', { name: row.name, count: out && Array.isArray(out.removed) ? out.removed.length : 0 }))
    setRemoving(null)
  }, [removing, run, t])

  const setHold = useCallback((row, hold) => run(
    row,
    () => modelsApi.setHold(row.name, hold),
    () => (hold ? t('model.live.held', { name: row.name }) : t('model.live.released', { name: row.name })),
  ), [run, t])

  const pending = useCallback((row) => pendingNames.has(row.name), [pendingNames])

  return {
    actions: { upgrade, retry, cancel, removeOld, setHold, pending },
    confirming,
    closeConfirm: () => setConfirming(null),
    start,
    removing,
    removingPending: removing ? pendingNames.has(removing.name) : false,
    closeRemove: () => setRemoving(null),
    confirmRemoveOld,
    announcement,
  }
}
