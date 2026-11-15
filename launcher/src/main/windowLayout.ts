// Where the views of the window go. Pure, no Electron import.
//
// The window holds up to three views, from bottom to top:
//   - shell: the launcher's own React page (the only view with the bridge).
//     It always fills the window. In `webui` layout only its bottom strip (the
//     status bar) is visible, unless it is raised above the WebUI view.
//   - webui: the server's WebUI. Hidden, but kept alive, in `shell-only`.
//   - dim: a see-through layer over the WebUI view, only while the local
//     server is stopped and the page is kept (the "stopped" banner shows).
//     It has the WebUI's bounds, so the banner strip and the bar, which the
//     shell draws outside them, stay clickable. The page underneath stays
//     loaded; the layer only takes its clicks while nothing can answer them.
//   - dot: a tiny view in the bottom-right corner, only while the bar is hidden.
//
// Electron 45 has no click-through for a region of a view: a view takes every
// click inside its bounds. So the shell cannot simply sit above the WebUI with
// holes in it. It is raised above the WebUI only while it must draw over it
// (a menu or popover opening upward from the bar); then it covers the window,
// with a transparent page background where the WebUI should show through, and
// takes the clicks. Banners (update, "stopped") do not raise it: the shell
// reports their height as insets and the WebUI view gives way to them. The
// dot is its own view for the same reason.
import { CORNER_DOT_BOX, CORNER_DOT_MARGIN, MIN_WINDOW_HEIGHT, MIN_WINDOW_WIDTH, STATUS_BAR_HEIGHT } from '../shared/layout'
import type { DotTone, LayoutMode } from '../shared/types'

export interface Rect {
  x: number
  y: number
  width: number
  height: number
}

export interface LayoutInput {
  /** Content size of the window. */
  width: number
  height: number
  mode: LayoutMode
  /** The status bar is shown (the saved choice, or forced by a state that needs the user). */
  barVisible: boolean
  /** The shell must draw above the WebUI view (a popover of the bar is open). */
  shellAbove: boolean
  /** Strips the shell draws above and below the view, besides the bar (ShellInsets). */
  insetTop?: number
  insetBottom?: number
  /** Dim the WebUI view (see dimWanted). */
  dim?: boolean
}

export type ViewName = 'shell' | 'webui' | 'dim' | 'dot'

export interface Layout {
  shell: Rect
  /** undefined: the WebUI view is hidden (not destroyed, not reloaded). */
  webui?: Rect
  /** undefined: no dot. */
  dot?: Rect
  /** undefined: the WebUI is not dimmed. Same bounds as `webui` otherwise. */
  dim?: Rect
  /** The visible views from bottom to top. */
  order: ViewName[]
}

const whole = (n: number) => Math.max(0, Math.floor(n))

export function computeLayout(i: LayoutInput): Layout {
  const width = whole(i.width)
  const height = whole(i.height)
  const shell: Rect = { x: 0, y: 0, width, height }
  if (i.mode === 'shell-only') return { shell, order: ['shell'] }

  const bar = i.barVisible ? Math.min(STATUS_BAR_HEIGHT, height) : 0
  // The strips never take more than the window has; the view keeps the rest.
  const top = Math.min(whole(i.insetTop ?? 0), height - bar)
  const bottom = Math.min(whole(i.insetBottom ?? 0), height - bar - top)
  const webui: Rect = { x: 0, y: top, width, height: height - bar - top - bottom }
  let dot: Rect | undefined
  if (!i.barVisible) {
    const size = Math.min(CORNER_DOT_BOX, width, height)
    dot = {
      x: Math.max(0, width - CORNER_DOT_MARGIN - size),
      y: Math.max(0, height - CORNER_DOT_MARGIN - size),
      width: size,
      height: size,
    }
  }
  const dim = i.dim ? { ...webui } : undefined
  const order: ViewName[] = i.shellAbove ? ['webui', 'shell'] : ['shell', 'webui']
  // The dim layer sits right on the WebUI: below a raised shell (its popovers draw over it) and below the dot.
  if (dim) order.splice(order.indexOf('webui') + 1, 0, 'dim')
  if (dot) order.push('dot')
  return { shell, webui, dot, dim, order }
}

/**
 * The WebUI is dimmed while the "stopped" banner shows over it (owner
 * decision 2026-11-14). Not while starting: the page comes back by itself.
 */
export function dimWanted(s: { layout: LayoutMode; banner: 'stopped' | 'starting' | null }): boolean {
  return s.layout === 'webui' && s.banner === 'stopped'
}

/**
 * The dim layer's page: a flat wash of the launcher's page colour at 45%,
 * which looks like the mocks' 55% opacity on the WebUI underneath. No
 * script, no link, nothing to load.
 */
export const DIM_PAGE = `data:text/html;charset=utf-8,${encodeURIComponent(
  '<!doctype html><html><head><meta charset="utf-8">' +
    "<meta http-equiv=\"Content-Security-Policy\" content=\"default-src 'none'; style-src 'unsafe-inline'\">" +
    '<style>' +
    'html,body{margin:0;height:100%;overflow:hidden;cursor:default;background:rgba(255,253,247,0.45)}' +
    '@media (prefers-color-scheme: dark){html,body{background:rgba(20,28,27,0.45)}}' +
    '</style></head><body></body></html>',
)}`

/** The smallest content size of the window. */
export const MIN_SIZE = { width: MIN_WINDOW_WIDTH, height: MIN_WINDOW_HEIGHT } as const

/** Clamps a requested content size to the minimum. */
export function clampSize(width: number, height: number): { width: number; height: number } {
  return { width: Math.max(MIN_SIZE.width, whole(width)), height: Math.max(MIN_SIZE.height, whole(height)) }
}

/** True when two layouts put the views in a different stacking order. */
export function orderChanged(a: Layout | undefined, b: Layout): boolean {
  return !a || a.order.join(',') !== b.order.join(',')
}

// ---- the corner dot -------------------------------------------------------------

/** Colours from the launcher mocks: light and dark theme. */
const DOT_COLOURS: Record<DotTone, [light: string, dark: string]> = {
  ok: ['#1f7a4c', '#7fd39b'],
  busy: ['#8c5400', '#ffc93c'],
  failed: ['#b3261e', '#ff9d92'],
  stopped: ['#4d5478', '#b9bfd0'],
}

const DOT_TITLES: Record<DotTone, string> = {
  ok: 'LocalAI is running. Show status bar',
  busy: 'LocalAI is busy. Show status bar',
  failed: 'LocalAI failed. Show status bar',
  stopped: 'LocalAI is stopped. Show status bar',
}

/**
 * The link the dot page navigates to on click. The main process cancels that
 * navigation in will-navigate and shows the bar; the `.invalid` top-level
 * domain can never resolve, so nothing leaves the computer even if it did not.
 */
export const DOT_CLICK_URL = 'http://show-status-bar.invalid/'

/**
 * The dot view's page, as a data URL. It has no script: a plain link is
 * enough, and the view gets no preload and no bridge.
 */
export function cornerDotPage(tone: DotTone): string {
  const [light, dark] = DOT_COLOURS[tone]
  const html =
    '<!doctype html><html><head><meta charset="utf-8">' +
    "<meta http-equiv=\"Content-Security-Policy\" content=\"default-src 'none'; style-src 'unsafe-inline'\">" +
    '<style>' +
    'html,body{margin:0;height:100%;background:transparent;overflow:hidden}' +
    'a{display:flex;width:100%;height:100%;align-items:center;justify-content:center;cursor:pointer}' +
    `i{width:12px;height:12px;border-radius:50%;background:${light};box-shadow:0 0 0 3px #fffdf7,0 0 0 4px #e6d9bd}` +
    `@media (prefers-color-scheme: dark){i{background:${dark};box-shadow:0 0 0 3px #141c1b,0 0 0 4px #27332f}}` +
    '</style></head><body>' +
    `<a href="${DOT_CLICK_URL}" title="${DOT_TITLES[tone]}" aria-label="${DOT_TITLES[tone]}"><i></i></a>` +
    '</body></html>'
  return `data:text/html;charset=utf-8,${encodeURIComponent(html)}`
}
