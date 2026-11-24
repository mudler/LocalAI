/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import RemoteConnectStep from './RemoteConnectStep'
import RemoteChooseStep from './RemoteChooseStep'
import RemoteInviteStep from './RemoteInviteStep'
import ConnectWith from './ConnectWith'
import { useRemoteConnect } from './useRemoteConnect'
import { useRemoteInvite } from './useRemoteInvite'
import { resultParts } from '../../utils/remotes'

// Both steps stacked, for a sheet. The first run places the two steps apart
// and uses the hooks itself. The first step connects with an address or with
// an invite link; the second reads the one in use.
export default function ConnectRemote({ onDone }) {
  const remote = useRemoteConnect({ onDone })
  const invite = useRemoteInvite({ onDone })
  const [connectWith, setConnectWith] = useState('address')
  const chosen = connectWith === 'invite' ? invite : remote
  return (
    <div className="remotes-stack">
      <div>
        <ConnectWith value={connectWith} onChange={setConnectWith} locked={invite.phase === 'connecting' || Boolean(invite.saved) || remote.locked} />
        {connectWith === 'invite' ? <RemoteInviteStep invite={invite} /> : <RemoteConnectStep remote={remote} />}
      </div>
      <RemoteChooseStep remote={chosen} />
    </div>
  )
}

// What the toasts after a connect say: the name, how many models were added
// and the models left out.
export function doneMessageParts(result) {
  const { created, skipped } = resultParts(result)
  return { name: result?.remote?.name || '', created: created.length, skipped }
}
