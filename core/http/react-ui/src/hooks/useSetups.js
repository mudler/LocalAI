import { useCallback, useMemo, useRef, useState } from 'react'
import { useWellKnown, wellKnownStore } from './useWellKnown'
import { saveWithRetry } from '../utils/setupsSave'
import {
  cleanName, setupsData, withAdded, withDefault, withMoved, withRemoved, withReplaced,
} from '../utils/setups'

// useSetups is the list of saved setups of the person (LocalAI/setups.json)
// and the ways to change it. Every change is a save of the whole document; on
// a conflict it is applied again on top of the newer file, once.
//
// available is false until the file is read, when enabled is false and when
// the API answers with an error: the Home chip is then hidden. error is
// 'limit' (too many setups) or the server's words; a failed change leaves the
// list as it was and resolves to null.
export function useSetups({ enabled = true } = {}) {
  const { doc, loading, error: loadError, save } = useWellKnown('setups', { enabled })
  const [error, setError] = useState('')
  const data = useMemo(() => setupsData(doc), [doc])

  // applyNow runs one change of the data (null when it fails). The base and its
  // version come from the store together, at the time of the call (not from the
  // last render), so a change that starts before the previous one is shown
  // sends an old version, meets a conflict and goes on top of the newer file.
  const applyNow = useCallback(async (change) => {
    setError('')
    try {
      const doc = wellKnownStore.snapshot('setups').doc
      // A file that was never read counts as existing: nothing is created over it.
      return await saveWithRetry(save, setupsData(doc), (d, meta) => change(setupsData({ data: d }), meta), doc?.version ?? 0, doc ? !!doc.exists : true)
    } catch (e) {
      setError(e instanceof RangeError ? 'limit' : (e?.message || 'failed'))
      return null
    }
  }, [save])

  // Changes also run one after the other, so several quick ones do not
  // conflict with each other (only with another tab).
  const queue = useRef(Promise.resolve())
  const apply = useCallback((change) => {
    const run = queue.current.then(() => applyNow(change))
    queue.current = run
    return run
  }, [applyNow])

  const add = useCallback(async (setup) => {
    const saved = await apply((d) => withAdded(d, { ...setup, name: cleanName(setup?.name) }))
    return saved ? saved.setups[saved.setups.length - 1] : null
  }, [apply])
  // The first setup of a person: only while the file does not exist, decided
  // on the file the write goes on (so a retry after another tab wrote, a
  // deleted list and an invalid file all do nothing). It is also the default.
  const addFirst = useCallback(async (setup) => {
    const saved = await apply((d, { exists }) => {
      if (exists) return null
      const added = withAdded(d, { ...setup, name: cleanName(setup?.name) })
      return withDefault(added, added.setups[added.setups.length - 1].id)
    })
    return saved ? saved.setups[saved.setups.length - 1] : null
  }, [apply])
  const update = useCallback((id, capture) => apply((d) => withReplaced(d, id, {
    model: capture.model, effort: capture.effort, tools: capture.tools,
  })), [apply])
  const rename = useCallback((id, name) => {
    const clean = cleanName(name)
    if (!clean) return Promise.resolve(null)
    return apply((d) => withReplaced(d, id, { name: clean }))
  }, [apply])
  const remove = useCallback((id) => apply((d) => withRemoved(d, id)), [apply])
  const move = useCallback((id, delta) => apply((d) => withMoved(d, id, delta)), [apply])
  const setDefault = useCallback((id) => apply((d) => withDefault(d, id)), [apply])

  // Also false when a reload fails after a good read: the chip hides until it works again.
  const ready = enabled && !loading
  return {
    ready,
    available: ready && !loadError && !!doc,
    list: data.setups,
    defaultId: data.default,
    exists: ready && !!doc?.exists,
    add, addFirst, update, rename, remove, move, setDefault,
    error,
  }
}
