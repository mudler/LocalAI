// SPDX-License-Identifier: MIT
import { useCallback, useEffect, useRef, useState } from 'react'

// Every async turn has both cancellation and a generation fence: transports
// that finish despite abort cannot replace a newer read or an unmounted form.
export function useHostSettings(client, scope) {
  const [state, setState] = useState({ snapshot: null, draft: null, error: null, busy: false })
  const sequence = useRef(0)
  const controller = useRef(null)
  const pending = useRef(null)
  const begin = useCallback(() => {
    controller.current?.abort()
    controller.current = new AbortController()
    return { id: ++sequence.current, signal: controller.current.signal }
  }, [])
  const load = useCallback(async (preserve = false) => {
    const { id, signal } = begin()
    setState(s => ({ ...s, busy: true }))
    try {
      const snapshot = await client.read(signal)
      if (sequence.current !== id) return
      setState(s => {
        const edits = preserve && s.snapshot ? Object.fromEntries(Object.entries(s.draft).filter(([k, v]) => JSON.stringify(v) !== JSON.stringify(s.snapshot.policy[k]))) : {}
        return { snapshot, draft: { ...snapshot.policy, ...edits }, error: pending.current ? new Error('Write outcome is uncertain. Retry the same operation, or discard after reviewing owner state.') : null, busy: false }
      })
    } catch (error) { if (sequence.current === id) setState(s => ({ ...s, error, busy: false })) }
  }, [begin, client])
  useEffect(() => {
    pending.current = null
    setState({ snapshot: null, draft: null, error: null, busy: false })
    load()
    return () => { ++sequence.current; controller.current?.abort() }
  }, [load, scope])
  const save = async patch => {
    const { id, signal } = begin()
    pending.current ||= client.prepare(patch, state.snapshot.revision)
    setState(s => ({ ...s, busy: true, error: null }))
    try {
      await pending.current.update(signal)
      if (sequence.current !== id) return
      pending.current = null
      // Never trust a frontend copy after a write (including a replay).
      const snapshot = await client.read(signal)
      if (sequence.current === id) setState({ snapshot, draft: structuredClone(snapshot.policy), error: null, busy: false })
    } catch (error) {
      if (sequence.current !== id) return
      if (['revision_conflict', 'invalid', 'operation_mismatch'].includes(error.kind)) pending.current = null
      setState(s => ({ ...s, error, busy: false }))
    }
  }
  return { ...state, uncertain: !!pending.current, reload: () => load(true), save,
    setDraft: draft => setState(s => ({ ...s, draft: typeof draft === 'function' ? draft(s.draft) : draft })),
    // Discard is local only: neither an outage nor an uncertain operation is
    // reconciled by resetting the draft. Keep the error and retry identity.
    discard: () => setState(s => ({ ...s, draft: structuredClone(s.snapshot.policy) })),
  }
}
