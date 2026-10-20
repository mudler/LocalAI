import { test, expect } from '@playwright/test'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { MEMBER, app, sessionFor, mockAddons } from './addons-fixtures.js'

// The add-on page shell against the stand-in routes of addons-fixtures.js: the
// fixture package's own module and style sheet are served as an owner would
// serve them. This runs in the default suite (no add-on owner needed); the
// real-authorization specs are in addons-runtime.spec.js (esm-ui suite).

const fixture = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../../../tests/addons/fixture/ui/uifixture')
const DIGEST = 'c'.repeat(64)
const base = `/api/addons/uifixture/assets/${DIGEST}`

function uiApp(over = {}) {
  return app({
    id: 'uifixture', name: 'uifixture', title: 'UI fixture', icon: undefined, generation: 1, mainPage: '/app/addons/uifixture/home',
    ui: { entry: `${base}/ui.js`, css: [`${base}/ui/style.css`], sdk: { min: 1, max: 2 }, pages: ['home', 'boom', 'dialog'], navigation: ['home'], chatActions: ['summarize'] },
    ...over,
  })
}

async function serve(page) {
  await page.route(/\/api\/addons\/uifixture\/assets\//, route => {
    const file = new URL(route.request().url()).pathname.split('/').slice(6).join('/')
    const known = { 'ui.js': 'text/javascript', 'ui/style.css': 'text/css' }
    if (!known[file]) return route.fulfill({ status: 403, json: { error: { type: 'addon_error', message: 'Addon request denied' } } })
    return route.fulfill({
      status: 200,
      contentType: known[file],
      headers: { 'cache-control': 'private, max-age=31536000, immutable', 'x-content-type-options': 'nosniff' },
      body: fs.readFileSync(path.join(fixture, file)),
    })
  })
  await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(MEMBER, { addons: true }) }))
}

test.describe('Add-on shell', () => {
  test('a module served by the owner draws its page with the host React and scoped styles', async ({ page }) => {
    await serve(page)
    await mockAddons(page, { apps: [uiApp()] })
    await page.goto('/app/addons/uifixture/home')
    const surface = page.locator('[data-testid="addon-surface"][data-addon="uifixture"]')
    await expect(surface).toBeVisible()
    await expect(page.getByTestId('fx-title')).toHaveText('Hello fixture')
    await page.getByRole('button', { name: 'Increment' }).click()
    await expect(surface.getByText('hooks 1')).toBeVisible()
    // The sheet's `h1` rule never reaches the host's own page title.
    await expect(page.locator('h1.page-title')).not.toHaveCSS('font-size', '11px')
    await expect(page.locator('style[data-addon-style="uifixture"]')).toHaveCount(1)
    // The detail view of the add-on links the declared navigation page.
    await page.goto('/app/addons?detail=uifixture')
    await expect(page.getByTestId('addon-detail').getByRole('link', { name: 'UI fixture', exact: true })).toHaveAttribute('href', '/app/addons/uifixture/home')
  })

  test('a page that throws is contained and an undeclared page is a missing page', async ({ page }) => {
    await serve(page)
    await mockAddons(page, { apps: [uiApp()] })
    await page.goto('/app/addons/uifixture/boom')
    await expect(page.getByTestId('addon-error')).toBeVisible()
    await expect(page.locator('.sidebar-nav')).toBeVisible()
    await page.goto('/app/addons/uifixture/other')
    await expect(page.getByTestId('addon-missing-page')).toBeVisible()
  })

  test('an add-on that is not usable says why and an unknown SDK range is never imported', async ({ page }) => {
    await serve(page)
    const requested = []
    page.on('request', r => { if (/\/assets\//.test(r.url()) && r.url().includes('/api/addons/')) requested.push(r.url()) })
    await mockAddons(page, { apps: [uiApp({ state: 'failed' }), uiApp({ id: 'future', name: 'future', title: 'Future', ui: { ...uiApp().ui, entry: '/api/addons/future/assets/' + DIGEST + '/ui.js', css: [], sdk: { min: 3, max: 3 } } })] })
    await page.goto('/app/addons/uifixture/home')
    await expect(page.getByTestId('addon-unavailable')).toHaveAttribute('data-reason', 'failed')
    await page.goto('/app/addons/future/home')
    await expect(page.getByTestId('addon-unavailable')).toHaveAttribute('data-reason', 'incompatible_sdk')
    expect(requested).toEqual([])
  })

  test('a withdrawn add-on is unmounted on the next list and Reload brings it back', async ({ page }) => {
    await serve(page)
    const state = await mockAddons(page, { apps: [uiApp()] })
    await page.goto('/app/addons/uifixture/home')
    await expect(page.getByTestId('fx-title')).toBeVisible()
    state.apps[0].enabled = false
    state.apps[0].state = 'stopped'
    await expect(page.getByTestId('addon-unavailable')).toHaveAttribute('data-reason', 'disabled', { timeout: 15_000 })
    await expect(page.locator('style[data-addon-style="uifixture"]')).toHaveCount(0)
    state.apps[0].enabled = true
    state.apps[0].state = 'running'
    await page.getByTestId('addon-reload').click()
    await expect(page.getByTestId('fx-title')).toBeVisible({ timeout: 15_000 })
  })
})
