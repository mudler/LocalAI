import { useCallback, useEffect, useMemo, useState } from 'react'
import { useDelayedAction } from '../../hooks/useDelayedAction'
import { loadList, missingFromServer, saveList } from '../../utils/identity'

// The people this browser enrolled, plus what the last search learned about
// the server. The server has no list call, so the list is a local record: it
// can be out of date after a restart, and the only evidence is a search that
// asked for everyone and did not get someone back.
//
//   storageKey   localStorage key of the saved list
//   forget       the real call, `({ id }) => Promise`
//   onForgot     (entry, outcome) after the call: 'gone', 'already-gone' or an Error
export function useRegistry({ storageKey, forget, onForgot }) {
  const [entries, setEntries] = useState(() => loadList(storageKey))
  const [search, setSearch] = useState(null)
  const [latest, setLatest] = useState(null)

  useEffect(() => { saveList(storageKey, entries) }, [storageKey, entries])

  const run = useCallback(async (_key, entry) => {
    try {
      await forget({ id: entry.id })
      setEntries(prev => prev.filter(e => e.id !== entry.id))
      setSearch(prev => (prev ? { ...prev, extras: prev.extras.filter(e => e.id !== entry.id) } : prev))
      onForgot?.(entry, 'gone')
    } catch (err) {
      if (err?.status === 404) {
        setEntries(prev => prev.filter(e => e.id !== entry.id))
        onForgot?.(entry, 'already-gone')
      } else {
        onForgot?.(entry, err)
      }
    }
  }, [forget, onForgot])

  const forgetting = useDelayedAction(run)

  const add = useCallback((entry) => setEntries(prev => [entry, ...prev.filter(e => e.id !== entry.id)]), [])

  // Swap a row for the same person under the id the server just made.
  const replace = useCallback((oldId, entry) => setEntries(prev => prev.map(e => (e.id === oldId ? entry : e))), [])

  const schedule = useCallback((entry) => {
    setLatest(entry)
    forgetting.schedule(entry.id, entry)
  }, [forgetting])

  const undo = useCallback((id) => {
    forgetting.cancel(id)
    setLatest(prev => (prev?.id === id ? null : prev))
  }, [forgetting])

  const finish = useCallback((id) => {
    forgetting.flush(id)
    setLatest(prev => (prev?.id === id ? null : prev))
  }, [forgetting])

  // Record what a search returned. `asked` is the top_k it sent.
  const noteSearch = useCallback((matches, asked) => {
    const ids = matches.map(m => m.id)
    const known = new Set(entries.map(e => e.id))
    setSearch({
      ids,
      complete: matches.length < asked,
      extras: matches.filter(m => !known.has(m.id)).map(m => ({ id: m.id, name: m.name || m.id, labels: m.labels || {} })),
    })
  }, [entries])

  const missing = useMemo(() => missingFromServer(entries, search), [entries, search])

  return {
    entries, add, replace, missing, searched: !!search, extras: search?.extras || [],
    waiting: forgetting.waiting, latest: latest && forgetting.waiting[latest.id] ? latest : null,
    schedule, undo, finish, noteSearch,
  }
}
