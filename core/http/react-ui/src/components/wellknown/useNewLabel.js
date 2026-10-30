import { useEffect, useRef } from 'react'
import { useSeen } from '../../hooks/useSeen'
import { markNewOpened, markNewSeen, newLabel, today } from '../../utils/discovery'

// useNewLabel says whether the sidebar entry of an area (pages) shows its
// one-time "New" label. The first time it shows, the date is kept under
// seen.new in user.json; the label goes once the user opens the entry
// (visiting is true) or after a week. "Notes about new things" off
// (seen.new_off) hides it. No count, no dot, no motion.
export function useNewLabel(area, { enabled, visiting }) {
  const { seen, loaded, doc, update } = useSeen({ enabled })
  const state = enabled && loaded && doc ? newLabel(seen, area) : 'hide'
  const wrote = useRef('')
  useEffect(() => {
    if (!enabled || !loaded || !doc) return
    if (visiting && seen?.new?.[area] !== 'opened' && !seen?.new_off) {
      if (wrote.current === 'opened') return
      wrote.current = 'opened'
      update(s => markNewOpened(s, area)).catch(() => { wrote.current = '' })
    } else if (state === 'first' && !visiting && wrote.current === '') {
      wrote.current = 'first'
      update(s => markNewSeen(s, area, today())).catch(() => { wrote.current = '' })
    }
  }, [enabled, loaded, doc, visiting, state, seen, area, update])
  return enabled && !visiting && (state === 'first' || state === 'show')
}
