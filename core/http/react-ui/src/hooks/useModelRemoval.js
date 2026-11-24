import { useCallback, useEffect, useRef, useState } from 'react'

// A removal with an undo window, done in the browser.
//
// The server has no "hold then delete" call, so the hold is a delay on this
// side: start() only hides the models and starts the window. Nothing is sent to
// the server until the window ends. Undo drops the batch and nothing was ever
// sent. If the page is closed or the user leaves it during the window, the batch
// is dropped as well and nothing is deleted: a delete the user can no longer
// see or undo must not happen.
//
// When the window ends, each model goes through the same delete call the row
// menu uses, one at a time. A model that started running in the meantime is
// skipped, not deleted.
//
//   deleteModel(id)   the existing delete call
//   loadRunning()     resolves to a Set of the model ids loaded right now
//   onSettled(result) { removed, failed: [{ id, message }], skipped: [id] }
//   onAbandoned(ids)  the page was left with a batch waiting
//
// Returns { pending, start, undo, commit }. pending is null, or
// { ids, items, phase: 'waiting' | 'removing' }.
export function useModelRemoval({ deleteModel, loadRunning, onSettled, onAbandoned }) {
  const [pending, setPending] = useState(null)
  const ref = useRef(null)
  const latest = useRef({})
  latest.current = { deleteModel, loadRunning, onSettled, onAbandoned }

  const start = useCallback((items) => {
    if (ref.current || items.length === 0) return false
    const batch = { ids: items.map(item => item.id), items, phase: 'waiting' }
    ref.current = batch
    setPending(batch)
    return true
  }, [])

  const undo = useCallback(() => {
    if (!ref.current || ref.current.phase !== 'waiting') return
    ref.current = null
    setPending(null)
  }, [])

  const commit = useCallback(async () => {
    const batch = ref.current
    if (!batch || batch.phase !== 'waiting') return
    batch.phase = 'removing'
    setPending({ ...batch })
    const removed = []
    const failed = []
    const skipped = []
    // Looked at once, when the window ends, not when the batch was chosen.
    let running = new Set()
    try {
      running = await latest.current.loadRunning()
    } catch {
      // Unknown. The delete call itself still refuses what the server refuses.
    }
    for (const id of batch.ids) {
      if (running.has(id)) {
        skipped.push(id)
        continue
      }
      try {
        await latest.current.deleteModel(id)
        removed.push(id)
      } catch (err) {
        failed.push({ id, message: err?.message || '' })
      }
    }
    ref.current = null
    setPending(null)
    latest.current.onSettled?.({ removed, failed, skipped })
  }, [])

  useEffect(() => () => {
    const batch = ref.current
    if (batch && batch.phase === 'waiting') {
      ref.current = null
      latest.current.onAbandoned?.(batch.ids)
    }
  }, [])

  return { pending, start, undo, commit }
}
