import { test, expect } from './coverage-fixtures.js'
import { mockAccess } from './access-fixtures.js'
import { mockAdmin, mockP2PShare } from './p2p-share-fixtures.js'
import { mockP2PSettings } from './p2p-settings-fixtures.js'
import { featuresWithPair, mockUserPairing, ownedDeviceRows } from './p2p-pairing-fixtures.js'

// The admin side of users pairing their own phones: the default of the
// feature p2p_pair on Users, the User pairing section of the P2P settings,
// and the owner column of the device table.

test.describe('User pairing, admin side', () => {
  test('Users: the default of p2p_pair is saved at once', async ({ page }) => {
    await mockAccess(page)
    const puts = []
    await page.route('**/api/auth/admin/features', route => route.fulfill({ json: featuresWithPair(false) }))
    await page.route('**/api/auth/admin/features/*/default', route => {
      const body = route.request().postDataJSON()
      puts.push({ url: new URL(route.request().url()).pathname, body })
      return route.fulfill({ json: { key: 'p2p_pair', label: 'Pair own devices (P2P)', default: body.enabled, configurable: true } })
    })
    await page.goto('/app/users')
    await expect(page.getByTestId('users-table')).toBeVisible()
    await page.getByRole('button', { name: 'Edit access for bob' }).click()
    const sheet = page.getByTestId('access-sheet')
    await expect(sheet.getByRole('switch', { name: 'Pair own devices (P2P)' })).toBeVisible()
    // Only the configurable feature has a default switch.
    await expect(sheet.getByTestId('feature-default-fine_tuning')).toHaveCount(0)
    const def = sheet.getByRole('switch', { name: 'On by default for users' })
    await expect(def).toHaveAttribute('aria-checked', 'false')
    await def.click()
    await expect(def).toHaveAttribute('aria-checked', 'true')
    expect(puts).toEqual([{ url: '/api/auth/admin/features/p2p_pair/default', body: { enabled: true } }])
  })

  test('Users: a user who follows the default moves with it, and Use default drops an explicit value', async ({ page }) => {
    const state = await mockAccess(page)
    await page.route('**/api/auth/admin/features', route => route.fulfill({ json: featuresWithPair(false) }))
    await page.route('**/api/auth/admin/features/*/default', route => {
      const body = route.request().postDataJSON()
      return route.fulfill({ json: { key: 'p2p_pair', label: 'Pair own devices (P2P)', default: body.enabled, configurable: true } })
    })
    await page.goto('/app/users')
    await page.getByRole('button', { name: 'Edit access for bob' }).click()
    const sheet = page.getByTestId('access-sheet')
    const pair = sheet.getByRole('switch', { name: 'Pair own devices (P2P)' })
    const source = sheet.getByTestId('feature-source-p2p_pair')
    const useDefault = sheet.getByRole('button', { name: 'Use the default for Pair own devices (P2P)' })
    await expect(pair).toHaveAttribute('aria-checked', 'false')
    await expect(source).toContainText('follows the default')
    await expect(useDefault).toHaveCount(0)

    // The default changes in the open sheet: bob follows it.
    await sheet.getByRole('switch', { name: 'On by default for users' }).click()
    await expect(pair).toHaveAttribute('aria-checked', 'true')

    // An explicit value for bob, then back to the default.
    await pair.click()
    await expect(pair).toHaveAttribute('aria-checked', 'false')
    await expect(source).toContainText('Set for this user')
    await useDefault.click()
    await expect(pair).toHaveAttribute('aria-checked', 'true')
    await expect(useDefault).toHaveCount(0)

    await sheet.getByRole('button', { name: 'Save access' }).click()
    await expect(sheet).toHaveCount(0)
    const put = state.writes.find(w => w.method === 'PUT' && w.path.endsWith('/permissions'))
    expect(put.body).toEqual({ agents: true, skills: true })
  })

  test('P2P settings: the User pairing section edits p2p_user_pairing', async ({ page }) => {
    await mockP2PSettings(page)
    const state = await mockUserPairing(page)
    await page.goto('/app/p2p/settings?section=users')
    const section = page.getByTestId('p2p-user-pairing')
    await expect(section).toBeVisible({ timeout: 15_000 })
    await section.getByTestId('p2p-users-relays').selectOption('none')
    await expect.poll(() => state.puts.length).toBe(1)
    expect(state.puts[0]).toEqual({ relays: 'none' })
    // Open relays mean nothing without relays.
    await expect(section.getByTestId('p2ps-users-open-switch')).toBeDisabled()
    await section.getByTestId('p2ps-users-lan-switch').click()
    await expect.poll(() => state.puts.length).toBe(2)
    expect(state.puts[1]).toEqual({ omit_lan_addrs: true })

    const save = section.getByTestId('p2p-users-limits-save')
    await expect(save).toBeDisabled()
    await section.getByTestId('p2p-users-max-devices').fill('0')
    await expect(save).toBeDisabled()
    await section.getByTestId('p2p-users-max-devices').fill('3')
    await section.getByTestId('p2p-users-max-ttl').fill('30')
    await save.click()
    await expect.poll(() => state.puts.length).toBe(3)
    expect(state.puts[2]).toEqual({ max_devices: 3, max_ttl_seconds: 1800 })
  })

  test('Devices: the owner, the sign-in badge and the owner filter', async ({ page }) => {
    await mockAdmin(page)
    await mockP2PShare(page, { invites: [], devices: ownedDeviceRows() })
    await page.goto('/app/p2p')
    const devices = page.getByTestId('p2p-devices')
    await expect(devices).toBeVisible({ timeout: 15_000 })
    await expect(devices.getByTestId('p2p-device-owner-od-1')).toHaveText('alice')
    await expect(devices.getByTestId('p2p-device-owner-od-3')).toHaveText('No account')
    await expect(devices.getByTestId('p2p-device-signin-od-1')).toHaveText('Signs in')
    await expect(devices.getByTestId('p2p-device-signin-od-2')).toHaveCount(0)
    await devices.getByTestId('p2p-owner-filter').selectOption({ label: 'bob' })
    await expect(page).toHaveURL(/owner=u-bob/)
    await expect(devices.getByTestId('p2p-device-od-2')).toBeVisible()
    await expect(devices.getByTestId('p2p-device-od-4')).toBeVisible()
    await expect(devices.getByTestId('p2p-device-od-1')).toHaveCount(0)
    await devices.getByTestId('p2p-owner-filter').selectOption({ label: 'All owners' })
    await expect(devices.getByTestId('p2p-device-od-1')).toBeVisible()
  })
})
