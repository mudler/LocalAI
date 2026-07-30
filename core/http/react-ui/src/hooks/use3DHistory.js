import { useState, useEffect, useCallback, useMemo } from 'react'
import { generateId } from '../utils/format'

// use3DHistory — IndexedDB-backed history of 3D generations. Unlike
// useMediaHistory (localStorage, URL-only), entries here carry the generated
// GLB as a Blob: GLBs are multi-megabyte binaries the server may eventually
// clean up, and IndexedDB is the only browser store that handles blobs of
// that size. Blobs read back from IndexedDB are lazy handles (the bytes are
// not materialized into JS memory until used), so a single store is fine.
//
// Entry: { id, createdAt, name, model,
//          params: { seed, steps, textureSteps, guidance, quality, background },
//          inputThumb,  // small dataURL of the conditioning image
//          glb }        // Blob

const DB_NAME = 'localai-3d-history'
const DB_VERSION = 1
const STORE = 'generations'
const MAX_ENTRIES = 20

function openDb() {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, DB_VERSION)
    req.onupgradeneeded = () => {
      const db = req.result
      if (!db.objectStoreNames.contains(STORE)) {
        db.createObjectStore(STORE, { keyPath: 'id' }).createIndex('createdAt', 'createdAt')
      }
    }
    req.onsuccess = () => resolve(req.result)
    req.onerror = () => reject(req.error)
  })
}

const txDone = (tx) => new Promise((resolve, reject) => {
  tx.oncomplete = resolve
  tx.onerror = () => reject(tx.error)
  tx.onabort = () => reject(tx.error)
})

async function withStore(mode, fn) {
  const db = await openDb()
  try {
    const tx = db.transaction(STORE, mode)
    const result = fn(tx.objectStore(STORE))
    await txDone(tx)
    return result
  } finally {
    db.close()
  }
}

async function idbGetAll() {
  const req = await withStore('readonly', (store) => store.getAll())
  return (req.result || []).sort((a, b) => b.createdAt - a.createdAt)
}

// Insert + keep-newest-N eviction in one transaction so a crash between the
// two can't leave the store unbounded.
async function idbPutAndEvict(entry) {
  await withStore('readwrite', (store) => {
    store.put(entry)
    // getAllKeys on the createdAt index yields primary keys oldest-first.
    const keysReq = store.index('createdAt').getAllKeys()
    keysReq.onsuccess = () => {
      const excess = keysReq.result.length - MAX_ENTRIES
      for (let i = 0; i < excess; i++) store.delete(keysReq.result[i])
    }
  })
}

const idbDelete = (id) => withStore('readwrite', (store) => store.delete(id))
const idbClear = () => withStore('readwrite', (store) => store.clear())

export function use3DHistory() {
  const [entries, setEntries] = useState([])
  const [selectedId, setSelectedId] = useState(null)

  const refresh = useCallback(async () => {
    try {
      setEntries(await idbGetAll())
    } catch {
      // IndexedDB unavailable (private mode etc.) — degrade to session-only.
      setEntries((prev) => prev)
    }
  }, [])

  useEffect(() => { refresh() }, [refresh])

  const addEntry = useCallback(async ({ model, params, inputThumb, glb, name }) => {
    const entry = { id: generateId(), createdAt: Date.now(), model, params, inputThumb, glb, name }
    try {
      await idbPutAndEvict(entry)
      await refresh()
    } catch {
      setEntries((prev) => [entry, ...prev].slice(0, MAX_ENTRIES))
    }
    return entry
  }, [refresh])

  const deleteEntry = useCallback(async (id) => {
    setSelectedId((prev) => (prev === id ? null : prev))
    try {
      await idbDelete(id)
      await refresh()
    } catch {
      setEntries((prev) => prev.filter((e) => e.id !== id))
    }
  }, [refresh])

  const clearAll = useCallback(async () => {
    setSelectedId(null)
    try {
      await idbClear()
    } catch {
      // fall through to the local reset below
    }
    setEntries([])
  }, [])

  // Toggles: clicking the selected entry deselects it (back to latest result).
  const selectEntry = useCallback((id) => {
    setSelectedId((prev) => (prev === id ? null : id))
  }, [])

  const selectedEntry = useMemo(
    () => entries.find((e) => e.id === selectedId) || null,
    [entries, selectedId],
  )

  return { entries, addEntry, deleteEntry, clearAll, selectEntry, selectedId, selectedEntry }
}
