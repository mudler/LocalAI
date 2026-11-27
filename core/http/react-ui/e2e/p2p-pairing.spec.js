import { test, expect } from './coverage-fixtures.js'
import { MEMBER, mockAccess } from './access-fixtures.js'
import { mockMyPairing, myDeviceRows, myInviteRows } from './p2p-pairing-fixtures.js'

// A user pairs their own phone (feature p2p_pair): Account, My devices, the
// Pair a phone dialog and its refusals. The admin side is in
// p2p-pairing-admin.spec.js.

const MEMBER_PAIR = { user: { ...MEMBER, provider: 'local', permissions: { chat: true, p2p_pair: true } } }

async function openMyDevices(page, { status = 'member', ...options } = {}) {
  await mockAccess(page, { status, statusExtra: status === 'member' ? MEMBER_PAIR : {} })
  const state = await mockMyPairing(page, options)
  await page.goto('/app/account?tab=devices')
  await expect(page.getByTestId('my-devices')).toBeVisible({ timeout: 15_000 })
  return state
}

test.describe('My devices', () => {
  test('is not a tab while the server does not share over P2P', async ({ page }) => {
    await mockAccess(page, { status: 'member', statusExtra: MEMBER_PAIR })
    await mockMyPairing(page, { me: { available: false, reason: 'not_share', mode: 'off' } })
    await page.goto('/app/account')
    await expect(page.getByTestId('account-profile')).toBeVisible()
    await expect(page.getByRole('tab', { name: 'My devices' })).toHaveCount(0)
  })

  test('is not a tab without the feature p2p_pair', async ({ page }) => {
    await mockAccess(page, { status: 'member' })
    const state = await mockMyPairing(page)
    await page.goto('/app/account')
    await expect(page.getByTestId('account-profile')).toBeVisible()
    await expect(page.getByRole('tab', { name: 'My devices' })).toHaveCount(0)
    expect(state.calls).toEqual([])
  })

  test('starts empty and pairs a phone that then signs in', async ({ page }) => {
    const state = await openMyDevices(page, { redeemAfter: 2 })
    await expect(page.getByTestId('my-devices')).toContainText('Pairing signs this phone in as you. Revoke it here if you lose it.')
    await expect(page.getByTestId('my-devices-empty')).toBeVisible()
    await expect(page.getByTestId('my-admin-warning')).toHaveCount(0)

    await page.getByTestId('my-pair-open').click()
    const dialog = page.getByTestId('pair-dialog')
    await expect(dialog).toBeVisible()
    // The life stops at the cap of the administrator (1 hour).
    await expect(dialog.getByTestId('pair-ttl-600')).toBeVisible()
    await expect(dialog.getByTestId('pair-ttl-3600')).toBeVisible()
    await expect(dialog.getByTestId('pair-ttl-86400')).toHaveCount(0)
    await dialog.getByTestId('pair-label').fill('Pixel 9')
    await dialog.getByTestId('pair-ttl-3600').click()
    await dialog.getByTestId('pair-mint').click()

    const result = page.getByTestId('pair-result')
    await expect(result).toBeVisible()
    await expect(result.getByTestId('qr-code')).toBeVisible()
    await expect(result.getByTestId('share-link')).toContainText('localai://pair?v=1&k=device')
    // A user cannot add a relay: the warning says whom to ask, with no link to the settings.
    const punch = result.getByTestId('share-warn-punch_only')
    await expect(punch).toContainText('Ask your administrator to add a relay.')
    await expect(punch.getByRole('link')).toHaveCount(0)
    expect(state.calls.find(c => c.method === 'POST')).toMatchObject({ path: '/invites', body: { label: 'Pixel 9', ttl_seconds: 3600 } })

    await expect(page.getByTestId('pair-done')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('pair-done')).toContainText('Pixel 9 is paired')
    await page.getByTestId('pair-ok').click()
    await expect(page.getByTestId('my-device-md-new')).toContainText('Pixel 9')
  })

  test('an admin is told that the phone gets an admin session', async ({ page }) => {
    await openMyDevices(page, { status: 'admin' })
    await expect(page.getByTestId('my-admin-warning')).toContainText('This phone gets an admin session.')
    await page.getByTestId('my-pair-open').click()
    await expect(page.getByTestId('pair-dialog').getByTestId('pair-admin-warning')).toBeVisible()
  })

  test('lists the phones and the waiting invites, renames, revokes and cancels', async ({ page }) => {
    const state = await openMyDevices(page, { devices: myDeviceRows(), invites: myInviteRows() })
    await expect(page.getByTestId('my-devices-count')).toContainText('2 of 5 devices')
    await expect(page.getByTestId('my-device-md-1')).toContainText('Hole-punched')
    await expect(page.getByTestId('my-device-md-2')).toContainText('Not connected')
    await expect(page.getByTestId('my-device-md-3')).toContainText('Revoked')
    await expect(page.getByTestId('my-device-revoke-md-3')).toHaveCount(0)
    await expect(page.getByTestId('my-invite-mi-1')).toContainText('Kitchen tablet')

    await page.getByTestId('my-device-rename-md-2').click()
    await page.getByTestId('my-rename-input').fill('Home tablet')
    await page.getByTestId('my-rename-save').click()
    await expect(page.getByTestId('my-device-md-2')).toContainText('Home tablet')
    expect(state.calls.find(c => c.method === 'PATCH')).toMatchObject({ path: '/devices/md-2', body: { label: 'Home tablet' } })

    await page.getByTestId('my-device-revoke-md-1').click()
    await expect(page.getByTestId('my-revoke-dialog')).toContainText('Revoke Pixel 9?')
    await page.getByTestId('my-revoke-confirm').click()
    await expect(page.getByTestId('my-device-revoke-md-1')).toHaveCount(0)

    await page.getByTestId('my-invite-cancel-mi-1').click()
    await expect(page.getByTestId('my-invites')).toHaveCount(0)
    expect(state.calls.filter(c => c.method === 'DELETE').map(c => c.path)).toEqual(['/devices/md-1', '/invites/mi-1'])
  })

  test('at the device limit the button is off and says why', async ({ page }) => {
    await openMyDevices(page, { devices: myDeviceRows(), me: { devices: 5 } })
    await expect(page.getByTestId('my-pair-open')).toBeDisabled()
    await expect(page.getByTestId('my-pair-blocked')).toContainText('You have 5 devices, the most allowed.')
  })

  test('says why the server refused the invite', async ({ page }) => {
    await openMyDevices(page, { fail: { 'POST /invites': { status: 409, type: 'needs_account', message: 'needs an account' } } })
    await page.getByTestId('my-pair-open').click()
    await page.getByTestId('pair-mint').click()
    await expect(page.getByTestId('pair-fail-needsAccount')).toContainText('This needs a user account.')
  })

  test('a limit the counts do not show is the hourly limit', async ({ page }) => {
    await openMyDevices(page, { fail: { 'POST /invites': { status: 429, type: 'limit_reached', message: 'limit reached' } } })
    await page.getByTestId('my-pair-open').click()
    await page.getByTestId('pair-mint').click()
    await expect(page.getByTestId('pair-fail-limit')).toContainText('You can make 10 invites an hour')
  })
})
