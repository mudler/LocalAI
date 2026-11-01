import { useCallback, useEffect, useRef, useState } from 'react'

let nextLink = 0

// usePageLinks keeps the one link a page asks to open. A new ask answers the
// one before it with {opened: false}; so does leaving the page. onLink goes to
// PageFrame; open and cancel go to PageLinkAsk.
export function usePageLinks() {
  const [link, setLink] = useState(null) // {id, target, resolve}
  const current = useRef(null)

  const onLink = useCallback((target) => new Promise((resolve) => {
    current.current?.resolve({ opened: false })
    const next = { id: ++nextLink, target, resolve }
    current.current = next
    setLink(next)
  }), [])

  const settle = (opened) => {
    const l = current.current
    if (!l) return
    current.current = null
    setLink(null)
    l.resolve({ opened })
  }
  // Open is a click in this document, so the new tab is not a blocked pop-up.
  // noopener and noreferrer: the site gets no handle on LocalAI and no
  // address of it.
  const open = () => {
    const l = current.current
    if (!l) return
    window.open(l.target.href, '_blank', 'noopener,noreferrer')
    settle(true)
  }
  const cancel = () => settle(false)

  useEffect(() => () => { current.current?.resolve({ opened: false }) }, [])
  return { link, onLink, open, cancel }
}
