// Pure rules of the page area of Home: the pageArea of home.json names a page
// that fills a full-width area below the composer. No React, no network, no
// DOM here; the area component and the unit tests use the same functions.
//
// The shell decides whether the area works from what it sees: the read of the
// page, the load of the frame, the first beat of the bridge, a timer. It never
// takes the page's word for it. A failed area shows a tile; nothing else on
// Home changes.
import { tileRef } from './pageTiles.js'

// The frame must show its first sign of life within this time of its start.
export const AREA_PAINT_MS = 5000

// The height of the area below its strip. A page can report the height of its
// content (localai.page.resize): the area follows it between AREA_MIN_PX and
// about 60% of the window (the cap is in pageArea.css, so it follows the
// window). Before a report, and for a page that never reports, the area is
// AREA_DEFAULT_PX high and the page scrolls inside it. AREA_MAX_PX only bounds
// the number a page can send.
export const AREA_DEFAULT_PX = 240
export const AREA_MIN_PX = 160
export const AREA_MAX_PX = 4000
// At most one height per this period, the last one asked; a step smaller than
// AREA_STEP_PX is not applied, so a page that jitters by a pixel causes no
// layout.
export const AREA_RESIZE_MS = 250
export const AREA_STEP_PX = 4

// areaHeight is a height a page reported, in whole pixels within the limits,
// or null when it is not a height.
export function areaHeight(v) {
  if (typeof v !== 'number' || !Number.isFinite(v) || v < 0) return null
  return Math.min(AREA_MAX_PX, Math.max(AREA_MIN_PX, Math.ceil(v)))
}

// createAreaSizer takes the heights a page reports (offer) and gives apply at
// most one per AREA_RESIZE_MS: the first at once, then the last of a burst at
// the end of the period. The frame is never reloaded; apply only sets the
// height of the area. offer returns the clamped height, or null. stop drops
// what waits.
export function createAreaSizer({ apply, now = Date.now, timer = setTimeout, clear = clearTimeout, every = AREA_RESIZE_MS }) {
  let last = -Infinity
  let shown = null
  let pending = null
  let handle = 0
  const flush = () => {
    handle = 0
    if (pending === null) return
    const h = pending
    pending = null
    last = now()
    if (shown !== null && Math.abs(h - shown) < AREA_STEP_PX) return
    shown = h
    apply(h)
  }
  return {
    offer(v) {
      const h = areaHeight(v)
      if (h === null) return null
      pending = h
      if (!handle) {
        const wait = last + every - now()
        if (wait <= 0) flush()
        else handle = timer(flush, wait)
      }
      return h
    },
    stop() {
      if (handle) clear(handle)
      handle = 0
      pending = null
    },
  }
}

// areaTarget reads the pageArea value of home.json:
//
//   none     no value, or null: no page area
//   page     {ref: {name, folder}}: a page name of your home, or {page, folder}
//   invalid  anything else: the area shows the failure tile
export function areaTarget(value) {
  if (value === undefined || value === null) return { kind: 'none', ref: null }
  const named = typeof value === 'string' ? { page: value } : value
  if (named && typeof named === 'object' && !Array.isArray(named)) {
    const ref = tileRef(named)
    if (ref) return { kind: 'page', ref }
  }
  return { kind: 'invalid', ref: null }
}

// areaValue is what home.json stores for a ref: null, a page name of your
// home, or {page, folder} for a shared page.
export function areaValue(ref) {
  if (!ref || !ref.name) return null
  return ref.folder ? { page: ref.name, folder: ref.folder } : ref.name
}

// areaKey is the key of a ref in the picker (the key of eligiblePages), '' for none.
export const areaKey = (ref) => (ref ? (ref.folder ? `${ref.folder}/${ref.name}` : ref.name) : '')

// The life of the frame of the area, one step at a time:
//
//   idle      no frame yet
//   starting  the frame was built; no sign of life yet
//   running   the first beat came
//   failed    {reason}: script (the page threw before its first beat),
//             navigated, busy, silent, noStart (the budget) or noPaint (the
//             timer ran out). Only a manual reload leaves it: there is no retry.
export const AREA_IDLE = Object.freeze({ phase: 'idle', reason: '' })

// areaStep is the reducer. Events: start (the frame was built), ready (its
// first beat), error (a script error of the page), navigated (the frame left
// its document), stopped (the budget stopped it, with a reason), timeout (the
// timer of AREA_PAINT_MS) and reload (the person asked).
export function areaStep(state, event) {
  const type = event?.type
  if (type === 'reload') return { phase: 'starting', reason: '' }
  if (type === 'reset') return AREA_IDLE
  if (state.phase === 'failed') return state
  switch (type) {
    case 'start': return state.phase === 'idle' ? { phase: 'starting', reason: '' } : state
    case 'ready': return state.phase === 'starting' ? { phase: 'running', reason: '' } : state
    // An error after the first beat is the page's business: it may be a click
    // handler. Before it, the page did not start.
    case 'error': return state.phase === 'starting' ? { phase: 'failed', reason: 'script', detail: event.detail } : state
    case 'navigated': return state.phase === 'idle' ? state : { phase: 'failed', reason: 'navigated' }
    case 'stopped': return state.phase === 'idle' ? state : { phase: 'failed', reason: ['busy', 'silent', 'noStart'].includes(event.reason) ? event.reason : 'busy' }
    case 'timeout': return state.phase === 'starting' ? { phase: 'failed', reason: 'noPaint' } : state
    default: return state
  }
}

// areaView says what the area shows:
//
//   none     nothing (no page area)
//   off      the note: an administrator turned the page area off
//   failed   the failure tile, with a reason
//   loading  the page is being read
//   allow    a shared page the viewer has not allowed on Home: the consent state
//   run      the frame runs (or waits for a slot of the live frames)
//
//   target   areaTarget of the value
//   enabled  false when an administrator turned the page area off; null while
//            that is not known yet
//   read     tileState of the page read ('loading', 'missing', 'error',
//            'needsAllow', 'off', 'ready')
//   life     the state of areaStep
export function areaView({ target, enabled, read, life }) {
  if (target.kind === 'none') return { view: 'none', reason: '' }
  if (enabled === false) return { view: 'off', reason: '' }
  if (enabled === null) return { view: 'loading', reason: '' }
  if (target.kind === 'invalid') return { view: 'failed', reason: 'invalid' }
  if (read === 'loading') return { view: 'loading', reason: '' }
  if (read === 'missing') return { view: 'failed', reason: 'missing' }
  if (read === 'error') return { view: 'failed', reason: 'read' }
  if (read === 'off') return { view: 'failed', reason: 'sharedOff' }
  if (read === 'needsAllow') return { view: 'allow', reason: '' }
  if (life.phase === 'failed') return { view: 'failed', reason: life.reason }
  return { view: 'run', reason: '' }
}

// revertTarget says what Revert restores for a failure: the home.json (the
// value is invalid) or the page (an own page that broke). A shared page is
// not the viewer's file: no revert.
export function revertTarget(reason, ref) {
  if (reason === 'invalid') return 'home'
  if (!ref || ref.folder) return ''
  return 'page'
}

// areaChoices are the pages the picker offers for the area: every page of the
// user's home (the page needs no widget block), then the pages of the shared
// folders, each with the ref home.json stores. A shared page still asks the
// viewer to allow it on Home.
export function areaChoices(own, shared) {
  const out = []
  for (const p of Array.isArray(own) ? own : []) {
    const ref = tileRef({ page: p?.name })
    if (ref) out.push({ key: areaKey(ref), ref, page: p })
  }
  for (const p of Array.isArray(shared) ? shared : []) {
    const ref = tileRef({ page: p?.name, folder: p?.folder_id })
    if (ref && ref.folder) out.push({ key: areaKey(ref), ref, page: p })
  }
  return out
}
