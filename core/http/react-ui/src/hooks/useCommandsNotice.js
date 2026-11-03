import { useCallback, useEffect, useState } from 'react'
import { commandsNotice } from '../utils/chatCommands'

// Dismissal is deliberately mount-local: logout/remount never retains it.
// Exact binding, observation and actor/chat context must match, not just state.
export function useCommandsNotice(commands, isAdmin, context) {
  const info = commandsNotice(commands.state, { isAdmin, reason: commands.reason, code: commands.code })
  const identity = JSON.stringify([context, commands.identity, commands.state, commands.reason, commands.code])
  const [hiddenIdentity, setHiddenIdentity] = useState('')
  useEffect(() => { setHiddenIdentity('') }, [identity])
  const hide = useCallback(() => {
    if (info && commands.identity) setHiddenIdentity(identity)
  }, [info, commands.identity, identity])
  return { info, hidden: !!commands.identity && !!info && hiddenIdentity === identity, hide }
}
