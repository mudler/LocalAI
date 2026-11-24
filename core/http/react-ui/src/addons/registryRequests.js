// SPDX-License-Identifier: MIT
// Requests belong to an actor/permission epoch. Abort is only an optimization:
// an API that ignores the signal still cannot publish into a later epoch.
export function createRegistryRequests() {
  let scope, epoch = 0, sequence = 0
  const pending = new Set()
  return {
    setScope(next) {
      if (next === scope) return
      scope = next
      epoch++
      for (const ctl of pending) ctl.abort()
      pending.clear()
    },
    async read(load, publish, signal) {
      const ownEpoch = epoch, ownSequence = ++sequence
      const ctl = new AbortController()
      const abort = () => ctl.abort()
      signal?.addEventListener('abort', abort, { once: true })
      if (signal?.aborted) ctl.abort()
      pending.add(ctl)
      const current = () => !ctl.signal.aborted && ownEpoch === epoch && ownSequence === sequence
      try {
        const apps = await load(ctl.signal)
        if (!current()) return null
        publish({ status: 'ready', apps, error: null })
        return apps
      } catch (error) {
        if (current()) publish({ status: 'error', apps: [], error })
        return null
      } finally {
        pending.delete(ctl)
        signal?.removeEventListener('abort', abort)
      }
    },
  }
}
