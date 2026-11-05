import fs from 'node:fs'
import path from 'node:path'
import { test, expect } from './coverage-fixtures.js'
import { ADMIN, mockAddons, sessionFor, stubAddonRoutes } from './addons-fixtures.js'
import { buildAddonBundles, packagedApp } from './studio-addon.js'

// The page of the Memory add-on says that memory is built in and links to
// Settings > Memory. The add-on is served from its build output.

const DIST = path.resolve(path.dirname(new URL(import.meta.url).pathname), '../../../../addons/memory/ui/dist')

test.beforeAll(async () => {
  if (process.env.E2E_ADDON_BUNDLES_BUILT) return
  await buildAddonBundles(['memory'])
})

test('the add-on page has a notice that memory is built in, with a link to Settings > Memory', async ({ page }) => {
  await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(ADMIN, { addons: true }) }))
  await stubAddonRoutes(page)
  await page.route(/\/api\/addons\/memory\/assets\//, (route) => {
    const file = new URL(route.request().url()).pathname.split('/').slice(6).join('/')
    const type = { js: 'text/javascript', css: 'text/css', png: 'image/png' }[file.split('.').pop()]
    if (!type || !fs.existsSync(path.join(DIST, file))) return route.fulfill({ status: 403, json: { error: { type: 'addon_error', message: 'Addon request denied' } } })
    return route.fulfill({ status: 200, contentType: type, headers: { 'cache-control': 'no-store' }, body: fs.readFileSync(path.join(DIST, file)) })
  })
  await page.route('**/api/addons/memory/user-settings', route => route.fulfill({ json: { settings: [{ key: 'save-mode', values: ['ask', 'auto', 'never'], default: 'ask', value: 'ask' }] } }))
  await mockAddons(page, { apps: [packagedApp('memory')] })
  await page.goto('/app/addons/memory/memory')
  const notice = page.getByTestId('memory-builtin-notice')
  await expect(notice).toBeVisible()
  await expect(notice).toContainText('Memory is built in')
  await expect(notice).toContainText('Settings > Memory')
  await page.screenshot({ path: 'test-results/memory-addon-notice.png' })
  await notice.getByRole('button', { name: 'Open Settings > Memory' }).click()
  await expect(page).toHaveURL(/\/app\/account\?tab=memory$/)
})
