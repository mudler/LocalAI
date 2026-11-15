import { useCallback, useEffect, useState } from 'react'
import { chatProjectsApi } from '../utils/api'
import { isProjectsOff } from '../utils/projects'

// The projects of the person, for the controls of a chat: the composer chip,
// the chat header, the chat settings and the All chats filter. The list is
// best effort. When projects are off or the read fails, `projects` stays null
// and `status` says why ('off' or 'failed'); a control then draws nothing and
// the page never shows an error for it.
//
//   enabled   false skips the request (a chat with no project needs none)
//   status    'idle' | 'loading' | 'ready' | 'off' | 'failed'
//   shared    the projects shared with the person (an array), or null when
//             that read has not answered or failed. It is separate from the
//             status: a failed shared read never changes what the controls draw.
//   reload    reads the list again (the chip does it on open, for fresh counts)
export function useProjectList(enabled = true) {
  const [state, setState] = useState({ projects: null, status: enabled ? 'loading' : 'idle' })
  const [shared, setShared] = useState(null)
  const [round, setRound] = useState(0)

  useEffect(() => {
    if (!enabled) return undefined
    let live = true
    chatProjectsApi.list()
      .then((r) => { if (live) setState({ projects: r?.projects || [], status: 'ready' }) })
      .catch((err) => { if (live) setState(s => ({ projects: null, status: isProjectsOff(err) ? 'off' : (s.projects ? 'ready' : 'failed') })) })
    // Best effort: the shared list is only for the pickers and the chips.
    chatProjectsApi.shared()
      .then((r) => { if (live) setShared(r?.projects || []) })
      .catch(() => { if (live) setShared(prev => prev) })
    return () => { live = false }
  }, [enabled, round])

  const reload = useCallback(() => setRound(n => n + 1), [])
  return { ...state, shared, reload }
}
