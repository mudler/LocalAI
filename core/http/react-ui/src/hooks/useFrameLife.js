import { useCallback, useEffect, useRef, useState } from 'react'
import { liveFrames } from '../utils/liveFrames'
import { nextFrame } from '../utils/pageTiles'
import { useHidden } from './useHidden'

// The margin around the screen within which a tile that scrolled away keeps
// its frame (paused); further off, the frame is taken down.
const NEAR = '100% 0px'

// useInView watches el: inView while any of it is on screen, near while it is
// within NEAR of the screen. Without IntersectionObserver a tile counts as in
// view, so it still loads.
export function useInView(el) {
  const [view, setView] = useState({ inView: false, near: false })
  useEffect(() => {
    if (!el) return undefined
    if (typeof IntersectionObserver !== 'function') {
      setView({ inView: true, near: true })
      return undefined
    }
    const seen = new IntersectionObserver(([e]) => setView((v) => ({ ...v, inView: e.isIntersecting })))
    const near = new IntersectionObserver(([e]) => setView((v) => ({ ...v, near: e.isIntersecting })), { rootMargin: NEAR })
    seen.observe(el)
    near.observe(el)
    return () => { seen.disconnect(); near.disconnect() }
  }, [el])
  return view
}

// useFrameLife is the life of the frame of a tile or of the page area: built
// once its box is in view, paused out of sight, taken down far away, and held
// against the live frames of the whole app (liveFrames). With every slot
// taken, `capped` is true and the caller shows a static card; runNow is the
// click on it.
//
//   el        the box of the frame (the element, from a state setter)
//   id        the holder id in liveFrames
//   runnable  the page may run (read and allowed)
//   stopped   the frame was stopped (budget or failure): no frame until reset
//
// It returns {frame: {alive, visible}, capped, runNow}.
export function useFrameLife(el, id, { runnable, stopped }) {
  const view = useInView(el)
  const hidden = useHidden()
  const [frame, setFrame] = useState({ alive: false, visible: false })
  const [capped, setCapped] = useState(false) // every live frame of the app is taken
  const [wake, setWake] = useState(0)
  const frameNow = useRef(frame)
  const onEvict = useCallback(() => {
    frameNow.current = { alive: false, visible: false }
    setFrame(frameNow.current)
    setCapped(true)
  }, [])
  useEffect(() => liveFrames.subscribe(() => setWake((n) => n + 1)), [])
  useEffect(() => {
    const prev = frameNow.current
    let next = { alive: false, visible: false }
    let full = false
    if (runnable && !stopped) {
      const want = !prev.alive && view.inView && !hidden
      const slot = liveFrames.holds(id) || (want && liveFrames.acquire(id, onEvict))
      full = want && !slot
      next = nextFrame(prev, { ...view, hidden, slot })
    }
    if (!next.alive) liveFrames.release(id)
    setCapped(full)
    if (next.alive !== prev.alive || next.visible !== prev.visible) {
      frameNow.current = next
      setFrame(next)
    }
  }, [runnable, stopped, id, view, hidden, wake, onEvict])
  useEffect(() => () => liveFrames.release(id), [id])
  const runNow = useCallback(() => {
    if (liveFrames.take(id, onEvict)) {
      setCapped(false)
      setWake((n) => n + 1)
    }
  }, [id, onEvict])
  return { frame, capped, runNow }
}
