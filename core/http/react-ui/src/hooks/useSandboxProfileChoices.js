import { useEffect, useState } from 'react'
import { chatSandboxProfileApi } from '../utils/api'

// The sandbox profiles a person may pick for a saved agent (GET
// /api/chats/sandbox-profiles). Empty while loading, and when no add-on runs
// commands in profiles: the agent editor then draws no select.
export function useSandboxProfileChoices() {
  const [list, setList] = useState([])
  useEffect(() => {
    let live = true
    chatSandboxProfileApi.list().then(r => { if (live) setList(r?.profiles || []) }).catch(() => {})
    return () => { live = false }
  }, [])
  return list
}
