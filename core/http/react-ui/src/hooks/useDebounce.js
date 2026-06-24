import { useRef, useEffect, useCallback } from 'react'

/**
 * Returns a debounced version of the callback. Always calls the latest
 * version of fn (via ref), so callers don't need to memoize it.
 * Timer is cleaned up on unmount.
 */
export function useDebouncedCallback(fn, delay = 500) {
  const timerRef = useRef(null)
  const fnRef = useRef(fn)
  fnRef.current = fn

  useEffect(() => () => {
    if (timerRef.current) clearTimeout(timerRef.current)
  }, [])

  return useCallback((...args) => {
    if (timerRef.current) clearTimeout(timerRef.current)
    timerRef.current = setTimeout(() => fnRef.current(...args), delay)
  }, [delay])
}

/**
 * Runs a debounced effect: when deps change, waits `delay` ms before
 * calling fn. Resets the timer on each deps change. Cleans up on unmount.
 */
export function useDebouncedEffect(fn, deps, delay = 500) {
  const timerRef = useRef(null)
  const fnRef = useRef(fn)
  fnRef.current = fn

  useEffect(() => {
    if (timerRef.current) clearTimeout(timerRef.current)
    timerRef.current = setTimeout(() => fnRef.current(), delay)
    return () => {
      if (timerRef.current) clearTimeout(timerRef.current)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)
}
