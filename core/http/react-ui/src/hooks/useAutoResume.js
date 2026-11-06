import { useEffect, useRef, useState } from 'react'
import { createResumer } from '../utils/turnResume'

// Resumes an interrupted turn of the open chat by itself (see utils/turnResume).
// Returns what the failure card shows and a stop() for the "Stop trying" button.
export function useAutoResume({ id, lastTurn, running, resuming, updated, resume, willResume = false, resumeAttempts = 0, refresh }) {
  const [view, setView] = useState({ phase: 'idle', attempt: 0, max: 0, seconds: 0 })
  const resumeRef = useRef(resume)
  resumeRef.current = resume
  const ref = useRef(null)
  const refreshRef = useRef(refresh)
  refreshRef.current = refresh
  if (!ref.current) {
    ref.current = createResumer({ resume: (cid) => resumeRef.current(cid), refresh: (cid) => refreshRef.current?.(cid), onChange: setView })
  }
  const updatedRef = useRef(updated)
  updatedRef.current = updated

  useEffect(() => {
    ref.current.observe({ id, lastTurn, running, resuming, updated: updatedRef.current, willResume, resumeAttempts })
  }, [id, lastTurn, running, resuming, willResume, resumeAttempts])
  useEffect(() => () => ref.current.dispose(), [])

  return { ...view, stop: () => ref.current.stop(), reset: () => ref.current.reset() }
}
