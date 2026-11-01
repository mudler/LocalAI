import { useCallback, useState } from 'react'
import { commandsNotice, readHidden, writeHidden } from '../utils/chatCommands'

const storage = () => (typeof window === 'undefined' ? null : window.localStorage)

// The notice of a chat that cannot run commands, from the "commands" field of
// the chat's add-on list, and whether the person hid it in this browser.
export function useCommandsNotice(commands, reason, isAdmin) {
  const info = commandsNotice(commands, { isAdmin, reason })
  const [hiddenState, setHiddenState] = useState(() => readHidden(storage()))
  const hide = useCallback(() => {
    if (!info) return
    writeHidden(storage(), info.state)
    setHiddenState(info.state)
  }, [info])
  return { info, hidden: !!info && hiddenState === info.state, hide }
}
