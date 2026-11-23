import { useEffect, useRef, useState } from 'react'

// A value that changes many times a second, handed to the page at most once
// every `ms`. The last value always wins: a trailing timer carries the newest
// one, so the end of a stream is never dropped.
export function useThrottledValue(value, ms) {
  const [shown, setShown] = useState(value)
  // When the last value reached `shown`, so the next one waits only what is left.
  const last = useRef(0)
  const timer = useRef(null)
  // The freshest value, for the trailing timer to read when it fires.
  const latest = useRef(value)
  latest.current = value

  useEffect(() => {
    if (value === shown) return
    const wait = ms - (Date.now() - last.current)
    if (wait <= 0) {
      // The window is open: show this value now and start the next window.
      if (timer.current) {
        clearTimeout(timer.current)
        timer.current = null
      }
      last.current = Date.now()
      setShown(value)
      return
    }
    // A timer already waits, and it will read `latest`: it shows the newest value.
    if (timer.current) return
    timer.current = setTimeout(() => {
      timer.current = null
      last.current = Date.now()
      setShown(latest.current)
    }, wait)
  }, [value, ms, shown])

  useEffect(() => () => {
    if (timer.current) clearTimeout(timer.current)
  }, [])

  return shown
}
