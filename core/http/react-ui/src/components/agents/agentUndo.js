import { useSyncExternalStore } from 'react'
import { chatAgentsApi } from '../../utils/api'
import { TOAST_MS } from '../ui/toastModel'

// Deleting an agent waits out its Undo window before the server is asked.
// The agent leaves the list at once; Undo within the window brings it back
// and nothing is sent. The store lives at module level so the window survives
// a route change (the agent page deletes and goes back to the list).

const pending = new Map() // id -> { timer, commit }
const listeners = new Set()
let snapshot = new Set()

function changed() {
  snapshot = new Set(pending.keys())
  listeners.forEach(fn => fn())
}

function subscribe(fn) {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

// The ids of the agents whose delete is waiting for its Undo window.
export function usePendingAgentDeletes() {
  return useSyncExternalStore(subscribe, () => snapshot, () => snapshot)
}

// Sends every waiting delete now. A closing tab cannot wait 8 seconds.
function flushAll() {
  for (const p of [...pending.values()]) p.commit()
}
if (typeof window !== 'undefined') window.addEventListener('pagehide', flushAll)

export function deleteAgentWithUndo(agent, { addToast, t, onDeleted, onFailed }) {
  if (!agent || pending.has(agent.id)) return
  const commit = async () => {
    const p = pending.get(agent.id)
    if (!p) return
    clearTimeout(p.timer)
    pending.delete(agent.id)
    try {
      await chatAgentsApi.remove(agent.id)
      changed()
      onDeleted?.()
    } catch (err) {
      changed()
      addToast(t('agents:toasts.deleteFailed', { message: err.message }), 'error')
      onFailed?.()
    }
  }
  // Keep the agent hidden until the server answers, so it does not flash back
  // between the commit and the next list read.
  const timer = setTimeout(commit, TOAST_MS.action)
  pending.set(agent.id, { timer, commit })
  changed()
  addToast(t('agents:toasts.deletedUndo', { name: agent.name }), 'success', undefined, {
    testId: 'agent-undo-toast',
    action: {
      label: t('common:ui.undo'),
      onClick: () => {
        const p = pending.get(agent.id)
        if (!p) {
          addToast(t('agents:toasts.undoLate', { name: agent.name }), 'info')
          return
        }
        clearTimeout(p.timer)
        pending.delete(agent.id)
        changed()
        addToast(t('agents:toasts.restored', { name: agent.name }), 'success')
      },
    },
  })
}
