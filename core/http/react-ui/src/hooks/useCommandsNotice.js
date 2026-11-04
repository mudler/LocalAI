import { useCallback, useEffect, useState } from 'react'
import { commandsNotice } from '../utils/chatCommands'

// Dismissal is deliberately mount-local: logout/remount never retains it.
// Exact binding, observation and actor/chat context must match, not just state.
export function useCommandsNotice(commands, isAdmin, context) {
  const info = commandsNotice(commands.state, { isAdmin, reason: commands.reason, code: commands.code, hostExec: commands.hostExec })
  const identity = JSON.stringify([context, commands.identity, commands.state, commands.reason, commands.code, commands.hostExec])
  const [hiddenIdentity, setHiddenIdentity] = useState('')
  useEffect(() => { setHiddenIdentity('') }, [identity])
  const hide = useCallback(() => {
    if (info) setHiddenIdentity(identity)
  }, [info, identity])
  // A state with no binding yet (loading, a failed read) can be hidden too: the
  // identity then holds the state and the reason, and a new reading brings the
  // notice back.
  return { info, hidden: !!info && hiddenIdentity === identity, hide }
}
