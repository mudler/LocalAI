import { useEffect, useState } from 'react'

// useHidden is true while the tab is hidden: a page frame then hears that it
// is out of sight and can pause.
export function useHidden() {
  const [hidden, setHidden] = useState(() => typeof document !== 'undefined' && document.hidden)
  useEffect(() => {
    const on = () => setHidden(document.hidden)
    document.addEventListener('visibilitychange', on)
    return () => document.removeEventListener('visibilitychange', on)
  }, [])
  return hidden
}
