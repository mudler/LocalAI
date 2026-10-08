import { useCallback, useEffect, useRef, useState } from 'react'
import { modelsApi } from '../utils/api'
import { splitByContext } from '../utils/placement'

// What the server estimates when the file sets no context size.
export const DEFAULT_CONTEXT = 8192

const DEBOUNCE_MS = 350

// Readings already made this session, keyed by what was asked. A failed or
// empty reading is never kept, so "Retry" really asks again.
const readings = new Map()

// One call to /api/models/vram-estimate, reduced to the figures the editor
// uses. null means the estimate has nothing to say: the file is missing, still
// downloading, or in a format it does not cover (the endpoint answers 200 with
// only a message in that case).
//
// `layers` null asks for every layer: the endpoint reads a zero or an absent
// gpu_layers as "all".
export function readEstimate(model, contextSize, layers) {
  const key = `${model}|${contextSize}|${layers ?? 'all'}`
  const known = readings.get(key)
  if (known) return known
  const body = { model, context_size: contextSize }
  if (layers != null) body.gpu_layers = layers
  const promise = modelsApi.estimateVram(body)
    .then(data => {
      const bytes = Number(data?.vram_bytes)
      if (!(bytes > 0)) { readings.delete(key); return null }
      return {
        bytes,
        sizeBytes: Number(data.size_bytes) || 0,
        maxContext: Number(data.model_max_context) || 0,
        // Not sent by the server today. Read when present, so a slider can use
        // it the day the estimate reports the model's layer count.
        layerCount: Number(data.block_count) || 0,
      }
    })
    .catch(() => { readings.delete(key); return null })
  readings.set(key, promise)
  return promise
}

// The estimate behind the Placement section for the current choice.
//
//   status   'idle' (nothing to estimate), 'loading' (first reading),
//            'ready', or 'unavailable'.
//   data     { gpuBytes, allBytes, parts, sizeBytes, maxContext, layerCount }
//            once a reading exists. `parts` splits gpuBytes into the part that
//            grows with context and the rest, or is null.
//   refreshing  a newer reading is on its way and `data` is the previous one.
//
// Three readings at most: the choice itself, the same choice at twice the
// context (the difference is the context-dependent part), and every layer on
// the GPU (what a partial choice leaves behind).
export function usePlacementEstimate({ model, contextSize, layers, enabled = true }) {
  const [state, setState] = useState({ status: 'idle', data: null, refreshing: false })
  const [nonce, setNonce] = useState(0)
  const contextRef = useRef(contextSize)
  contextRef.current = contextSize

  useEffect(() => {
    if (!enabled || !model) {
      setState({ status: 'idle', data: null, refreshing: false })
      return undefined
    }
    let cancelled = false
    setState(prev => (prev.data ? { ...prev, refreshing: true } : { status: 'loading', data: null, refreshing: false }))
    const timer = setTimeout(async () => {
      const ctx = contextSize > 0 ? contextSize : DEFAULT_CONTEXT
      const [here, grown, all] = await Promise.all([
        readEstimate(model, ctx, layers),
        readEstimate(model, ctx * 2, layers),
        layers != null ? readEstimate(model, ctx, null) : null,
      ])
      if (cancelled) return
      if (!here) {
        setState({ status: 'unavailable', data: null, refreshing: false })
        return
      }
      setState({
        status: 'ready',
        refreshing: false,
        data: {
          gpuBytes: here.bytes,
          allBytes: layers != null ? (all?.bytes ?? here.bytes) : here.bytes,
          parts: splitByContext({ ctx, bytes: here.bytes }, grown ? { ctx: ctx * 2, bytes: grown.bytes } : null),
          sizeBytes: here.sizeBytes,
          maxContext: here.maxContext,
          layerCount: here.layerCount,
        },
      })
    }, DEBOUNCE_MS)
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [model, contextSize, layers, enabled, nonce])

  const retry = useCallback(() => {
    setState({ status: 'loading', data: null, refreshing: false })
    setNonce(n => n + 1)
  }, [])

  // For the layer search: the estimate in bytes at a layer count, at the
  // context now on screen. null when the endpoint cannot say.
  const bytesAt = useCallback(async (layerCount) => {
    const ctx = contextRef.current > 0 ? contextRef.current : DEFAULT_CONTEXT
    const reading = await readEstimate(model, ctx, layerCount)
    return reading ? reading.bytes : null
  }, [model])

  return { ...state, retry, bytesAt }
}
