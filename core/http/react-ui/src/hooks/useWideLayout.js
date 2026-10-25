import { useEffect, useState } from 'react'

const QUERY = '(min-width: 901px)'

// True when the list and the open item sit side by side. On a phone they take
// turns, so a page must not open an item the person did not ask for.
export function useWideLayout() {
  const read = () => (typeof window !== 'undefined' && typeof window.matchMedia === 'function' ? window.matchMedia(QUERY).matches : true)
  const [wide, setWide] = useState(read)
  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return undefined
    const mq = window.matchMedia(QUERY)
    const onChange = () => setWide(mq.matches)
    mq.addEventListener('change', onChange)
    return () => mq.removeEventListener('change', onChange)
  }, [])
  return wide
}
