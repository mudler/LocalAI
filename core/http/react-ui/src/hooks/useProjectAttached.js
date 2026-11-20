import { useEffect, useState } from 'react'
import { chatProjectsApi } from '../utils/api'
import { attachedOf, hasAttached } from '../utils/projectAttached'

// The lists a project attaches, for the Tools and Skills menus of a chat or an
// agent in that project. Read best effort: with no project, or a failed read,
// the menus show no project mark and the server still applies the attachment.
// Returns null until there is something attached.
export function useProjectAttached(projectId) {
  const [attached, setAttached] = useState(null)
  useEffect(() => {
    if (!projectId) { setAttached(null); return undefined }
    let live = true
    chatProjectsApi.get(projectId)
      .then((p) => { if (live) { const a = attachedOf(p); setAttached(hasAttached(a) ? a : null) } })
      .catch(() => { if (live) setAttached(null) })
    return () => { live = false }
  }, [projectId])
  return attached
}
