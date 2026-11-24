import { useEffect, useRef } from 'react'
import { apiUrl } from '../utils/basePath'
import { createWorkspaceFeed } from '../utils/workspaceFeed'

// One feed per tab, created on first use, so every page shares one stream.
let shared = null
function feed() {
  if (!shared) shared = createWorkspaceFeed({ url: apiUrl('/api/workspace/events') })
  return shared
}

// useWorkspaceFeed calls onChange({paths, revision}) when files of the user's
// home folder that match one of the path globs change, whoever changed them.
// patterns are relative to the home folder: ['home.json', 'pages/*/**'].
// A burst of changes becomes one call 150 ms after the last one. A call with
// resync: true (and paths empty) means changes may have been missed: read
// again everything the component shows. That also happens when the window
// gets the focus again and when a chat turn ends.
export function useWorkspaceFeed(patterns, onChange) {
  const cb = useRef(onChange)
  cb.current = onChange
  const key = JSON.stringify(patterns || [])
  useEffect(() => {
    const globs = JSON.parse(key)
    if (globs.length === 0) return undefined
    return feed().subscribe(globs, (ev) => cb.current?.(ev))
  }, [key])
}

// invalidate tells the feed that this tab saved path itself and already shows
// the new content, so the echo of that save does not refresh it again. Pass the
// revision the save returned when there is one.
export function invalidate(path, revision) {
  feed().invalidate(path, revision)
}

// notifyTurnEnd is called by the chat at the end of a turn: the tools of the
// turn may have written files, so every subscriber reads again once.
export function notifyTurnEnd() {
  shared?.turnEnded()
}
