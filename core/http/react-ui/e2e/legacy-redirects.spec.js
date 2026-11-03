import { test, expect } from '@playwright/test'
import { MEMBER, sessionFor } from './addons-fixtures.js'
import { installStudioAddons } from './studio-addon.js'
import { MIGRATION_ROUTES, chatsRedirect } from '../src/utils/legacyRoutes.js'

// Native migrations remain supported; removed addon aliases must not select
// a privileged provider, even when the corresponding addon is installed.
test('the migration table preserves native destinations only', () => {
  expect(MIGRATION_ROUTES.map(r => r.from)).toEqual([
    '/app/chat', '/app/chats/agents', '/app/chats/triggers', '/app/chats/skills',
    '/app/agent-jobs', '/app/agents', '/app/skills', '/app/models', '/app/settings',
  ])
  for (const r of MIGRATION_ROUTES) {
    expect(r.to).not.toContain('/addons/')
    if (r.kind === 'chats') expect(chatsRedirect(r.from)).toBe(r.to)
    else expect(r.to).toBe(r.from)
  }
})

for (const from of ['image', 'video', 'threed', 'tts', 'sound', 'audio-transform', 'diarization']) {
  for (const suffix of ['', '/my-model']) {
    test(`removed generator /app/${from}${suffix} is NotFound, not an addon redirect`, async ({ page }) => {
      await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(MEMBER, { addons: true }) }))
      await installStudioAddons(page, { ids: ['studio'] })
      await page.goto(`/app/${from}${suffix}`)
      await expect(page.getByRole('heading', { name: 'This page does not exist' })).toBeVisible()
      await expect(page).toHaveURL(new RegExp(`/app/${from}${suffix}$`))
      await expect(page.getByTestId('studio-overview')).toHaveCount(0)
    })
  }
}
