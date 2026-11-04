import { test, expect } from './coverage-fixtures.js'
import { mockSettings } from './settings-fixtures.js'
import { mockAddons } from './addons-fixtures.js'

// The host ceiling of connector files is an owner policy key. The Settings page
// writes it through host-settings-update, like the add-on upgrade setting, and
// states the numbers in effect that the connector list reports.

const MIB = 1024 * 1024
const SHOTS = process.env.UI_SHOTS_DIR

async function open(page, ceiling = 0) {
  await mockSettings(page, { addon_host_settings_authority: 'owner_required' })
  const state = await mockAddons(page, { hostSettings: { connector_files_ceiling_bytes: ceiling } })
  await page.route(/\/api\/chats\/connectors\/kinds$/, route => route.fulfill({ json: { kinds: [{
    addon: 'telegram', kind: 'telegram', title: 'Telegram',
    files: { host_ceiling_bytes: state.hostSettings.policy.connector_files_ceiling_bytes || 100 * MIB, receive: {}, send: {} },
  }] } }))
  await page.goto('/app/settings')
  await page.getByRole('button', { name: /^Backends and galleries/ }).click()
  return state
}

test.describe('Settings: the host ceiling of connector files', () => {
  test('says what is in effect', async ({ page }) => {
    await open(page)
    const box = page.getByTestId('owner-connector-files')
    await expect(box).toContainText('Now: 100 MiB.')
    await expect(box.getByRole('button', { name: 'Apply file ceiling' })).toBeDisabled()
    if (SHOTS) await box.scrollIntoViewIfNeeded().then(() => page.screenshot({ path: `${SHOTS}/settings-ceiling-${process.env.UI_SHOT_NAME || 'light'}.png` }))
  })

  test('writes the ceiling in bytes as its own owner key, and refuses a size that is too small', async ({ page }) => {
    const state = await open(page)
    const input = page.getByTestId('connector-files-ceiling')
    await input.fill('0.0001')
    await expect(page.getByTestId('owner-connector-files')).toContainText('Use a size from 1 KiB to 1 TiB')
    await expect(page.getByRole('button', { name: 'Apply file ceiling' })).toBeDisabled()
    await input.fill('5')
    await page.getByRole('button', { name: 'Apply file ceiling' }).click()
    await expect.poll(() => state.log.hostSettings).toEqual([{ connector_files_ceiling_bytes: 5 * MIB }])
    await expect(input).toHaveValue('5')
  })
})
