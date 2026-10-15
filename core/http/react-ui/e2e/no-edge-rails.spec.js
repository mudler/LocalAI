import { test, expect } from './coverage-fixtures.js'

// No coloured strip on the left edge of a tile, card, row or message. A rail
// can be drawn four ways, and each is checked from computed style:
//   1. a left border thicker than 1px,
//   2. an inset box-shadow offset sideways with no blur (a "shadow" bar),
//   3. a ::before or ::after that is a narrow absolute strip on the left edge,
//   4. a narrow, tall child pinned to the left edge of its parent.
// Selection and status use a surface step, a check or a dot instead.
const ROUTES = [
  '/app', '/app/chat', '/app/models', '/app/models?view=installed', '/app/studio',
  '/app/talk', '/app/agents', '/app/skills', '/app/collections', '/app/agent-jobs',
  '/app/backends', '/app/activity', '/app/operate', '/app/build', '/app/settings',
  '/app/traces', '/app/usage', '/app/nodes', '/app/p2p', '/app/voice-library',
  '/app/account',
]

test('no element on the main routes carries a left accent rail', async ({ page }) => {
  test.setTimeout(ROUTES.length * 8_000)
  const findings = []
  for (const theme of ['light', 'dark']) {
    await page.addInitScript((t) => localStorage.setItem('localai-theme', t), theme)
    for (const route of ROUTES) {
      await page.goto(route)
      await page.waitForTimeout(400)
      const found = await page.evaluate(() => {
        const out = []
        const name = (el) => `${el.tagName.toLowerCase()}.${String(el.className).split(' ').slice(0, 2).join('.')}`
        const strip = (cs, parent, el, which) => {
          if (cs.content === 'none' || cs.display === 'none' || cs.position !== 'absolute') return false
          const w = parseFloat(cs.width)
          const h = parseFloat(cs.height)
          if (!(w >= 2 && w <= 4)) return false
          const p = parent.getBoundingClientRect()
          if (!(h >= p.height * 0.6 || cs.height === 'auto')) return false
          return cs.left === '0px' || cs.insetInlineStart === '0px'
        }
        for (const el of document.querySelectorAll('body *')) {
          const r = el.getBoundingClientRect()
          if (r.width === 0 || r.height === 0) continue
          const cs = getComputedStyle(el)
          if (el.closest('.cm-editor')) continue
          if (cs.borderLeftStyle !== 'none' && cs.borderLeftStyle !== 'dashed' && parseFloat(cs.borderLeftWidth) > 1 &&
              cs.borderTopWidth !== cs.borderLeftWidth) { // a ring (spinner) has all four sides
            out.push(`border-left ${cs.borderLeftWidth} on ${name(el)}`)
          }
          for (const m of cs.boxShadow.matchAll(/(-?\d+(?:\.\d+)?)px (-?\d+(?:\.\d+)?)px (\d+(?:\.\d+)?)px (-?\d+(?:\.\d+)?)px inset|inset (-?\d+(?:\.\d+)?)px (-?\d+(?:\.\d+)?)px (\d+(?:\.\d+)?)px (-?\d+(?:\.\d+)?)px/g)) {
            const nums = m.slice(1).filter((v) => v !== undefined).map(Number)
            const [x, y, blur] = nums
            if (Math.abs(x) >= 2 && y === 0 && blur === 0) out.push(`inset side shadow ${m[0]} on ${name(el)}`)
          }
          for (const which of ['::before', '::after']) {
            if (strip(getComputedStyle(el, which), el, el, which)) out.push(`${which} strip on ${name(el)}`)
          }
          // A narrow tall child pinned to the left edge of a card-like parent.
          if (el.parentElement && cs.position === 'absolute' && el.children.length === 0 && !el.textContent.trim()) {
            const p = el.parentElement.getBoundingClientRect()
            if (r.width >= 2 && r.width <= 4 && r.height >= p.height * 0.6 && Math.abs(r.left - p.left) < 2) {
              out.push(`left strip child ${name(el)}`)
            }
          }
        }
        return out
      })
      for (const f of found) findings.push(`${theme} ${route}: ${f}`)
    }
  }
  expect([...new Set(findings)]).toEqual([])
})
