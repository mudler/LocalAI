import { useCallback, useMemo, useState } from 'react'

// The nodes a page portals its supporting line and actions into (HubHeader),
// and how many pages fill the line, so a layout can show its own default line
// only when none does.
export function useHeaderSlots() {
  const [supporting, setSupporting] = useState(null)
  const [actions, setActions] = useState(null)
  const [filled, setFilled] = useState(0)
  const claim = useCallback((delta) => setFilled(n => Math.max(0, n + delta)), [])
  const value = useMemo(() => ({ supporting, actions, claim }), [supporting, actions, claim])
  return { value, filled, setSupporting, setActions }
}
