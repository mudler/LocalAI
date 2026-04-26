import { useState, useEffect, useRef, useMemo } from 'react'
import { modelsApi } from '../utils/api'

const DEBOUNCE_MS = 500

export function useVramEstimate({ model, contextSize, gpuLayers }) {
  const [vramDisplay, setVramDisplay] = useState(null)
  const [loading, setLoading] = useState(false)
  const debounceRef = useRef(null)
  const abortRef = useRef(null)

  useEffect(() => {
    if (!model || contextSize === undefined) {
      setVramDisplay(null)
      setLoading(false)
      return
    }

    if (debounceRef.current) clearTimeout(debounceRef.current)
    if (abortRef.current) abortRef.current.abort()

    debounceRef.current = setTimeout(async () => {
      const controller = new AbortController()
      abortRef.current = controller
      setLoading(true)

      try {
        const body = { model }
        if (contextSize != null && contextSize !== '') body.context_size = Number(contextSize)
        if (gpuLayers != null && gpuLayers !== '') body.gpu_layers = Number(gpuLayers)

        const data = await modelsApi.estimateVram(body, { signal: controller.signal })

        if (!controller.signal.aborted) {
          setVramDisplay(data?.vramDisplay || null)
          setLoading(false)
        }
      } catch {
        if (!controller.signal.aborted) {
          setVramDisplay(null)
          setLoading(false)
        }
      }
    }, DEBOUNCE_MS)

    return () => {
      if (debounceRef.current) clearTimeout(debounceRef.current)
      if (abortRef.current) abortRef.current.abort()
    }
  }, [model, contextSize, gpuLayers])

  return useMemo(() => ({ vramDisplay, loading }), [vramDisplay, loading])
}
