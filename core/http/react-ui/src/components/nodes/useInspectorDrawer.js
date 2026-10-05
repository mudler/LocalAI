import { useEffect, useRef, useState } from 'react'

const MOBILE_DRAWER_QUERY = '(max-width: 768px)'
const FOCUSABLE = 'button:not(:disabled), [href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])'

function isolateBackground(drawer) {
  const changed = new Map()
  let current = drawer

  while (current?.parentElement && current.parentElement !== document.documentElement) {
    const parent = current.parentElement
    for (const sibling of parent.children) {
      if (sibling === current || sibling.classList.contains('node-inspector__scrim') || changed.has(sibling)) continue
      changed.set(sibling, {
        ariaHidden: sibling.getAttribute('aria-hidden'),
        inert: sibling.hasAttribute('inert'),
      })
      sibling.setAttribute('aria-hidden', 'true')
      sibling.setAttribute('inert', '')
    }
    current = parent
  }

  return () => {
    for (const [element, previous] of changed) {
      if (previous.ariaHidden === null) element.removeAttribute('aria-hidden')
      else element.setAttribute('aria-hidden', previous.ariaHidden)
      if (!previous.inert) element.removeAttribute('inert')
    }
  }
}

export default function useInspectorDrawer(open, onClose, drawerRef) {
  const onCloseRef = useRef(onClose)
  const [modal, setModal] = useState(() => typeof window !== 'undefined' && window.matchMedia(MOBILE_DRAWER_QUERY).matches)

  useEffect(() => { onCloseRef.current = onClose }, [onClose])

  useEffect(() => {
    const media = window.matchMedia(MOBILE_DRAWER_QUERY)
    const update = event => setModal(event.matches)
    setModal(media.matches)
    media.addEventListener('change', update)
    return () => media.removeEventListener('change', update)
  }, [])

  useEffect(() => {
    if (!open) return undefined
    const drawer = drawerRef.current
    if (!drawer) return undefined
    const previousOverflow = document.body.style.overflow
    const restoreBackground = modal ? isolateBackground(drawer) : () => {}
    if (modal) document.body.style.overflow = 'hidden'

    const handleKeyDown = event => {
      const blockingModal = [...document.querySelectorAll('[aria-modal="true"]')].some(element => element !== drawer)
      if (event.defaultPrevented || blockingModal) return
      if (event.key === 'Escape') {
        event.preventDefault()
        onCloseRef.current()
        return
      }
      if (!modal || event.key !== 'Tab') return
      const focusable = [...drawer.querySelectorAll(FOCUSABLE)].filter(element => element.getClientRects().length > 0)
      if (!focusable.length) {
        event.preventDefault()
        drawer.focus()
        return
      }
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (event.shiftKey && (document.activeElement === first || !drawer.contains(document.activeElement))) {
        event.preventDefault()
        last.focus()
      } else if (!event.shiftKey && (document.activeElement === last || !drawer.contains(document.activeElement))) {
        event.preventDefault()
        first.focus()
      }
    }

    document.addEventListener('keydown', handleKeyDown)
    return () => {
      document.removeEventListener('keydown', handleKeyDown)
      restoreBackground()
      if (modal) document.body.style.overflow = previousOverflow
    }
  }, [drawerRef, modal, open])

  return modal
}
