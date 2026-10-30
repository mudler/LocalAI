import { useCallback, useEffect, useRef, useState } from 'react'
import { apiUrl } from '../utils/basePath'
import { createWellKnownApi, WELL_KNOWN_FILES } from '../utils/wellKnown'
import { useWorkspaceFeed, invalidate } from './workspaceFeed'

export const wellKnownApi = createWellKnownApi({ fetch: (...a) => window.fetch(...a), url: apiUrl })

const schemaCache = new Map()

// loadSchema fetches the JSON Schema of a kind once per page load.
export function loadSchema(name) {
  if (!schemaCache.has(name)) {
    const p = wellKnownApi.schema(name).catch((e) => { schemaCache.delete(name); throw e })
    schemaCache.set(name, p)
  }
  return schemaCache.get(name)
}

// fileOf is the path in the home that a document lives at.
function fileOf(name, file) {
  return file ? String(file).replace(/^\/+/, '') : WELL_KNOWN_FILES[name]
}

// useWellKnown reads one well-known file and keeps it fresh: it reads again
// when the change feed says the file changed, whoever wrote it. A save goes
// through the server, which validates it, and tells the feed of this tab.
//
// It returns { doc, error, loading, reload, save, revert }. doc is the server's
// answer (data, version, valid, errors, fallback, prefilled). An error of a
// save is thrown to the caller, which shows it next to the form.
export function useWellKnown(name, { file, enabled = true } = {}) {
  const [doc, setDoc] = useState(null)
  const [error, setError] = useState(null)
  const [loading, setLoading] = useState(enabled)
  const seq = useRef(0)
  const path = fileOf(name, file)

  const reload = useCallback(async () => {
    if (!enabled || !path) return
    const mine = ++seq.current
    try {
      const d = await wellKnownApi.get(name, file)
      if (mine === seq.current) { setDoc(d); setError(null) }
    } catch (e) {
      if (mine === seq.current) setError(e)
    } finally {
      if (mine === seq.current) setLoading(false)
    }
  }, [name, file, enabled, path])

  useEffect(() => { setLoading(enabled); reload() }, [reload, enabled])
  useWorkspaceFeed(path ? [path] : [], () => { reload() })

  // version overrides the version of the last read, for a save that answers a
  // conflict on top of the newer file.
  const save = useCallback(async (values, { replace = false, version } = {}) => {
    const d = await wellKnownApi.put(name, values, { version: version ?? doc?.version ?? 0, replace, file })
    seq.current++
    setDoc(d)
    setError(null)
    invalidate(path)
    return d
  }, [name, file, doc?.version, path])

  const revert = useCallback(async () => {
    const d = await wellKnownApi.revert(name, file)
    seq.current++
    setDoc(d)
    invalidate(path)
    return d
  }, [name, file, path])

  return { doc, error, loading, reload, save, revert, path }
}
