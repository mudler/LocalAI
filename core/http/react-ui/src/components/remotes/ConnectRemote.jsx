import RemoteConnectStep from './RemoteConnectStep'
import RemoteChooseStep from './RemoteChooseStep'
import { useRemoteConnect } from './useRemoteConnect'

// Both steps stacked, for a sheet. The first run places the two steps apart
// and uses useRemoteConnect itself.
export default function ConnectRemote({ onDone }) {
  const remote = useRemoteConnect({ onDone })
  return (
    <div className="remotes-stack">
      <RemoteConnectStep remote={remote} />
      <RemoteChooseStep remote={remote} />
    </div>
  )
}

export function doneMessageParts(result) {
  const created = Array.isArray(result?.created) ? result.created.length : 0
  const skipped = Array.isArray(result?.skipped) ? result.skipped.length : 0
  return { name: result?.remote?.name || '', created, skipped }
}
