import { useCallback, useMemo, useRef, useState } from 'react'
import { useWellKnown } from './useWellKnown'
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
  const dataRef = useRef(data)
  dataRef.current = data

  // apply runs one change of the data (null when it fails).
  const apply = useCallback(async (change) => {
    setError('')
    try {
      return await saveWithRetry(save, dataRef.current, (d) => change(setupsData({ data: d })))
    } catch (e) {
      setError(e instanceof RangeError ? 'limit' : (e?.message || 'failed'))
      return null
    }
  }, [save])

  const add = useCallback(async (setup) => {
    const saved = await apply((d) => withAdded(d, { ...setup, name: cleanName(setup?.name) }))
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

  const ready = enabled && !loading
  return {
    ready,
    available: ready && !loadError && !!doc,
    list: data.setups,
    defaultId: data.default,
    add, update, rename, remove, move, setDefault,
    error,
  }
}
