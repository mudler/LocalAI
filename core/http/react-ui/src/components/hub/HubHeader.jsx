import { useContext, useLayoutEffect } from 'react'
import { createPortal } from 'react-dom'
import { HubHeaderContext } from './hubHeaderContext'

// The Administration layout (admin/AdminLayout) keeps a header zone above the
// page. A page says what is true right now (the supporting line) and what to
// do about it (the actions) by rendering <HubHeader>. The parts are portalled
// into that zone, so the page keeps its own state and handlers and the layout
// never re-renders the page.
export function HubHeader({ supporting, actions }) {
  const slots = useContext(HubHeaderContext)
  const claim = slots?.claim
  const fills = Boolean(supporting)
  // Tells the layout that a page fills the line.
  useLayoutEffect(() => {
    if (!claim || !fills) return undefined
    claim(1)
    return () => claim(-1)
  }, [claim, fills])
  if (!slots) {
    // Outside a hub (a page rendered on its own): draw the parts in place.
    if (!supporting && !actions) return null
    return (
      <div className="hub-head-inline">
        {supporting && <div className="page-header__supporting">{supporting}</div>}
        {actions && <div className="page-header__meta">{actions}</div>}
      </div>
    )
  }
  return (
    <>
      {supporting && slots.supporting && createPortal(supporting, slots.supporting)}
      {actions && slots.actions && createPortal(actions, slots.actions)}
    </>
  )
}
