import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { backendsApi } from '../utils/api'
import { addonsApi, addonsCatalog } from '../utils/addonsClient'
import { addonUpgrades } from '../utils/addonUpgrades'
import { useOperations } from './useOperations'
import { useBulkUpgrade } from './useBulkUpgrade'
import { useAddonUpgrade } from '../components/addons/useAddonUpgrade'
import { useAddonActions } from '../components/addons/useAddonActions'
import { upgradeFailureWords } from '../components/addons/updateReview'
import { applyLocal, bulkItemsOf } from '../utils/updates'
import { runAddonRow, runGalleryRow, runLibraryRow } from '../utils/updateRun'
import { isClusterRow } from '../utils/nodeUpdates'

// Everything the Updates page does to a row: one update with real completion,
// the run of "Update ready", cancel, retry and the add-on dialogs. It owns what
// the lists cannot show yet (an update just started) and merges it into the
// rows. The words of a failure are built here, from the code the pure runner
// reports.
export function useUpdateActions({ updates }) {
  const { t } = useTranslation('updates')
  const { t: ta } = useTranslation('addons')
  const { operations, cancelOperation, dismissFailedOp } = useOperations()
  const opsRef = useRef(operations)
  opsRef.current = operations
  const [local, setLocalMap] = useState({})
  const [announcement, setAnnouncement] = useState('')
  const [review, setReview] = useState(null)
  const inFlight = useRef(new Set())
  const ctl = useRef(null)
  useEffect(() => {
    ctl.current = new AbortController()
    return () => ctl.current.abort()
  }, [])

  const { readAddons, recheck } = updates
  const setLocal = useCallback((id, value) => setLocalMap((m) => {
    const next = { ...m }
    if (value) next[id] = value
    else delete next[id]
    return next
  }), [])

  // The one add-on upgrade of this page: the store follows the owner's
  // operation, the confirmation is the same dialog as on the Apps page.
  const addonUpgrade = useAddonUpgrade(addonsApi, { onChanged: () => readAddons() })
  const addonActions = useAddonActions(addonsApi, { onSettled: () => readAddons(), adapter: addonsCatalog })

  const wordsOf = useCallback((res) => {
    switch (res.code) {
      case 'start': return t('row.startFailed', { message: res.error })
      case 'unchanged': return t('row.unchanged')
      case 'unconfirmed': return t('row.unconfirmed')
      default:
        return res.errorKey ? upgradeFailureWords(res.errorKey, ta) : t('row.failed', { message: res.error || t('row.noReason') })
    }
  }, [t, ta])

  // Updates one row and resolves { ok, message } only when the update is over
  // by the rule of utils/updateRun: done means the operation ended without an
  // error and a fresh check no longer lists the item.
  const runRow = useCallback(async (row, signal) => {
    if (inFlight.current.has(row.id)) return { ok: false, message: '' }
    // Never the plain fan-out for a backend that nodes report: it would stop
    // their models with no confirmation. The page sends those to the node dialog.
    if (isClusterRow(row)) return { ok: false, message: t('row.needsConfirm', { name: row.name }) }
    inFlight.current.add(row.id)
    const stop = signal || ctl.current.signal
    try {
      let res
      if (row.kind === 'addon') {
        res = await runAddonRow({
          row,
          upgrade: (rec) => addonUpgrade.upgrade(rec),
          reread: async () => { const list = await addonsApi.installed(); await readAddons(); return list },
        })
      } else if (row.kind === 'library') {
        setLocal(row.id, { phase: 'running', message: '', progress: 0 })
        res = await runLibraryRow({
          row,
          install: (r) => addonsApi.backends.install(
            { sourceId: r.sourceId, name: r.name, version: r.availableVersion }, stop,
            { onStatus: (job) => setLocal(row.id, { phase: 'running', message: job && job.message ? String(job.message) : '', progress: job && job.progress ? Math.round(job.progress) : 0 }) },
          ),
          recheck,
          signal: stop,
        })
      } else {
        // A failed operation stays in the list until it is dismissed: take the
        // old one away so the new one is the only one the row follows.
        if (row.jobID) await dismissFailedOp(row.jobID)
        setLocal(row.id, { phase: 'running', message: '', progress: 0 })
        res = await runGalleryRow({
          row,
          start: async (r) => { const out = await backendsApi.upgrade(r.name); return out && (out.jobID || out.uuid) },
          getOperations: () => opsRef.current,
          recheck,
          signal: stop,
        })
      }
      if (res.ok) {
        setLocal(row.id, null)
        setAnnouncement(t('live.updated', { name: row.name }))
        return { ok: true, message: '' }
      }
      const message = wordsOf(res)
      setLocal(row.id, row.kind === 'addon' && res.errorKey ? null : { phase: 'failed', message })
      setAnnouncement(t('live.failed', { name: row.name }))
      return { ok: false, message: `${row.name}: ${message}` }
    } catch (e) {
      if (e && e.name === 'AbortError') { setLocal(row.id, null); return { ok: false, message: '' } }
      const message = t('row.failed', { message: e && e.message ? e.message : t('row.noReason') })
      setLocal(row.id, { phase: 'failed', message })
      return { ok: false, message: `${row.name}: ${message}` }
    } finally {
      inFlight.current.delete(row.id)
    }
  }, [addonUpgrade, readAddons, recheck, dismissFailedOp, setLocal, t, wordsOf])

  const rows = useMemo(() => applyLocal(updates.rows, local), [updates.rows, local])

  // "Update ready": one run, one row after another. A row is done only when
  // runRow says so.
  const bulkItems = useMemo(() => bulkItemsOf(rows), [rows])
  const bulk = useBulkUpgrade({
    items: bulkItems,
    runOne: async (item, signal) => {
      const out = await runRow(item.row, signal)
      return out.ok ? { ok: true } : { ok: false, message: out.message }
    },
  })

  const busy = bulk.running

  // A single Update. A backend starts at once; an add-on asks its own
  // confirmation first (its data is backed up and migrated).
  const update = useCallback((row) => {
    if (row.kind === 'addon') addonUpgrade.ask(row.record)
    else runRow(row)
  }, [addonUpgrade, runRow])

  // The upgrade the confirmation dialog starts goes through runRow, so the add-on
  // is held to the same rule of completion as the backends.
  const confirmApi = useMemo(() => ({
    ...addonUpgrade,
    upgrade: (rec) => {
      const row = rows.find(r => r.kind === 'addon' && r.addonId === rec.id)
      return row ? runRow(row) : addonUpgrade.upgrade(rec)
    },
  }), [addonUpgrade, rows, runRow])

  const retry = useCallback((row) => {
    if (row.kind === 'addon' && row.jobID) addonUpgrades.retry(addonsApi, row.jobID)
    else runRow(row)
  }, [runRow])

  const cancel = useCallback((row) => {
    if (row.kind === 'addon') addonUpgrades.cancel(row.jobID)
    else if (row.jobID) cancelOperation(row.jobID)
  }, [cancelOperation])

  const check = useCallback((row) => { if (row.record) addonUpgrade.check(row.record) }, [addonUpgrade])
  const checking = useCallback((row) => addonUpgrade.checks[row.addonId]?.phase === 'working', [addonUpgrade.checks])

  return {
    rows, bulk, busy, update, retry, cancel, check, checking,
    review, setReview, addonActions, confirmApi, announcement,
  }
}
