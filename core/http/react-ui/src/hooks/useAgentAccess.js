import { useEffect, useMemo, useState } from 'react'
import { chatConnectorsApi } from '../utils/api'

// What each messaging connection of a saved agent lets its senders do, as the
// server's access preview answers it (the same permission chain as the
// connection's own page). The preview reads the saved agent and the saved
// connection, so it is asked once per set of connections, not per keystroke.
// connections: the rows of useAgentTools ({ id, name, to }).
// Returns [{ id, name, to, status: loading | ok | error, view }].
export function useAgentAccess(agentId, connections) {
  const key = agentId ? connections.map((c) => c.id).join('|') : ''
  const [views, setViews] = useState({})
  useEffect(() => {
    if (!key) return undefined
    let live = true
    key.split('|').forEach((id) => {
      chatConnectorsApi.accessPreview(id, { agent_id: agentId })
        .then((view) => { if (live) setViews((v) => ({ ...v, [id]: { status: 'ok', view } })) })
        .catch(() => { if (live) setViews((v) => ({ ...v, [id]: { status: 'error' } })) })
    })
    return () => { live = false }
  }, [key, agentId])
  return useMemo(
    () => (agentId ? connections : []).map((c) => ({ ...c, status: views[c.id]?.status || 'loading', view: views[c.id]?.view })),
    [agentId, connections, views],
  )
}
