import { useCallback, useEffect, useRef, useState } from 'react'
import { filesApi } from '../utils/api'
import { invalidate, useWorkspaceFeed } from './useWorkspaceFeed'

// The parent of a path inside the home: "/a/b" -> "/a", "/a" -> "/".
export const parentOf = (path) => {
  const i = path.lastIndexOf('/')
  return i <= 0 ? '/' : path.slice(0, i)
}

// "/a" + "b" -> "/a/b"; "/" + "b" -> "/b".
export const joinPath = (dir, name) => (dir === '/' ? `/${name}` : `${dir}/${name}`)

// State of the workspace page: the open folder, its entries, the Trash, the
// history of the folder and the search results. A 409 answer means files are
// off on this server; `off` then holds the reason.
export function useWorkspace(initialPath = '/') {
  const [path, setPath] = useState(initialPath)
  const [entries, setEntries] = useState([])
  const [loading, setLoading] = useState(true)
  const [loaded, setLoaded] = useState(false) // the open folder was read at least once
  const [loadError, setLoadError] = useState(null) // the read of the folder failed
  const [maxFileSize, setMaxFileSize] = useState(0)
  const [error, setError] = useState('')
  const [off, setOff] = useState('')
  const [trash, setTrash] = useState([])
  const [trashTotals, setTrashTotals] = useState({ files: 0, folders: 0, bytes: 0 })
  // What the last permanent delete removed: { files, folders, bytes } or null.
  const [purged, setPurged] = useState(null)
  const [history, setHistory] = useState([])
  const [hits, setHits] = useState(null) // null: no search; an array: its results
  // How many documents the last search could not look into yet: their text is
  // still being read on the server.
  const [indexing, setIndexing] = useState(0)
  const [busy, setBusy] = useState(false)
  const pathRef = useRef(path)
  pathRef.current = path

  // A failed call: files off, or an error to show.
  const fail = useCallback((e) => {
    if (e?.status === 409 && e.body?.error?.type === 'files_off') {
      setOff(e.body.error.message)
      return
    }
    setError(e?.message || 'The request failed.')
  }, [])

  // quiet skips the loading state: a refresh that the change feed starts must
  // not blank the list the user is looking at.
  const refresh = useCallback(async (at = pathRef.current, { quiet = false } = {}) => {
    if (!quiet) setLoading(true)
    try {
      const [list, tr] = await Promise.all([filesApi.list(at), filesApi.trash()])
      if (pathRef.current !== at) return
      setEntries(list.entries || [])
      setMaxFileSize(list.max_file_size || 0)
      setTrash(tr.entries || [])
      setTrashTotals(tr.totals || { files: 0, folders: 0, bytes: 0 })
      setOff('')
      setLoadError(null)
      setLoaded(true)
    } catch (e) {
      if (pathRef.current !== at) return
      if (e?.status === 409 && e.body?.error?.type === 'files_off') fail(e)
      // A quiet refresh that fails keeps the list on screen.
      else if (!quiet) setLoadError(e)
    } finally {
      if (pathRef.current === at) setLoading(false)
    }
  }, [fail])

  const loadHistory = useCallback(async () => {
    try {
      const h = await filesApi.history(pathRef.current)
      setHistory(h.entries || [])
    } catch (e) {
      fail(e)
    }
  }, [fail])

  const open = useCallback((next) => {
    if (next !== pathRef.current) {
      setEntries([])
      setLoaded(false)
    }
    setPath(next)
    pathRef.current = next
    setHits(null)
    refresh(next)
  }, [refresh])

  useEffect(() => { refresh(initialPath) }, [])  // eslint-disable-line react-hooks/exhaustive-deps

  // A change to the open folder, from another tab, a chat, a trigger or another
  // client, reads it again. The feed's paths have no leading slash.
  const folderGlob = path === '/' ? '*' : `${path.slice(1)}/*`
  useWorkspaceFeed([folderGlob], () => { refresh(pathRef.current, { quiet: true }) })

  // Runs a change, then reads the folder and the Trash again.
  const act = useCallback(async (fn) => {
    setBusy(true)
    setError('')
    try {
      const out = await fn()
      await refresh()
      return out
    } catch (e) {
      fail(e)
      return null
    } finally {
      setBusy(false)
    }
  }, [refresh, fail])

  // A new empty file, which the page then opens.
  const touch = useCallback((name) => act(async () => {
    const node = await filesApi.upload(joinPath(pathRef.current, name), new Blob([]))
    if (node?.path) invalidate(node.path, node.rev)
    return node
  }), [act])

  const mkdir = useCallback((name) => act(() => filesApi.mkdir(joinPath(pathRef.current, name))), [act])
  const rename = useCallback((entry, name) => act(() => filesApi.move(entry.path, joinPath(parentOf(entry.path), name))), [act])
  const remove = useCallback((entry) => act(() => filesApi.remove(entry.path)), [act])

  // move sends entries into the folder dest. It returns the revision of each
  // move, for Undo, or null when one failed (the error shows; the moves before
  // it stay).
  const move = useCallback((list, dest) => act(async () => {
    const revs = []
    for (const e of list) {
      const node = await filesApi.move(e.path, joinPath(dest, e.name))
      if (node?.rev) revs.push(node.rev)
    }
    return revs
  }), [act])

  // removeMany moves entries to the Trash and returns the revisions that undo
  // restores, newest first.
  const removeMany = useCallback((list) => act(async () => {
    for (const e of list) await filesApi.remove(e.path)
    const tr = await filesApi.trash()
    const gone = new Set(list.map(e => e.path))
    const revs = []
    for (const entry of tr.entries || []) {
      if ((entry.paths || []).some(p => gone.has(p))) {
        revs.push(entry.rev)
        for (const p of entry.paths || []) gone.delete(p)
      }
      if (gone.size === 0) break
    }
    return revs
  }), [act])

  // undoRevs reverts change sets, newest first, as Undo of a toast does.
  const undoRevs = useCallback((revs) => act(async () => {
    for (const rev of [...revs].sort((a, b) => b - a)) await filesApi.undo(rev)
    return true
  }), [act])
  const restore = useCallback((rev) => act(async () => {
    const res = await filesApi.undo(rev)
    return res
  }), [act])

  // Permanent deletes. The server counts what it removed; the page says so.
  const purge = useCallback((rev) => act(async () => {
    const res = await filesApi.purgeTrash(rev)
    setPurged(res)
    return res
  }), [act])
  const emptyTrash = useCallback(() => act(async () => {
    const res = await filesApi.emptyTrash()
    setPurged(res)
    return res
  }), [act])

  const search = useCallback(async (text) => {
    if (!text.trim()) { setHits(null); return }
    try {
      const res = await filesApi.search(text.trim())
      setHits(res.hits || [])
      setIndexing(res.indexing || 0)
    } catch (e) {
      fail(e)
    }
  }, [fail])

  return {
    path, entries, loading, loaded, loadError, maxFileSize, error, off, trash, trashTotals, purged, history, hits, indexing, busy,
    open, refresh, loadHistory, touch, mkdir, rename, remove, move, removeMany, undoRevs, restore, purge, emptyTrash, search,
    clearPurged: () => setPurged(null),
    clearError: () => setError(''), clearSearch: () => setHits(null),
  }
}
