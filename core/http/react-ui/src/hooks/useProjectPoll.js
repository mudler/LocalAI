import { useCallback, useEffect, useRef, useState } from 'react'

export const POLL_MS = 15000

// Reads data that belongs to one project and keeps it fresh. load(projectId)
// returns the data of the project. One timer serves the whole tab, and a tick
// is skipped while the page is hidden; the page reads again when it shows.
// An answer for another project, or one that comes after the tab went away,
// is dropped, and the data is cleared when the project changes, so the tab
// never draws the rows of the project before.
export function useProjectPoll(projectId, load) {
  const [data, setData] = useState(null)
  const [error, setError] = useState(null)
  const loadRef = useRef(load)
  loadRef.current = load
  const runRef = useRef(null)

  useEffect(() => {
    let ignore = false
    setData(null)
    setError(null)
    const run = async () => {
      try {
        const next = await loadRef.current(projectId)
        if (!ignore) { setData(next); setError(null) }
      } catch (err) {
        if (!ignore) setError(err)
      }
    }
    runRef.current = run
    run()
    const timer = setInterval(() => { if (!document.hidden) run() }, POLL_MS)
    const onShow = () => { if (!document.hidden) run() }
    document.addEventListener('visibilitychange', onShow)
    return () => {
      ignore = true
      clearInterval(timer)
      document.removeEventListener('visibilitychange', onShow)
    }
  }, [projectId])

  const reload = useCallback(() => { runRef.current?.() }, [])
  return { data, error, reload }
}
