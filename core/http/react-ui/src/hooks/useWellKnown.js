import { useCallback, useEffect, useSyncExternalStore } from 'react'
import { apiUrl } from '../utils/basePath'
import { createWellKnownApi, createWellKnownStore, wellKnownPath } from '../utils/wellKnown'
import { useWorkspaceFeed, invalidate } from './workspaceFeed'

export const wellKnownApi = createWellKnownApi({ fetch: (...a) => window.fetch(...a), url: apiUrl })

// One copy of each document for the tab, shared by every useWellKnown.
export const wellKnownStore = createWellKnownStore({ api: wellKnownApi, onOwnWrite: (path) => invalidate(path) })

const schemaCache = new Map()

// loadSchema fetches the JSON Schema of a kind once per page load.
export function loadSchema(name) {
  if (!schemaCache.has(name)) {
    const p = wellKnownApi.schema(name).catch((e) => { schemaCache.delete(name); throw e })
    schemaCache.set(name, p)
  }
  return schemaCache.get(name)
}

// useWellKnown reads one well-known file and keeps it fresh: it reads again
// when the change feed says the file changed, whoever wrote it. Every reader of
// one file in the tab shares one copy (wellKnownStore), so a save made in one
// place (the shortcuts editor, the Appearance tab, a pin) shows in all of them
// without a second read. A save goes through the server, which validates it,
// and tells the feed of this tab, so its echo is not read again.
//
// It returns { doc, error, loading, reload, save, revert }. doc is the server's
// answer (data, version, valid, errors, fallback, prefilled). An error of a
// save is thrown to the caller, which shows it next to the form.
export function useWellKnown(name, { file, enabled = true } = {}) {
  const path = wellKnownPath(name, file)
  const on = enabled && !!path
  const subscribe = useCallback((fn) => wellKnownStore.subscribe(name, file, fn), [name, file])
  const getSnapshot = useCallback(() => wellKnownStore.snapshot(name, file), [name, file])
  const snap = useSyncExternalStore(subscribe, getSnapshot)

  const reload = useCallback(async () => {
    if (on) await wellKnownStore.load(name, file)
  }, [name, file, on])

  useEffect(() => { reload() }, [reload])
  useWorkspaceFeed(on ? [path] : [], () => { reload() })

  // version overrides the version every reader shows, for a save that answers
  // a conflict on top of the newer file.
  const save = useCallback((values, { replace = false, version } = {}) => (
    wellKnownStore.save(name, values, { replace, version, file })
  ), [name, file])

  const revert = useCallback(() => wellKnownStore.revert(name, file), [name, file])

  return {
    doc: on ? snap.doc : null,
    error: on ? snap.error : null,
    loading: on && !snap.loaded,
    reload,
    save,
    revert,
    path,
  }
}
