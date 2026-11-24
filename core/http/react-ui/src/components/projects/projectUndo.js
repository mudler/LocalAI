import { useSyncExternalStore } from 'react'
import { chatProjectsApi } from '../../utils/api'
import { TOAST_MS } from '../ui/toastModel'

// Deleting a project waits out its Undo window before the server is asked.
// The project leaves the list at once; Undo within the window brings it back
// and nothing is sent. The store lives at module level so the window survives
// a route change (the project page deletes and goes back to the list).

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

// The ids of the projects whose delete is waiting for its Undo window.
export function usePendingProjectDeletes() {
  return useSyncExternalStore(subscribe, () => snapshot, () => snapshot)
}

// Sends every waiting delete now. A closing tab cannot wait 8 seconds.
function flushAll() {
  for (const p of [...pending.values()]) p.commit()
}
if (typeof window !== 'undefined') window.addEventListener('pagehide', flushAll)

// broken says the project is a damaged row that was moved to the Trash with its
// whole folder: the toast says so, since the person asked to remove a row.
export function deleteProjectWithUndo(project, { addToast, t, onDeleted, onFailed, broken = false }) {
  if (!project || pending.has(project.id)) return
  const commit = async () => {
    const p = pending.get(project.id)
    if (!p) return
    clearTimeout(p.timer)
    try {
      await chatProjectsApi.remove(project.id)
      pending.delete(project.id)
      changed()
      onDeleted?.()
    } catch (err) {
      pending.delete(project.id)
      changed()
      addToast(t('projects:toasts.deleteFailed', { message: err.message }), 'error')
      onFailed?.()
    }
  }
  const timer = setTimeout(commit, TOAST_MS.action)
  pending.set(project.id, { timer, commit })
  changed()
  addToast(t(broken ? 'projects:toasts.trashedUndo' : 'projects:toasts.deletedUndo', { name: project.name }), 'success', undefined, {
    testId: 'project-undo-toast',
    action: {
      label: t('common:ui.undo'),
      onClick: () => {
        const p = pending.get(project.id)
        if (!p) {
          addToast(t('projects:toasts.undoLate', { name: project.name }), 'info')
          return
        }
        clearTimeout(p.timer)
        pending.delete(project.id)
        changed()
        addToast(t('projects:toasts.undone', { name: project.name }), 'success')
      },
    },
  })
}
