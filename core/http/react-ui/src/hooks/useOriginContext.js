import { useEffect, useState } from 'react'
import { chatConnectorsApi, chatTriggersApi } from '../utils/api'

const EMPTY = { connectors: [], connectorKinds: [], triggers: [] }

// The lists that name what started a chat: the connector bindings (and the
// kinds the add-ons serve) and the triggers. Each read is best effort: a
// server with connectors or triggers off answers 409, and the chats of that
// origin are then named by their kind. Nothing is read until `enabled`.
export function useOriginContext(enabled) {
  const [ctx, setCtx] = useState(EMPTY)

  useEffect(() => {
    if (!enabled) return undefined
    let live = true
    Promise.all([
      chatConnectorsApi.list().catch(() => null),
      chatConnectorsApi.kinds().catch(() => null),
      chatTriggersApi.list().catch(() => null),
    ]).then(([connectors, kinds, triggers]) => {
      if (!live) return
      setCtx({
        connectors: connectors?.connectors || [],
        connectorKinds: kinds?.kinds || [],
        triggers: triggers?.triggers || [],
      })
    })
    return () => { live = false }
  }, [enabled])

  return ctx
}
