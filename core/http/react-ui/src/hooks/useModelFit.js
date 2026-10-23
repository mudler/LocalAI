import { useEffect, useMemo, useRef, useState } from 'react'
import { resourcesApi } from '../utils/api'
import { modelBudget } from '../utils/modelBudget'
import { fitFor } from '../utils/modelLedger'
import { readEstimate, DEFAULT_CONTEXT } from './usePlacementEstimate'

// How many models get an estimate when the switcher opens, and how many run at
// once. A long list is estimated from the top; the rest show no fit text, which
// is honest: nothing has been measured for them.
const MAX_MODELS = 12
const CONCURRENCY = 3

// What the model switcher can say about fit, read when it opens (`openCount`
// goes up by one each time, and 0 means it never has): one
// reading of this host's memory and one estimate per listed model at the chat's
// context size. A model with no estimate (a file the server cannot read, an
// unsupported format) gets no fit text.
export function useModelFit({ openCount, names, contextSize }) {
  const [resources, setResources] = useState(null)
  const [sizes, setSizes] = useState({})
  const ctx = contextSize > 0 ? contextSize : DEFAULT_CONTEXT
  const key = names.slice(0, MAX_MODELS).join('\n')
  const ctxRef = useRef(ctx)
  ctxRef.current = ctx

  useEffect(() => {
    if (!openCount) return undefined
    let cancelled = false
    resourcesApi.get().then(r => { if (!cancelled) setResources(r) }).catch(() => {})
    return () => { cancelled = true }
  }, [openCount])

  useEffect(() => {
    if (!openCount || !key) return undefined
    let cancelled = false
    const queue = key.split('\n')
    let cursor = 0
    const worker = async () => {
      while (!cancelled && cursor < queue.length) {
        const name = queue[cursor++]
        const reading = await readEstimate(name, ctxRef.current, null)
        if (cancelled) return
        setSizes(prev => ({ ...prev, [`${name}|${ctxRef.current}`]: reading || false }))
      }
    }
    for (let i = 0; i < CONCURRENCY; i++) worker()
    return () => { cancelled = true }
  }, [openCount, key, ctx])

  const budget = useMemo(() => modelBudget(resources), [resources])
  const ramAvailable = resources?.ram?.available ?? resources?.ram?.free ?? null

  // { bytes, sizeBytes, fit } for a model, or null when there is no reading.
  const reading = (name) => {
    const r = sizes[`${name}|${ctx}`]
    if (!r) return null
    return { bytes: r.bytes, sizeBytes: r.sizeBytes, fit: fitFor(r.bytes, budget, ramAvailable) }
  }

  return { resources, budget, reading }
}
