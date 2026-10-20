import { test, expect } from '@playwright/test'
import { MEMBER, sessionFor, mockAddons } from './addons-fixtures.js'
import { MIGRATION_ROUTES } from '../src/utils/legacyRoutes.js'

// Every moved generator route, with and without a model: 14 old paths. For
// each, in both flag states, the final location is the Studio tab and that
// generator is drawn (its tab is active), not the Studio overview fallback.
const MOVED = MIGRATION_ROUTES.filter(r => r.kind === 'studio-tab')
const CASES = MOVED.flatMap(r => [
  { from: r.from, to: r.to, tab: r.to.split('/').pop() },
  { from: `${r.from}/my-model`, to: `${r.to}/my-model`, tab: r.to.split('/').pop() },
])

const STUDIO = { id: 'studio', kind: 'page', titleKey: 'items.studio', icon: 'palette', mainPath: '/app/studio' }

async function setup(page, flag) {
  await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(MEMBER, { addons: true, '3d': true, audio_diarization: true, audio_transform: true }) }))
  await page.route('**/api/features', route => route.fulfill({ json: { addon_navigation: flag } }))
  await page.route('**/api/addons/builtin', route => route.fulfill({ json: { builtin: [STUDIO] } }))
  await mockAddons(page, { apps: [] })
  await page.route('**/api/addons/pins', route => route.fulfill({ json: [] }))
}

test('the table covers all 14 old paths', () => {
  expect(CASES).toHaveLength(14)
})

for (const flag of [true, false]) {
  test.describe(`old generator links, addon_navigation=${flag}`, () => {
    for (const c of CASES) {
      test(`${c.from} opens ${c.to}`, async ({ page }) => {
        await setup(page, flag)
        await page.goto(c.from)
        await expect(page).toHaveURL(new RegExp(`${c.to.replace(/\//g, '\\/')}$`))
        await expect(page.locator(`.studio-tab-active[data-tab="${c.tab}"]`)).toBeVisible()
        await expect(page.getByTestId('studio-overview')).toHaveCount(0)
      })
    }
  })
}
