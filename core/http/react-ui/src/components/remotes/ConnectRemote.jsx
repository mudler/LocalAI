import RemoteConnectStep from './RemoteConnectStep'
import RemoteChooseStep from './RemoteChooseStep'
import { useRemoteConnect } from './useRemoteConnect'
import { resultParts } from '../../utils/remotes'

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

// What the toasts after a connect say: the name, how many models were added
// and the models left out.
export function doneMessageParts(result) {
  const { created, skipped } = resultParts(result)
  return { name: result?.remote?.name || '', created: created.length, skipped }
}
