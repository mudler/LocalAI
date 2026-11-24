import { useEffect, useId, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'
import Button from './Button'
import usePresence from './usePresence'
import { pushLayer } from '../../utils/modalLayers'

const OVERLAY_QUERY = '(max-width: 1279px)'
const EXIT_MS = 160

function useOverlay() {
  const [overlay, setOverlay] = useState(() => typeof window !== 'undefined' && !!window.matchMedia?.(OVERLAY_QUERY).matches)
  useEffect(() => {
    const mq = window.matchMedia?.(OVERLAY_QUERY)
    if (!mq) return undefined
    const on = () => setOverlay(mq.matches)
    mq.addEventListener('change', on)
    return () => mq.removeEventListener('change', on)
  }, [])
  return overlay
}

// A detail pane beside a list: the Files pane, add-on and model details, an
// inspector. 360 px (400 with `wide`). The header (title, meta row, close)
// stays; the body scrolls.
//
// From 1280 px it docks: put it in a <PaneLayout> next to the main column and
// it pushes the content. Below that it overlays the page with a scrim, and
// below 640 px it is a bottom sheet. Escape closes it and focus returns to
// the control that opened it.
//
//   open      shows the pane; it slides in and out (transform and opacity)
//   title     the pane title (17/600)
//   meta      a short meta row under the title
//   actions   controls drawn in the header before the close button
//   footer    controls fixed at the bottom
//   onClose   called by close, Escape and a click on the scrim
export default function SidePane({ open, title, meta, actions, footer, onClose, wide = false, className = '', testId, children }) {
  const { t } = useTranslation('common')
  const { mounted, phase } = usePresence(open, EXIT_MS)
  const overlay = useOverlay()
  const paneRef = useRef(null)
  const closeRef = useRef(onClose)
  closeRef.current = onClose
  const titleId = useId()

  useEffect(() => {
    if (!open) return undefined
    const opener = document.activeElement
    const layer = pushLayer()
    paneRef.current?.focus({ preventScroll: true })
    const onKey = (e) => {
      if (e.key !== 'Escape' || !layer.isTop()) return
      if (e.target?.closest?.('[role="listbox"]') || e.target?.getAttribute?.('aria-expanded') === 'true') return
      e.preventDefault()
      closeRef.current?.()
    }
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('keydown', onKey)
      layer.pop()
      if (opener && document.contains(opener)) opener.focus?.({ preventScroll: true })
    }
  }, [open])

  if (!mounted) return null
  const pane = (
    <>
      {overlay && <div className="ui-pane-scrim" data-state={phase} onMouseDown={() => closeRef.current?.()} aria-hidden="true" />}
      <aside
        ref={paneRef}
        className={`ui-pane${wide ? ' ui-pane--wide' : ''} ${className}`.trim()}
        data-state={phase}
        role={overlay ? 'dialog' : 'complementary'}
        aria-modal={overlay || undefined}
        aria-labelledby={titleId}
        tabIndex={-1}
        data-testid={testId}
      >
        <span className="ui-pane__grip" aria-hidden="true" />
        <div className="ui-pane__head">
          <div className="ui-pane__heading">
            <h2 className="ui-pane__title" id={titleId}>{title}</h2>
            {meta && <div className="ui-pane__meta">{meta}</div>}
          </div>
          {actions}
          <Button variant="ghost" size="sm" icon="close" aria-label={t('actions.close')} onClick={() => closeRef.current?.()} />
        </div>
        <div className="ui-pane__body">{children}</div>
        {footer && <div className="ui-pane__foot">{footer}</div>}
      </aside>
    </>
  )
  // An overlay is fixed to the window. A page wrapper with a transform (the
  // route transition) would hold it instead, so it renders under <body>.
  return overlay ? createPortal(pane, document.body) : pane
}

// The row that holds a main column and a docked pane.
export function PaneLayout({ className = '', children }) {
  return <div className={`ui-pane-layout ${className}`.trim()}>{children}</div>
}
