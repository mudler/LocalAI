import { useCallback, useEffect, useState } from 'react'
import { modelsApi } from '../utils/api'
import { draftCandidates } from '../utils/drafting'
import { engineForBackend } from './useEngineSettings'

// The model file of each installed model, asked once per tab: the draft
// model key holds a file, and the picker shows models.
const fileByModel = new Map()

/** The model file (parameters.model) of an installed model, cached. */
export function modelFileOf(name) {
  if (!fileByModel.has(name)) {
    const p = modelsApi.getConfigJson(name).then(cfg => String(cfg?.parameters?.model || ''))
    fileByModel.set(name, p)
    p.catch(() => { if (fileByModel.get(name) === p) fileByModel.delete(name) })
  }
  return fileByModel.get(name)
}

/**
 * The installed models a draft model can come from for `model`, served by
 * the engine `engineId` (utils/drafting draftCandidates). With `file`, the
 * candidate whose model file it is, so a saved draft shows as its model.
 *
 * Returns { status: 'idle'|'loading'|'ready'|'error', candidates, matched, retry }.
 */
export function useDraftCandidates(model, engineId, file, enabled = true) {
  const [state, setState] = useState({ status: enabled ? 'loading' : 'idle', candidates: [], matched: null })
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    if (!enabled) return undefined
    let live = true
    setState(s => ({ ...s, status: 'loading' }))
    ;(async () => {
      const models = await modelsApi.listCapabilities()
      const list = Array.isArray(models) ? models : Array.isArray(models?.data) ? models.data : []
      const backends = [...new Set(list.map(m => m?.backend ?? ''))]
      const engines = await Promise.all(backends.map(b => engineForBackend(b).then(e => [b, e?.id], () => [b, undefined])))
      const engineOf = new Map(engines)
      const candidates = draftCandidates(list, model, b => engineOf.get(b ?? '') === engineId)
      let matched = null
      if (file) {
        const files = await Promise.all(candidates.map(c => modelFileOf(c.id).then(f => [c.id, f], () => [c.id, ''])))
        matched = files.find(([, f]) => f === file)?.[0] ?? null
      }
      if (live) setState({ status: 'ready', candidates, matched })
    })().catch(() => { if (live) setState({ status: 'error', candidates: [], matched: null }) })
    return () => { live = false }
  }, [model, engineId, file, enabled, attempt])

  const retry = useCallback(() => setAttempt(a => a + 1), [])
  return enabled ? { ...state, retry } : { status: 'idle', candidates: [], matched: null, retry }
}
