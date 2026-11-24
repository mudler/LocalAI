import { useMemo } from 'react'
import { useAuth } from '../context/AuthContext'
import { useWellKnown } from './useWellKnown'
import { readCommands } from '../utils/userCommands'

// The slash commands of the user (LocalAI/commands.json), read through the
// shared copy of the file, so a change by the assistant or another tab shows
// at once. A file with errors gives its last good version; no file, no
// commands.
export function useUserCommands() {
  const { hasFeature } = useAuth()
  const on = !hasFeature || hasFeature('chats')
  const wk = useWellKnown('commands', { enabled: on })
  return useMemo(() => readCommands(wk.doc?.data), [wk.doc])
}
