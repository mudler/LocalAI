import { useCallback, useEffect, useRef, useState } from 'react'
import { useOperations } from '../../hooks/useOperations'
import { hostGrantsApi } from '../../utils/addonsClient'
import { batchAccept, batchCurrent, batchDone, batchSkip, batchStart, busyAddonIds, isAddonInstallOp, queueBody, queueOutcome } from '../../utils/addonQueue'
import { newOperationId } from '../catalog/catalogState'

// The review of one or more add-ons and the request that queues them.
//
// `start(entries)` opens the review: the dialog shows each add-on with its own
// host access and the person accepts or skips it. After the last one, the
// accepted add-ons go to the owner in one request and then run in the
// background as operations (the rows of the queue). `notes` holds what the
// owner refused, one entry per refusal, until the person closes it.
//
// An install that failed is retried by taking a new review, never by sending
// the old approval again: its nonce is spent and the package may have changed.
export function useInstallQueue({ adapter, api = hostGrantsApi } = {}) {
  const { operations, refetch, dismissFailedOp } = useOperations()
  const [batch, setBatch] = useState(null)
  // The batch also lives in a ref: accept and skip decide from the latest one
  // and then send, which must not run inside a state updater.
  const held = useRef(null)
  const hold = useCallback((next) => { held.current = next; setBatch(next) }, [])
  const [review, setReview] = useState(false)
  const [notes, setNotes] = useState([])
  const [sending, setSending] = useState(false)
  const [tick, setTick] = useState(0)
  // The entries this page has seen, so a failed row can be reviewed again
  // without searching the library for it.
  const known = useRef(new Map())
  // The failed rows a new review replaces: dismissed once the new install is queued.
  const replaces = useRef(new Map())
  const busy = busyAddonIds(operations)

  // An install that leaves the list finished or was cancelled: the page reads
  // the installed add-ons again.
  const before = useRef(new Set())
  useEffect(() => {
    const now = new Set(operations.filter(op => isAddonInstallOp(op) && !op.error).map(op => op.jobID))
    let left = false
    for (const id of before.current) if (!now.has(id)) left = true
    before.current = now
    if (left) setTick(n => n + 1)
  }, [operations])

  const remember = useCallback((entries) => { for (const e of entries) known.current.set(e.id, e) }, [])

  const start = useCallback((entries, { replace } = {}) => {
    const next = batchStart(entries)
    if (next.entries.length === 0) return
    remember(next.entries)
    if (replace) replaces.current.set(replace.addonId, replace.jobID)
    setReview(Boolean(replace && replace.review))
    hold(next)
  }, [remember, hold])

  const close = useCallback(() => { hold(null); setReview(false) }, [hold])

  const note = useCallback((text, addonId = '') => {
    setNotes(list => [...list, { id: newOperationId(), addonId, name: known.current.get(addonId)?.title || addonId, text }])
  }, [])

  const send = useCallback(async (approvals) => {
    if (approvals.length === 0) return
    setSending(true)
    try {
      const out = await api.queue(queueBody(approvals))
      const { queued, refused } = queueOutcome(approvals, out)
      for (const q of queued) {
        const old = replaces.current.get(q.addonId)
        if (old) { replaces.current.delete(q.addonId); dismissFailedOp(old) }
      }
      for (const r of refused) note(r.error, r.addonId)
    } catch (error) {
      note(error, '')
    } finally {
      setSending(false)
      refetch()
    }
  }, [api, dismissFailedOp, refetch, note])

  const accept = useCallback((approval) => {
    const current = held.current
    if (!current) return
    const next = batchAccept(current, approval)
    if (!batchDone(next)) { hold(next); return }
    hold(null)
    send(next.accepted)
  }, [send, hold])

  const skip = useCallback(() => {
    const current = held.current
    if (!current) return
    const next = batchSkip(current)
    if (!batchDone(next)) { hold(next); return }
    hold(null)
    if (next.accepted.length > 0) send(next.accepted)
  }, [send, hold])

  // A failed row, or one that needs a new review, as a review of its add-on.
  // The library entry comes from memory, or from the library by the add-on id.
  const reviewOp = useCallback(async (op) => {
    const id = op.name
    let entry = known.current.get(id)
    if (!entry && adapter) {
      try {
        const found = await adapter.search({ text: id, page: 1 })
        entry = (found.entries || []).find(e => e.id === id)
      } catch { entry = undefined }
    }
    if (!entry) { note({ type: 'not_in_library', message: '' }, id); return }
    start([entry], { replace: { addonId: id, jobID: op.jobID, review: true } })
  }, [adapter, note, start])

  const dismissNote = useCallback((id) => setNotes(list => list.filter(n => n.id !== id)), [])
  const reviewId = useCallback((id) => reviewOp({ name: id }), [reviewOp])

  return {
    batch, current: batchCurrent(batch), review, notes, sending, tick, busy,
    start, accept, skip, close, send, reviewOp, reviewId, dismissNote,
  }
}
