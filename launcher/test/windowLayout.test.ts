import { describe, expect, it } from 'vitest'
import { CORNER_DOT_BOX, CORNER_DOT_MARGIN, MIN_WINDOW_HEIGHT, MIN_WINDOW_WIDTH, STATUS_BAR_HEIGHT } from '../src/shared/layout'
import { clampSize, computeLayout, cornerDotPage, DIM_PAGE, dimWanted, DOT_CLICK_URL, orderChanged } from '../src/main/windowLayout'

const base = { width: 1100, height: 720, barVisible: true, shellAbove: false }

describe('computeLayout', () => {
  it('shell-only: the shell fills the window and the WebUI view is hidden', () => {
    for (const barVisible of [true, false]) {
      const l = computeLayout({ ...base, barVisible, mode: 'shell-only' })
      expect(l.shell).toEqual({ x: 0, y: 0, width: 1100, height: 720 })
      expect(l.webui).toBeUndefined()
      expect(l.dot).toBeUndefined()
      expect(l.order).toEqual(['shell'])
    }
  })

  it('webui with the bar: the WebUI sits above the bar, the shell below it', () => {
    const l = computeLayout({ ...base, mode: 'webui' })
    expect(l.webui).toEqual({ x: 0, y: 0, width: 1100, height: 720 - STATUS_BAR_HEIGHT })
    expect(l.shell).toEqual({ x: 0, y: 0, width: 1100, height: 720 })
    expect(l.dot).toBeUndefined()
    expect(l.order).toEqual(['shell', 'webui'])
    expect(STATUS_BAR_HEIGHT).toBe(30)
  })

  it('webui with the bar hidden: the WebUI fills the window and the dot sits in the corner', () => {
    const l = computeLayout({ ...base, mode: 'webui', barVisible: false })
    expect(l.webui).toEqual({ x: 0, y: 0, width: 1100, height: 720 })
    expect(l.dot).toEqual({
      x: 1100 - CORNER_DOT_MARGIN - CORNER_DOT_BOX,
      y: 720 - CORNER_DOT_MARGIN - CORNER_DOT_BOX,
      width: CORNER_DOT_BOX,
      height: CORNER_DOT_BOX,
    })
    expect(l.order).toEqual(['shell', 'webui', 'dot'])
  })

  it('gives the shell its strips: the view starts below the top strip and ends above the bottom strip and the bar', () => {
    const l = computeLayout({ ...base, mode: 'webui', insetTop: 52, insetBottom: 40 })
    expect(l.webui).toEqual({ x: 0, y: 52, width: 1100, height: 720 - 52 - 40 - STATUS_BAR_HEIGHT })
    expect(l.order).toEqual(['shell', 'webui'])
    // Strips never push the view out of the window.
    const big = computeLayout({ ...base, mode: 'webui', insetTop: 600, insetBottom: 600 })
    expect(big.webui).toEqual({ x: 0, y: 600, width: 1100, height: 0 })
    // Strips do not matter while the shell fills the window.
    expect(computeLayout({ ...base, mode: 'shell-only', insetTop: 52 }).webui).toBeUndefined()
  })

  it('raises the shell above the WebUI for popovers, the dot stays on top', () => {
    expect(computeLayout({ ...base, mode: 'webui', shellAbove: true }).order).toEqual(['webui', 'shell'])
    expect(computeLayout({ ...base, mode: 'webui', shellAbove: true, barVisible: false }).order).toEqual(['webui', 'shell', 'dot'])
    // The WebUI keeps its bounds underneath, so nothing reflows when a menu opens.
    expect(computeLayout({ ...base, mode: 'webui', shellAbove: true }).webui).toEqual(computeLayout({ ...base, mode: 'webui' }).webui)
  })

  it('follows a resize', () => {
    for (const [w, h] of [
      [720, 480],
      [1920, 1080],
      [801.6, 600.4],
      [3840, 2160],
    ]) {
      const l = computeLayout({ ...base, width: w, height: h, mode: 'webui' })
      expect(l.shell).toEqual({ x: 0, y: 0, width: Math.floor(w), height: Math.floor(h) })
      expect(l.webui).toEqual({ x: 0, y: 0, width: Math.floor(w), height: Math.floor(h) - 30 })
      const d = computeLayout({ ...base, width: w, height: h, mode: 'webui', barVisible: false }).dot!
      expect(d.x + d.width + CORNER_DOT_MARGIN).toBe(Math.floor(w))
      expect(d.y + d.height + CORNER_DOT_MARGIN).toBe(Math.floor(h))
    }
  })

  it('never produces negative sizes for a tiny or empty window', () => {
    for (const [w, h] of [
      [0, 0],
      [10, 10],
      [-5, 20],
    ]) {
      for (const barVisible of [true, false]) {
        const l = computeLayout({ ...base, width: w, height: h, mode: 'webui', barVisible })
        for (const r of [l.shell, l.webui, l.dot].filter(Boolean)) {
          expect(r!.x).toBeGreaterThanOrEqual(0)
          expect(r!.y).toBeGreaterThanOrEqual(0)
          expect(r!.width).toBeGreaterThanOrEqual(0)
          expect(r!.height).toBeGreaterThanOrEqual(0)
        }
      }
    }
  })

  it('clamps to the minimum window size', () => {
    expect(clampSize(100, 100)).toEqual({ width: MIN_WINDOW_WIDTH, height: MIN_WINDOW_HEIGHT })
    expect(clampSize(1000.7, 900.2)).toEqual({ width: 1000, height: 900 })
    // At the minimum size the WebUI still gets a usable area.
    const l = computeLayout({ ...base, ...clampSize(1, 1), mode: 'webui' })
    expect(l.webui!.height).toBe(MIN_WINDOW_HEIGHT - STATUS_BAR_HEIGHT)
  })

  it('reports a change of stacking order only when it changes', () => {
    const a = computeLayout({ ...base, mode: 'webui' })
    expect(orderChanged(undefined, a)).toBe(true)
    expect(orderChanged(a, computeLayout({ ...base, width: 900, mode: 'webui' }))).toBe(false)
    expect(orderChanged(a, computeLayout({ ...base, mode: 'webui', shellAbove: true }))).toBe(true)
  })
})

describe('dim layer over a stopped server', () => {
  it('covers exactly the WebUI bounds, right above the WebUI view, so the banner strip and the bar stay clickable', () => {
    const l = computeLayout({ ...base, mode: 'webui', insetBottom: 40, dim: true })
    expect(l.dim).toEqual(l.webui)
    expect(l.dim).toEqual({ x: 0, y: 0, width: 1100, height: 720 - 40 - STATUS_BAR_HEIGHT })
    expect(l.order).toEqual(['shell', 'webui', 'dim'])
  })

  it('stays under a raised shell and under the dot', () => {
    expect(computeLayout({ ...base, mode: 'webui', dim: true, shellAbove: true }).order).toEqual(['webui', 'dim', 'shell'])
    expect(computeLayout({ ...base, mode: 'webui', dim: true, barVisible: false }).order).toEqual(['shell', 'webui', 'dim', 'dot'])
  })

  it('is never shown without the WebUI, and does not move the WebUI', () => {
    const l = computeLayout({ ...base, mode: 'shell-only', dim: true })
    expect(l.dim).toBeUndefined()
    expect(l.order).toEqual(['shell'])
    expect(computeLayout({ ...base, mode: 'webui', dim: true }).webui).toEqual(computeLayout({ ...base, mode: 'webui' }).webui)
    expect(computeLayout({ ...base, mode: 'webui' }).dim).toBeUndefined()
  })

  it('changes the stacking order when it comes and goes', () => {
    const a = computeLayout({ ...base, mode: 'webui' })
    expect(orderChanged(a, computeLayout({ ...base, mode: 'webui', dim: true }))).toBe(true)
  })

  it('is wanted only while the stopped banner shows over the WebUI', () => {
    expect(dimWanted({ layout: 'webui', banner: 'stopped' })).toBe(true)
    expect(dimWanted({ layout: 'webui', banner: 'starting' })).toBe(false)
    expect(dimWanted({ layout: 'webui', banner: null })).toBe(false)
    expect(dimWanted({ layout: 'shell-only', banner: 'stopped' })).toBe(false)
  })

  it('is a script-free, see-through data page with a locked CSP', () => {
    expect(DIM_PAGE.startsWith('data:text/html;charset=utf-8,')).toBe(true)
    const html = decodeURIComponent(DIM_PAGE.slice(DIM_PAGE.indexOf(',') + 1))
    expect(html).not.toMatch(/<script|<a |href=/i)
    expect(html).toContain("default-src 'none'")
    expect(html).toMatch(/background:rgba\(/)
    expect(html).toContain('prefers-color-scheme: dark')
  })
})

describe('cornerDotPage', () => {
  it('is a script-free data page with a locked CSP and the click link', () => {
    for (const tone of ['ok', 'busy', 'failed', 'stopped'] as const) {
      const url = cornerDotPage(tone)
      expect(url.startsWith('data:text/html;charset=utf-8,')).toBe(true)
      const html = decodeURIComponent(url.slice(url.indexOf(',') + 1))
      expect(html).not.toMatch(/<script/i)
      expect(html).toContain("default-src 'none'")
      expect(html).toContain(`href="${DOT_CLICK_URL}"`)
      expect(html).toContain('Show status bar')
    }
    expect(cornerDotPage('ok')).not.toBe(cornerDotPage('failed'))
    expect(new URL(DOT_CLICK_URL).hostname.endsWith('.invalid')).toBe(true)
  })
})
