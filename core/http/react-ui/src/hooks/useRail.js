import { useEffect, useState } from 'react'

// Keyboard and scroll behaviour of the list rail of the Knowledge and Skills pages.

// The "/" key focuses the search box, as on most developer tools, unless the
// person is already typing somewhere.
export function useSlashToFocus(ref) {
  useEffect(() => {
    const onKey = (e) => {
      if (e.key !== '/' || e.ctrlKey || e.metaKey || e.altKey) return
      const el = e.target
      if (el instanceof HTMLElement && (el.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName))) return
      if (!ref.current) return
      e.preventDefault()
      ref.current.focus()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [ref])
}

// Up and Down move between rows, Home and End jump to the first and last. Only
// one row is in the tab order (the open one, or the one last focused), so Tab
// crosses a list of two hundred in one press and the arrows do the walking.
export function useRovingRows(keys, selectedKey) {
  const [last, setLast] = useState('')
  const stopKey = keys.includes(last) ? last : (keys.includes(selectedKey) ? selectedKey : keys[0])
  const onKeyDown = (e) => {
    if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(e.key)) return
    const rows = Array.from(e.currentTarget.querySelectorAll('[data-roving]'))
    if (rows.length === 0) return
    const at = rows.indexOf(document.activeElement)
    if (at < 0) return
    e.preventDefault()
    const next = e.key === 'Home' ? 0 : e.key === 'End' ? rows.length - 1 : Math.max(0, Math.min(rows.length - 1, at + (e.key === 'ArrowDown' ? 1 : -1)))
    rows[next].focus()
  }
  return { stopKey, onKeyDown, onRowFocus: setLast }
}

// Keeps the open row in view inside the rail without moving the page.
export function useRevealSelected(scrollRef, selectedKey) {
  useEffect(() => {
    const box = scrollRef.current
    const row = box?.querySelector('[aria-current="true"]')
    if (!box || !row) return
    const top = row.offsetTop - box.offsetTop
    if (top < box.scrollTop) box.scrollTop = top - 8
    else if (top + row.offsetHeight > box.scrollTop + box.clientHeight) box.scrollTop = top + row.offsetHeight - box.clientHeight + 8
  }, [scrollRef, selectedKey])
}

// Counts of items by a key, for "Personal 40" in a select.
export function tally(items, keyOf) {
  const counts = new Map()
  for (const it of items) counts.set(keyOf(it), (counts.get(keyOf(it)) || 0) + 1)
  return counts
}
