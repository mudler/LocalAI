import { test, expect } from './coverage-fixtures.js'

// The bundle the binary embeds is code-split and its CSS is emitted per chunk,
// in an order the V8 coverage bundle (one inlined chunk) does not have. A rule
// that wins in one order and loses in the other passes the coverage run and
// breaks in the shipped app. This walk loads the main pages on the production
// bundle, against the real test server with nothing stubbed, and fails on
// anything the browser reports: a page error, a console error, a request that
// the server refused or a stylesheet that did not apply.
//
// Tagged @production-chunks so `make test-ui-stale-chunk` (production build)
// runs it, and the coverage run (inlined build) skips it.

const PAGES = [
  { name: 'Home', path: '/app', ready: page => page.locator('.home-page') },
  { name: 'Chats', path: '/app/chats', ready: page => page.getByTestId('chats-page') },
  { name: 'Build', path: '/app/build', ready: page => page.getByTestId('build-landing') },
  { name: 'Operate', path: '/app/operate', ready: page => page.getByTestId('operate-overview') },
  { name: 'Models', path: '/app/models', ready: page => page.locator('.sidebar-nav a.nav-item[href="/app/models"][aria-current="page"], .sidebar-nav a.nav-item.active[href="/app/models"]').first() },
]

function watch(page) {
  const problems = []
  page.on('pageerror', err => problems.push(`page error: ${err.message}`))
  // The browser reports a failed request as a console error too; the response
  // handler below names the request, so that duplicate is dropped.
  page.on('console', msg => {
    if (msg.type() === 'error' && !msg.text().startsWith('Failed to load resource')) problems.push(`console error: ${msg.text()}`)
  })
  page.on('response', res => {
    const status = res.status()
    const url = new URL(res.url())
    // Static assets must not fail. The test server runs without add-ons, and
    // their routes answer 503 "unavailable" by design. Any other server error
    // is a bug. A 404 on an API list with nothing to show yet is the API's
    // own answer and does not count.
    const addonRoute = /^\/api\/(addons|fine-tuning|quantization)(\/|$)/.test(url.pathname)
    if (url.pathname.startsWith('/assets/') && status >= 400) problems.push(`asset ${url.pathname}: ${status}`)
    else if (status >= 500 && !(addonRoute && status === 503)) problems.push(`${url.pathname}: ${status}`)
  })
  return problems
}

test.describe('@production-chunks Production bundle', () => {
  for (const { name, path, ready } of PAGES) {
    test(`${name} loads on the shipped bundle`, async ({ page }) => {
      const problems = watch(page)
      await page.goto(path)
      await expect(page.locator('.sidebar')).toBeVisible()
      await expect(ready(page)).toBeVisible()
      await expect(page.getByText('Unexpected Application Error')).toHaveCount(0)

      // Every stylesheet the page links has been parsed.
      const unapplied = await page.evaluate(() =>
        [...document.querySelectorAll('link[rel="stylesheet"]')].filter(l => !l.sheet).map(l => l.href))
      expect(unapplied).toEqual([])
      // And the design system reached the sidebar: an unstyled app has a
      // static, transparent one.
      const sidebar = await page.locator('.sidebar').evaluate(el => {
        const s = getComputedStyle(el)
        return { position: s.position, background: s.backgroundColor }
      })
      expect(sidebar.background).not.toBe('rgba(0, 0, 0, 0)')

      expect(problems).toEqual([])
    })
  }

  test('the main pages are code-split chunks, not one inlined bundle', async ({ page }) => {
    const scripts = new Set()
    page.on('response', res => {
      const { pathname } = new URL(res.url())
      if (pathname.startsWith('/assets/') && pathname.endsWith('.js')) scripts.add(pathname)
    })
    await page.goto('/app')
    await expect(page.locator('.home-page')).toBeVisible()
    await page.goto('/app/build')
    await expect(page.getByTestId('build-landing')).toBeVisible()
    // The coverage bundle has a single script; the shipped one has a chunk per page.
    expect(scripts.size).toBeGreaterThan(2)
  })
})
