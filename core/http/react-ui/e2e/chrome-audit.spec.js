import { test, expect } from './coverage-fixtures.js'

// A standing guard against the two defects an earlier automated edit left
// scattered through the pages: icons stripped of their fa-* class (which render
// nothing at all), and controls left with the user agent's own chrome, which is
// a pale grey button on a dark ground.
const ROUTES = [
  '/app', '/app/chat', '/app/models', '/app/studio', '/app/talk',
  '/app/agents', '/app/skills', '/app/collections', '/app/agent-jobs',
  '/app/fine-tune', '/app/quantize', '/app/face', '/app/voice',
  '/app/manage', '/app/backends', '/app/activity', '/app/operate',
  '/app/settings', '/app/traces', '/app/usage', '/app/nodes', '/app/p2p',
  '/app/voice-library', '/app/voice-library/new', '/app/account',
]

test('no page renders a dead icon or a default-chrome control', async ({ page }) => {
  // One test walks every route, so its budget has to scale with the list rather
  // than sit on Playwright's per-test default of 30s. At 25 routes that default
  // allows ~1.2s per navigation, which holds on a developer machine and does
  // not on a loaded CI runner: the suite went red on the commit that added this
  // spec, timing out mid-loop at waitForTimeout rather than at any single goto,
  // which is what cumulative slowness looks like as opposed to one hung route.
  // Six seconds a route absorbs a slow runner and still fails promptly if a
  // route really does hang.
  test.setTimeout(ROUTES.length * 6_000)

  const findings = []
  for (const route of ROUTES) {
    await page.goto(route)
    await page.waitForTimeout(400)
    const found = await page.evaluate(() => {
      const out = []
      for (const el of document.querySelectorAll('button, a')) {
        if (el.getBoundingClientRect().width === 0) continue
        const cs = getComputedStyle(el)
        if (cs.borderTopStyle === 'outset' || cs.backgroundColor === 'rgb(239, 239, 239)') {
          out.push(`default-chrome: "${(el.textContent || '').trim().slice(0, 24)}" [${el.className}]`)
        }
      }
      for (const i of document.querySelectorAll('i')) {
        if (!/\bfa-/.test((i.className || '').toString())) {
          out.push(`dead-icon: [${i.className}]`)
        }
      }
      return [...new Set(out)]
    })
    for (const f of found) findings.push(`${route} — ${f}`)
  }
  expect(findings).toEqual([])
})
