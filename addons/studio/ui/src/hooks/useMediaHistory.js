import { useState, useCallback, useRef, useEffect, useMemo } from 'react'
import { generateId } from '../utils/format'
import { fetchUrl } from '../utils/api'
import { MEDIA_STORAGE_KEYS as STORAGE_KEYS, FAVOURITES_KEY, clearAllMediaHistory as clearStoredMediaHistory } from '../utils/mediaHistoryStore'
import {
  SYNC_EVENT, clearRemote, deleteRemote, historyStorageKey, saveRemote, setStudioHistoryTransport, syncStudioHistory,
} from '../utils/studioHistorySync'

export { FAVOURITES_KEY }

// The history sync reaches /api/studio/history with the signed-in person's own
// session, like every other core call of the Studio.
setStudioHistoryTransport(fetchUrl)

// Forget everything the Studio front page shows, here and, when the server
// keeps the history, there too (every workspace; 3D included, which the caller
// also clears in IndexedDB). Returns how many stored lists were removed.
export function clearAllMediaHistory() {
  const shown = Object.entries(readAllMediaHistory()).flatMap(([type, list]) => list.map(e => ({ id: e.id, type })))
  let removed = clearStoredMediaHistory()
  for (const [mediaType, base] of Object.entries(STORAGE_KEYS)) {
    const key = historyStorageKey(mediaType)
    if (!key || key === base) continue
    try {
      if (localStorage.getItem(key) !== null) removed += 1
      localStorage.removeItem(key)
    } catch { /* storage unavailable: nothing to clear */ }
  }
  clearRemote('', shown)
  return removed
}

const SAVE_DEBOUNCE_MS = 500
const MAX_ENTRIES = 100
// A prompt is a sentence or a paragraph. This keeps one runaway paste from
// filling the quota that every other entry shares.
const MAX_PROMPT_CHARS = 2000

// Read every store at once, without mounting a hook per media type.
//
// The Studio overview reads across all of them and writes to none, while
// useMediaHistory() carries save timers and selection state it would have no
// use for. Note there is no 'threed' store here: 3D history lives in IndexedDB
// rather than localStorage because its entries carry multi-megabyte GLB blobs,
// so callers wanting 3D read use3DHistory alongside this.
export function readAllMediaHistory() {
  const all = {}
  for (const mediaType of Object.keys(STORAGE_KEYS)) {
    all[mediaType] = loadEntries(historyStorageKey(mediaType))
  }
  return all
}

// A null key means the server has not yet confirmed whose cache this browser
// holds: nothing is shown until it has.
function loadEntries(key) {
  if (!key) return []
  try {
    const stored = localStorage.getItem(key)
    if (stored) {
      const data = JSON.parse(stored)
      if (Array.isArray(data)) return data
    }
  } catch (_e) {
    localStorage.removeItem(key)
  }
  return []
}

function saveEntries(key, entries) {
  if (!key) return
  try {
    localStorage.setItem(key, JSON.stringify(entries))
  } catch (err) {
    if (err.name === 'QuotaExceededError' || err.code === 22) {
      console.warn('localStorage quota exceeded for media history')
    }
  }
}

export function useMediaHistory(mediaType) {
  const [storageKey, setStorageKey] = useState(() => historyStorageKey(mediaType))
  const [entries, setEntries] = useState(() => loadEntries(storageKey))
  const [selectedId, setSelectedId] = useState(null)
  const saveTimer = useRef(null)
  const dirty = useRef(false)
  const entriesRef = useRef(entries)
  entriesRef.current = entries
  // Entries made on this page, kept across a sync that lands after them.
  const addedIds = useRef(new Set())

  // Ask the server once per page load; when it answers, show its history
  // (merged into this account's browser cache by the sync).
  useEffect(() => {
    const reload = () => {
      const key = historyStorageKey(mediaType)
      setStorageKey(key)
      setEntries(prev => {
        const cached = loadEntries(key)
        const known = new Set(cached.map(e => e.id))
        const fresh = prev.filter(e => addedIds.current.has(e.id) && !known.has(e.id))
        if (fresh.length === 0) return cached
        dirty.current = true
        return [...fresh, ...cached].slice(0, MAX_ENTRIES)
      })
    }
    globalThis.addEventListener?.(SYNC_EVENT, reload)
    syncStudioHistory()
    return () => globalThis.removeEventListener?.(SYNC_EVENT, reload)
  }, [mediaType])

  // Debounced save — only when dirty
  useEffect(() => {
    if (!dirty.current) return
    clearTimeout(saveTimer.current)
    saveTimer.current = setTimeout(() => {
      saveEntries(storageKey, entries)
      dirty.current = false
    }, SAVE_DEBOUNCE_MS)
    return () => clearTimeout(saveTimer.current)
  }, [entries, storageKey])

  // Flush pending save on unmount
  useEffect(() => {
    return () => {
      clearTimeout(saveTimer.current)
      if (dirty.current) {
        saveEntries(storageKey, entriesRef.current)
      }
    }
  }, [storageKey])

  // parentId and edge link a result to the one it was made from (a hand-off
  // from another Studio workspace); they are written only when there is one.
  const addEntry = useCallback(({ prompt, model, params, results, parentId, edge }) => {
    const entry = {
      id: generateId(),
      prompt: typeof prompt === 'string' ? prompt.slice(0, MAX_PROMPT_CHARS) : prompt,
      model,
      params: params || {},
      results: results || [],
      createdAt: Date.now(),
      ...(parentId ? { parentId, edge: edge || 'take' } : {}),
    }
    dirty.current = true
    addedIds.current.add(entry.id)
    setEntries(prev => {
      const updated = [entry, ...prev]
      if (updated.length > MAX_ENTRIES) updated.length = MAX_ENTRIES
      return updated
    })
    saveRemote(mediaType, entry).then(version => {
      if (!version || version < 0) return
      dirty.current = true
      setEntries(prev => prev.map(e => (e.id === entry.id ? { ...e, serverVersion: version } : e)))
    })
    return entry.id
  }, [mediaType])

  const deleteEntry = useCallback((id) => {
    dirty.current = true
    setEntries(prev => prev.filter(e => e.id !== id))
    setSelectedId(prev => prev === id ? null : prev)
    deleteRemote(mediaType, id)
  }, [mediaType])

  const clearAll = useCallback(() => {
    dirty.current = true
    const removed = entriesRef.current.map(e => ({ id: e.id, type: mediaType }))
    setEntries([])
    setSelectedId(null)
    clearRemote(mediaType, removed)
  }, [mediaType])

  const selectEntry = useCallback((id) => {
    setSelectedId(prev => prev === id ? null : id)
  }, [])

  const selectedEntry = useMemo(
    () => entries.find(e => e.id === selectedId) || null,
    [entries, selectedId]
  )

  const historyProps = useMemo(() => ({
    entries,
    selectedId,
    onSelect: selectEntry,
    onDelete: deleteEntry,
    onClearAll: clearAll,
    mediaType,
  }), [entries, selectedId, selectEntry, deleteEntry, clearAll, mediaType])

  return { addEntry, selectEntry, selectedEntry, historyProps }
}
