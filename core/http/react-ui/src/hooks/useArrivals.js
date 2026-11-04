import { useEffect, useRef, useState } from 'react'

const FRESH_MS = 3000

// useArrivals tells which keys of a list arrived while the list was on the
// screen: a file the assistant just wrote, a page just pinned. The first list
// it sees is the starting point, so nothing is marked on load. A key stays
// fresh for three seconds, the time of the accent wash that marks it.
//
// It returns a Map from key to phase: 'on' for the first frame (the wash at
// full strength), then 'fade' (the wash transitions out), until the key is
// dropped. Draw it as data-fresh={fresh.get(key)}. Two phases, because a
// transition cannot start from a pseudo-element's first style.
//
// scope resets the starting point (another chat, another user); ready says the
// list is the real one and not a placeholder before the first read.
export function useArrivals(keys, { scope = '', ready = true } = {}) {
  const seen = useRef(null)
  const seenScope = useRef(scope)
  const [fresh, setFresh] = useState(() => new Map())
  const timers = useRef([])
  const joined = keys.join('\n')

  useEffect(() => () => timers.current.forEach(clearTimeout), [])

  useEffect(() => {
    if (seenScope.current !== scope) {
      seenScope.current = scope
      seen.current = null
      setFresh(new Map())
    }
    if (!ready) return undefined
    const list = joined ? joined.split('\n') : []
    if (seen.current === null) {
      seen.current = new Set(list)
      return undefined
    }
    const added = list.filter(k => !seen.current.has(k))
    list.forEach(k => seen.current.add(k))
    if (added.length === 0) return undefined
    const phase = (value) => setFresh(prev => {
      const next = new Map(prev)
      added.forEach(k => next.set(k, value))
      return next
    })
    phase('on')
    // Two frames, so the browser has drawn the wash at full strength first.
    requestAnimationFrame(() => requestAnimationFrame(() => phase('fade')))
    // A later change of the list must not cancel this timer, or the keys
    // would stay fresh; the timers end with the component.
    timers.current.push(setTimeout(() => {
      setFresh(prev => {
        const next = new Map(prev)
        added.forEach(k => next.delete(k))
        return next
      })
    }, FRESH_MS))
    return undefined
  }, [joined, scope, ready])

  return fresh
}
