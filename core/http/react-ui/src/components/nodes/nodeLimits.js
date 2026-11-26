// The draft of the Limits pane of a node, and the changes it sends.

export function draftOf(limits) {
  return {
    max_active_backends: String(limits?.max_active_backends ?? 0),
    vram_budget: limits?.vram_budget ?? '',
    memory_reclaimer_threshold: String(Math.round((limits?.memory_reclaimer_threshold ?? 0.95) * 100)),
    model_swap: limits?.model_swap ?? true,
  }
}

// changesOf returns the keys the admin changed, in the API's shape.
export function changesOf(limits, draft) {
  const out = {}
  const before = draftOf(limits)
  const n = parseInt(draft.max_active_backends, 10)
  if (draft.max_active_backends !== before.max_active_backends && Number.isFinite(n) && n >= 0) out.max_active_backends = n
  if (draft.vram_budget.trim() !== before.vram_budget) out.vram_budget = draft.vram_budget.trim()
  const pct = parseInt(draft.memory_reclaimer_threshold, 10)
  if (draft.memory_reclaimer_threshold !== before.memory_reclaimer_threshold && pct > 0 && pct <= 100) out.memory_reclaimer_threshold = pct / 100
  if (draft.model_swap !== before.model_swap) out.model_swap = draft.model_swap
  return out
}
