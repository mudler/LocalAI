import { useEffect, useState } from 'react'

// Keeps a surface mounted while it leaves, and names the phase for CSS:
// 'enter' on the first frame (the start values), 'open' after it (the
// transition runs to the resting values), 'exit' while it leaves. The CSS
// moves transform and opacity only, and the global reduced-motion rule makes
// every phase change instant.
export default function usePresence(open, exitMs) {
  const [mounted, setMounted] = useState(open)
  const [phase, setPhase] = useState(open ? 'enter' : 'exit')

  // Mount in the same render that opens, so the first frame has the start
  // values (React's adjust-state-while-rendering pattern).
  if (open && !mounted) {
    setMounted(true)
    setPhase('enter')
  }

  useEffect(() => {
    if (open) {
      // Two frames: the browser must paint the start values once, or the
      // transition has nothing to run from.
      let second = 0
      const first = requestAnimationFrame(() => {
        second = requestAnimationFrame(() => setPhase('open'))
      })
      return () => { cancelAnimationFrame(first); cancelAnimationFrame(second) }
    }
    if (!mounted) return undefined
    setPhase('exit')
    const timer = setTimeout(() => setMounted(false), exitMs)
    return () => clearTimeout(timer)
  }, [open, mounted, exitMs])

  return { mounted, phase }
}
