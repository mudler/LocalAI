import { useCallback, useEffect, useMemo, useState } from 'react'
import YAML from 'yaml'
import { modelsApi } from '../utils/api'

// The keys the Placement section writes, and the one it reads.
export const PLACEMENT_KEYS = ['gpu_layers', 'tensor_split', 'main_gpu', 'context_size']

function pick(config) {
  const values = {}
  for (const key of PLACEMENT_KEYS) {
    if (config && config[key] !== undefined && config[key] !== null) values[key] = config[key]
  }
  return values
}

// A model's placement keys, held apart from the rest of its configuration so
// the model page can edit just these and save them as a patch.
//
//   status   'loading', 'ready' or 'error'.
//   values   the keys now on screen (an absent key is unset).
//   preview  the configuration file as it would read after saving.
//   dirty    whether `values` differs from what is saved.
//   save     writes the changed keys; an unset key is sent as null, which the
//            patch endpoint stores as no value.
export function usePlacementConfig(name, { onSaved, onError } = {}) {
  const [state, setState] = useState({ status: 'loading', parsed: null, saved: {}, error: '' })
  const [values, setValues] = useState({})
  const [saving, setSaving] = useState(false)
  const [nonce, setNonce] = useState(0)

  useEffect(() => {
    let cancelled = false
    setState(prev => (prev.parsed ? prev : { status: 'loading', parsed: null, saved: {}, error: '' }))
    modelsApi.getEditConfig(name)
      .then(data => {
        if (cancelled) return
        let parsed = {}
        try { parsed = YAML.parse(data?.config || '') || {} } catch { parsed = {} }
        const saved = pick(parsed)
        setState({ status: 'ready', parsed, saved, error: '' })
        setValues(saved)
      })
      .catch(err => { if (!cancelled) setState({ status: 'error', parsed: null, saved: {}, error: err.message }) })
    return () => { cancelled = true }
  }, [name, nonce])

  const setValue = useCallback((path, value) => {
    setValues(prev => {
      const next = { ...prev }
      if (value === undefined) delete next[path]
      else next[path] = value
      return next
    })
  }, [])

  const dirty = PLACEMENT_KEYS.some(key => JSON.stringify(values[key]) !== JSON.stringify(state.saved[key]))

  const preview = useMemo(() => {
    if (!state.parsed) return ''
    const merged = { ...state.parsed }
    for (const key of PLACEMENT_KEYS) {
      if (values[key] === undefined) delete merged[key]
      else merged[key] = values[key]
    }
    try { return YAML.stringify(merged) } catch { return '' }
  }, [state.parsed, values])

  const save = useCallback(async () => {
    const patch = {}
    for (const key of PLACEMENT_KEYS) {
      if (JSON.stringify(values[key]) === JSON.stringify(state.saved[key])) continue
      patch[key] = values[key] === undefined ? null : values[key]
    }
    if (Object.keys(patch).length === 0) return
    setSaving(true)
    try {
      await modelsApi.patchConfig(name, patch)
      setNonce(n => n + 1)
      onSaved?.()
    } catch (err) {
      onError?.(err)
    } finally {
      setSaving(false)
    }
  }, [name, values, state.saved, onSaved, onError])

  const reload = useCallback(() => setNonce(n => n + 1), [])
  return { status: state.status, error: state.error, values, setValue, preview, dirty, save, saving, reload }
}
