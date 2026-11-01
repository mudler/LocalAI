import { useLayoutEffect, useRef } from 'react'

const EASE = 'cubic-bezier(0.65, 0, 0.35, 1)' // --ease-in-out

function tokenMs(name, fallback) {
  const v = parseFloat(getComputedStyle(document.documentElement).getPropertyValue(name))
  return Number.isFinite(v) ? v : fallback
}

// useSwapMotion moves two rows of a list to their new places when "Move up"
// or "Move down" swaps them: each row starts where the other was and slides
// home (transforms only, --dur-base, --ease-in-out), so the eye follows the
// item that moved. Focus follows the moved item, so a second press moves it
// again. Under reduced motion the rows swap at once.
//
// listRef is the list element; its children are the rows, in order. Call
// swap(from, to) just before the state change that swaps rows from and to.
export function useSwapMotion(deps) {
  const listRef = useRef(null)
  const pending = useRef(null)

  const swap = (from, to) => {
    const rows = listRef.current?.children
    if (!rows?.[from] || !rows?.[to]) return
    const focusIndex = Array.from(rows[from].querySelectorAll('button')).indexOf(document.activeElement)
    pending.current = { from, to, a: rows[from].getBoundingClientRect().top, b: rows[to].getBoundingClientRect().top, focusIndex }
  }

  useLayoutEffect(() => {
    const p = pending.current
    pending.current = null
    const rows = listRef.current?.children
    if (!p || !rows?.[p.from] || !rows?.[p.to]) return
    // The item that was at `from` is now drawn in the row at `to`.
    if (p.focusIndex >= 0) {
      const buttons = rows[p.to].querySelectorAll('button')
      const target = buttons[p.focusIndex]
      if (target && !target.disabled) target.focus({ preventScroll: true })
      else rows[p.to].querySelector('button:not(:disabled)')?.focus({ preventScroll: true })
    }
    const reduce = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
    if (reduce || typeof rows[p.to].animate !== 'function') return
    const duration = tokenMs('--dur-base', 200)
    // The moved item is lifted a little (scale 1.02) and settles as it lands.
    const slide = (el, dy, lift) => el.animate(
      [{ transform: `translateY(${dy}px) scale(${lift})` }, { transform: 'translateY(0) scale(1)' }],
      { duration, easing: EASE },
    )
    slide(rows[p.to], p.a - p.b, 1.02)
    slide(rows[p.from], p.b - p.a, 1)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)

  return { listRef, swap }
}
