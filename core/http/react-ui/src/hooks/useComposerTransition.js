import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { preloadRoute } from '../router'
import {
  offerHandoff, peekHandoff, clearHandoff, handoffSeed, withHandoff, whenArrived, markArrived,
} from '../utils/composerHandoff'

// The move from Home to the chat a first message opens, as one gesture: the
// composer glides from its place on Home to the dock of the chat, the text
// lifts out of it into the message, and the rest of Home fades away while the
// chat fades in. Home uses useComposerDeparture, the chat page
// useComposerArrival; composerHandoff carries the message between them.
//
// How it moves (motionMode):
//   glide  a view transition: the browser captures Home, the chat page
//          renders, and the parts named in chat.css (composer, text, files,
//          sidebar) move between the two captures
//   flip   no View Transitions API: Home fades, the chat page renders, and
//          the composer and the message slide from where they were on Home
//   fade   less motion asked for: a short cross-fade, nothing moves
//   none   less motion and no API: the chat replaces Home at once
// Every mode sets data-handoff on <html> while it runs; the rules for it in
// chat.css and home.css name the parts and stop the transitions that would
// move the layout under them.

function prefersLessMotion() {
  try { return window.matchMedia('(prefers-reduced-motion: reduce)').matches } catch { return false }
}

export function motionMode() {
  const api = typeof document !== 'undefined' && typeof document.startViewTransition === 'function'
  if (prefersLessMotion()) return api ? 'fade' : 'none'
  return api ? 'glide' : 'flip'
}

// A duration token of theme.css in ms, for the Web Animations API.
function tokenMs(name, fallback) {
  const v = getComputedStyle(document.documentElement).getPropertyValue(name).trim()
  const n = parseFloat(v)
  if (!Number.isFinite(n)) return fallback
  return v.endsWith('s') && !v.endsWith('ms') ? n * 1000 : n
}

function token(name, fallback) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || fallback
}

function box(el) {
  const r = el?.getBoundingClientRect()
  return r ? { left: r.left, top: r.top, width: r.width, height: r.height } : null
}

function endHandoff(mode) {
  const html = document.documentElement
  if (html.dataset.handoff === mode) delete html.dataset.handoff
}

// The small shake of a composer whose message did not go: it says "not sent"
// where the eyes are, and the text stays in it.
const SHAKE = [
  { transform: 'translateX(0)' },
  { transform: 'translateX(-6px)' },
  { transform: 'translateX(5px)' },
  { transform: 'translateX(-3px)' },
  { transform: 'translateX(2px)' },
  { transform: 'translateX(0)' },
]

// What the chat shows of the files sent with the message: the name, the
// size, the type and, for an image, the address of the copy the browser
// holds, so its thumbnail shows before the server has stored it.
function describeFiles(files) {
  return (files || []).map((f) => {
    const image = (f.type || '').startsWith('image/')
    let url
    if (image && typeof Blob !== 'undefined' && f instanceof Blob) {
      try { url = URL.createObjectURL(f) } catch { url = undefined }
    }
    return { name: f.name, size: f.size || 0, type: f.type || '', url }
  })
}

// Home's side. rootRef is the box around Home's composer.
//   depart    'out' from Send until the chat opens or the send fails (the page
//             fades what is not the composer), 'back' while it comes back
//             after a failure, '' otherwise
//   begin()   claims the send; false while one is in flight, so a second
//             Enter in the same frame does not send twice
//   handOff(handoff, go)   leaves the hand-off and runs go (clears the draft
//             and opens the chat) inside the transition
//   fail()    brings Home back, shakes the composer and focuses it again
export function useComposerDeparture(rootRef) {
  const [depart, setDepart] = useState('')
  const busyRef = useRef(false)
  const leftAtRef = useRef(0)
  const backRef = useRef(null)
  useEffect(() => () => clearTimeout(backRef.current), [])

  const begin = useCallback(() => {
    if (busyRef.current) return false
    busyRef.current = true
    leftAtRef.current = performance.now()
    clearTimeout(backRef.current)
    setDepart('out')
    // The chat page is a separate chunk: fetch it while the message goes.
    preloadRoute('/app/chats')
    return true
  }, [])

  const fail = useCallback(() => {
    busyRef.current = false
    setDepart('back')
    backRef.current = setTimeout(() => setDepart(''), tokenMs('--dur-base', 200))
    const root = rootRef.current
    const card = root?.querySelector('form.home-cmd')
    if (card && !prefersLessMotion() && typeof card.animate === 'function') {
      card.animate(SHAKE, { duration: tokenMs('--dur-slow', 280), easing: token('--ease-out', 'ease-out') })
    }
    root?.querySelector('textarea')?.focus()
  }, [rootRef])

  const handOff = useCallback(async (handoff, go) => {
    const root = rootRef.current
    const mode = motionMode()
    offerHandoff({
      ...handoff,
      files: describeFiles(handoff.files),
      mode,
      from: { composer: box(root?.querySelector('form.home-cmd')), text: box(root?.querySelector('.home-textarea')) },
    })
    const html = document.documentElement
    const id = handoff.chat.id
    if (mode === 'glide' || mode === 'fade') {
      html.dataset.handoff = mode
      let vt
      try {
        vt = document.startViewTransition(async () => {
          go()
          await whenArrived(id)
        })
      } catch {
        endHandoff(mode)
        go()
        return
      }
      // A click while it lands finishes it at once; typing does not, the
      // composer already has the focus.
      const skip = () => vt.skipTransition()
      document.addEventListener('pointerdown', skip, { once: true, capture: true })
      vt.finished.finally(() => {
        document.removeEventListener('pointerdown', skip, { capture: true })
        endHandoff(mode)
      })
      return
    }
    if (mode === 'flip') {
      // Let Home finish fading before the chat replaces it; the chat page
      // then slides the composer and the message from where they were.
      html.dataset.handoff = mode
      const left = tokenMs('--dur-base', 200) - (performance.now() - leftAtRef.current)
      if (left > 0) await new Promise(r => setTimeout(r, left))
    }
    go()
  }, [rootRef])

  return { depart, begin, fail, handOff }
}

// Slides the parts the fallback moves from their place on Home to their place
// in the chat (FLIP: measure, invert, play), with transform and opacity only.
function flipFrom(from, page) {
  const html = document.documentElement
  const duration = tokenMs('--dur-glide', 360)
  const easing = token('--ease-out-strong', 'ease-out')
  const moves = []
  const slide = (el, rect, fade) => {
    if (!el || !rect || typeof el.animate !== 'function') return
    const r = el.getBoundingClientRect()
    const dx = rect.left - r.left
    const dy = rect.top - r.top
    const first = { transform: `translate(${dx}px, ${dy}px)` }
    if (fade) first.opacity = 0
    moves.push(el.animate([first, { transform: 'none', opacity: 1 }], { duration, easing }))
  }
  slide(page?.querySelector('.cx-dock form.home-cmd'), from?.composer, false)
  slide(page?.querySelector('.cx-msg[data-handoff] .cx-bubble'), from?.text, true)
  Promise.allSettled(moves.map(m => m.finished)).then(() => endHandoff('flip'))
  if (moves.length === 0) delete html.dataset.handoff
}

// The chat page's side, called before useServerChat.
//   handoff    the hand-off of this chat, or null (any other way in)
//   seed       what useServerChat starts from
//   messages(list, activeId)   the transcript with the handed-off message
//   pageRef    the chat page's root, for the fallback slide
// On the first commit it focuses the composer, plays the fallback slide when
// the browser has no view transitions, and tells Home the chat is drawn.
export function useComposerArrival(routeId, { textareaRef, pageRef }) {
  const [handoff] = useState(() => peekHandoff(routeId))
  const seed = useMemo(() => handoffSeed(handoff), [handoff])

  useLayoutEffect(() => {
    if (!handoff) return
    textareaRef.current?.focus({ preventScroll: true })
    if (handoff.mode !== 'flip') return undefined
    // Measured in the next frame, before it is painted: the layout effects of
    // this commit (focus mode folding the sidebar) have moved the page by then.
    const frame = requestAnimationFrame(() => flipFrom(handoff.from, pageRef.current))
    return () => cancelAnimationFrame(frame)
  // Runs once: the hand-off is the first render's.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    if (!handoff) return undefined
    const id = handoff.chat.id
    clearHandoff(id)
    // The passive effects of this commit (focus mode folding the sidebar)
    // have run; let React apply what they set before the capture.
    const timer = setTimeout(() => markArrived(id), 16)
    return () => clearTimeout(timer)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const messages = useCallback((list, activeId) => withHandoff(list, handoff, activeId), [handoff])
  return { handoff, seed, messages }
}
