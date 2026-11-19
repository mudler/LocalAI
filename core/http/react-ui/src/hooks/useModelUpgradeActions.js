import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { modelsApi } from '../utils/api'
import { adoptBody, adoptCandidates } from '../utils/modelUpdates'
import { useOperations } from './useOperations'

// What the Updates page and the Models page do to a model row: open the
// upgrade confirmation, start the upgrade it confirms, retry a failed one,
// cancel one that has not stopped the model yet, remove the old version an
// upgrade kept (after a confirmation) and set or release the hold. Each is a
// click; the read model is read again after each, so the row shows the new
// state at once.
//
// Adopt records where a model installed by an earlier release came from. It
// runs no check, so the hook runs one after it. With one gallery that lists the
// entry it just works; with several the server answers 409 with the
// candidates and the hook opens a dialog to pick one.
//
// `readModels` is the UpdatesContext reader. `addToast` (optional) shows the
// outcome of an adoption.
export function useModelUpgradeActions({ readModels, addToast }) {
  const { t } = useTranslation('updates')
  const { cancelOperation, refetch: refetchOperations } = useOperations()
  const [confirming, setConfirming] = useState(null)
  const [removing, setRemoving] = useState(null)
  // The adoption that needs a choice: { row, candidates }.
  const [adopting, setAdopting] = useState(null)
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

  const say = useCallback((message, type) => {
    setAnnouncement(message)
    if (addToast) addToast(message, type, undefined, { testId: 'model-adopt-toast' })
  }, [addToast])

  // Sends the adoption, then checks the model gallery and reads it again: the
  // adoption itself says nothing about updates. Throws what the API threw; a
  // failed check after a good adoption is said, not thrown.
  const adoptNow = useCallback(async (row, pick) => {
    mark(row.name, true)
    try {
      const out = await modelsApi.adopt(row.name, adoptBody(pick))
      const gallery = (out && out.gallery) || (pick && pick.gallery) || row.gallery || ''
      try {
        await modelsApi.checkUpdatesNow()
        say(t(out && out.filesMatch === false ? 'model.live.adoptedDiffers' : 'model.live.adopted', { name: row.name, gallery }), 'success')
      } catch (e) {
        say(t('model.live.adoptedNoCheck', { name: row.name, message: (e && e.message) || t('row.noReason') }), 'warning')
      }
      return out
    } finally {
      mark(row.name, false)
      await after()
    }
  }, [after, mark, say, t])

  // The Adopt button: the server picks the entry when exactly one gallery
  // lists it. More than one opens the dialog; any other refusal is said.
  const adopt = useCallback(async (row) => {
    try {
      await adoptNow(row, null)
    } catch (e) {
      const candidates = e && e.status === 409 ? adoptCandidates(e.body) : []
      if (candidates.length > 0) setAdopting({ row, candidates })
      else say(t('model.live.adoptFailed', { name: row.name, message: (e && e.message) || t('row.noReason') }), 'error')
    }
  }, [adoptNow, say, t])

  // The dialog's confirm. It throws so the dialog can show the refusal.
  const confirmAdopt = useCallback(async (pick) => {
    const row = adopting && adopting.row
    if (!row) return
    await adoptNow(row, pick)
    setAdopting(null)
  }, [adopting, adoptNow])

  const pending = useCallback((row) => pendingNames.has(row.name), [pendingNames])

  return {
    actions: { upgrade, retry, cancel, removeOld, setHold, adopt, pending },
    confirming,
    closeConfirm: () => setConfirming(null),
    start,
    removing,
    removingPending: removing ? pendingNames.has(removing.name) : false,
    closeRemove: () => setRemoving(null),
    confirmRemoveOld,
    adopting,
    closeAdopt: () => setAdopting(null),
    confirmAdopt,
    announcement,
  }
}
