// The state of the Sandbox add-on for the Tools picker of a chat. The add-on's
// status read (open to every user) says whether it can run commands, with
// which backend, and whether it runs them on the host with no isolation. The
// picker says it in plain words next to the add-on's switch.

export const SANDBOX_ADDON = 'sandbox'

// { state: 'isolated' | 'host' | 'cannot' | 'starting' | 'unknown', backend, reason } from
// the payload of the status read; anything unreadable is 'unknown'.
export function sandboxState(status) {
  if (!status || typeof status !== 'object' || status.ok !== true) return { state: 'unknown', backend: '', reason: '' }
  const text = (v) => (typeof v === 'string' ? v.slice(0, 200) : '')
  const backend = text(status.active)
  if (status.canRun !== true) {
    const why = Array.isArray(status.backends) ? status.backends.find(b => b && b.usable !== true && text(b.reason)) : null
    return { state: 'cannot', backend: '', reason: text(status.reason) || (why ? text(why.reason) : '') }
  }
  if (status.hostNotice === true || backend === 'host') return { state: 'host', backend, reason: '' }
  return { state: 'isolated', backend, reason: '' }
}

// startingState is the state while the add-on service does not answer yet:
// the page reads again by itself in retryMs.
export function startingState(retryMs) {
  return { state: 'starting', backend: '', reason: '', retryMs: Math.max(0, Math.round(retryMs || 0)) }
}
