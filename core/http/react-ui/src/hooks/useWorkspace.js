import { useCallback, useEffect, useRef, useState } from 'react'
import { filesApi } from '../utils/api'

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
  const [error, setError] = useState('')
  const [off, setOff] = useState('')
  const [trash, setTrash] = useState([])
  const [history, setHistory] = useState([])
  const [hits, setHits] = useState(null) // null: no search; an array: its results
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

  const refresh = useCallback(async (at = pathRef.current) => {
    setLoading(true)
    try {
      const [list, tr] = await Promise.all([filesApi.list(at), filesApi.trash()])
      if (pathRef.current !== at) return
      setEntries(list.entries || [])
      setTrash(tr.entries || [])
      setOff('')
      setError('')
    } catch (e) {
      fail(e)
    } finally {
      setLoading(false)
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
    setPath(next)
    pathRef.current = next
    setHits(null)
    refresh(next)
  }, [refresh])

  useEffect(() => { refresh(initialPath) }, [])  // eslint-disable-line react-hooks/exhaustive-deps

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

  const upload = useCallback((fileList) => act(async () => {
    const results = []
    for (const f of Array.from(fileList)) {
      results.push(await filesApi.upload(joinPath(pathRef.current, f.name), f))
    }
    return results
  }), [act])

  const mkdir = useCallback((name) => act(() => filesApi.mkdir(joinPath(pathRef.current, name))), [act])
  const rename = useCallback((entry, name) => act(() => filesApi.move(entry.path, joinPath(parentOf(entry.path), name))), [act])
  const remove = useCallback((entry) => act(() => filesApi.remove(entry.path)), [act])
  const restore = useCallback((rev) => act(async () => {
    const res = await filesApi.undo(rev)
    return res
  }), [act])

  const search = useCallback(async (text) => {
    if (!text.trim()) { setHits(null); return }
    try {
      const res = await filesApi.search(text.trim())
      setHits(res.hits || [])
    } catch (e) {
      fail(e)
    }
  }, [fail])

  return {
    path, entries, loading, error, off, trash, history, hits, busy,
    open, refresh, loadHistory, upload, mkdir, rename, remove, restore, search,
    clearError: () => setError(''), clearSearch: () => setHits(null),
  }
}
