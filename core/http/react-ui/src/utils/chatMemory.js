// SPDX-License-Identifier: MIT
const reasons = new Set(['exhausted_or_expired', 'expired', 'exhausted', 'policy_revoked', 'authorization_denied', 'deleted', 'empty_input', 'empty_summary', 'summary_failed', 'missing_entry_id', 'payload_unavailable', 'capture_failed', 'capture_skipped'])
export const memoryReason = reason => reasons.has(reason) ? reason : 'unknown'
export const memoryStatus = status => ['queued', 'processing', 'saved', 'failed', 'skipped'].includes(status) ? status : 'unknown'
export function memoryRetryTime(seconds) {
  const date = new Date(Number(seconds) * 1000)
  return seconds > 0 && Number.isFinite(date.getTime()) ? date : null
}

// A read after retry or fanout supersedes earlier reads, even if abort is ignored.
export function createMemoryReader(load, apply, fail = () => {}) {
  let epoch = 0
  let disposed = false
  let controller
  return {
    async refresh() {
      if (disposed) return
      const mine = ++epoch
      controller?.abort()
      controller = new AbortController()
      try {
        const rows = await load(controller.signal)
        if (!disposed && mine === epoch) apply(Array.isArray(rows) ? rows : [])
      } catch (error) {
        if (!disposed && mine === epoch && error.name !== 'AbortError') fail(error)
      }
    },
    dispose() { disposed = true; epoch++; controller?.abort() },
  }
}
