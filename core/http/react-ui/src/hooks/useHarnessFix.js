import { useEffect, useState } from 'react'
import { resourcesApi } from '../utils/api'
import { modelBudget } from '../utils/modelBudget'
import { fitFor } from '../utils/modelLedger'
import { contextLadder, pickWindow } from '../utils/chatHarness'
import { readEstimate } from './usePlacementEstimate'

// What an administrator is told before raising the window of a model: whether
// the recommended window fits the memory of this machine and, if not, the
// largest window that does and still reaches the minimum. It reads the memory
// of the host (or the best node of a cluster) once and asks the estimate
// endpoint for one window at a time, largest first, so a roomy machine costs a
// single call.
//
// Returns { status, pick } where status is 'idle' (not asked), 'loading' or
// 'ready', and pick is what pickWindow returns.
export function useHarnessFix({ enabled, model, minimum, recommended }) {
  const [state, setState] = useState({ status: 'idle', pick: null, maxContext: 0 })

  useEffect(() => {
    if (!enabled || !model || !(minimum > 0)) { setState({ status: 'idle', pick: null, maxContext: 0 }); return undefined }
    let cancelled = false
    setState({ status: 'loading', pick: null, maxContext: 0 })
    ;(async () => {
      const resources = await resourcesApi.get().catch(() => null)
      const budget = modelBudget(resources)
      const ramAvailable = resources?.ram?.available ?? resources?.ram?.free ?? null
      // The first reading also tells the trained maximum of the model, which
      // ends the ladder at what the model can do.
      const first = await readEstimate(model, recommended, null)
      if (cancelled) return
      const maxContext = first?.maxContext || 0
      const ladder = contextLadder({ minimum, recommended, maxContext })
      const readings = {}
      for (const value of ladder) {
        const reading = value === recommended ? first : await readEstimate(model, value, null)
        if (cancelled) return
        readings[value] = reading
        if (reading && fitFor(reading.bytes, budget, ramAvailable)?.state === 'fits') break
      }
      if (cancelled) return
      setState({ status: 'ready', pick: pickWindow({ ladder, readings, budget, ramAvailable }), maxContext })
    })()
    return () => { cancelled = true }
  }, [enabled, model, minimum, recommended])

  return state
}
