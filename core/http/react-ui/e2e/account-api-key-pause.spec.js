import { test, expect } from '@playwright/test'

// Account > API Keys: pause and resume a key without deleting it.

const soon = new Date(Date.now() + 3 * 3600 * 1000).toISOString()

function json(body) {
  return { contentType: 'application/json', body: JSON.stringify(body) }
}

test.describe('Account API keys pause', () => {
  let keys
  let patches

  test.beforeEach(async ({ page }) => {
    patches = []
    keys = [
      { id: 'k1', name: 'active-key', keyPrefix: 'lai-aaaaaaaa', role: 'user', createdAt: new Date().toISOString(), disabled: false },
      { id: 'k2', name: 'paused-key', keyPrefix: 'lai-bbbbbbbb', role: 'user', createdAt: new Date().toISOString(), disabled: true },
      { id: 'k3', name: 'timed-key', keyPrefix: 'lai-cccccccc', role: 'user', createdAt: new Date().toISOString(), disabled: false, pausedUntil: soon },
    ]

    await page.route('**/api/auth/status', (route) =>
      route.fulfill(json({
        authEnabled: true,
        providers: ['local'],
        hasUsers: true,
        user: { id: 'u1', email: 'u@example.com', name: 'U', role: 'user' },
      }))
    )
    await page.route('**/api/auth/api-keys', (route) => route.fulfill(json({ keys })))
    await page.route('**/api/auth/api-keys/*', async (route) => {
      const req = route.request()
      if (req.method() === 'PATCH') {
        const id = req.url().split('/').pop()
        const body = req.postDataJSON()
        patches.push({ id, body })
        const key = keys.find((k) => k.id === id)
        key.disabled = body.disabled
        key.pausedUntil = body.paused_until || undefined
        return route.fulfill(json({ message: 'API key updated' }))
      }
      return route.continue()
    })

    await page.goto('/app/account')
    await page.getByRole('tab', { name: 'API keys' }).click()
  })

  test('shows paused badges and resume buttons', async ({ page }) => {
    await expect(page.locator('.apikey-item')).toHaveCount(3)
    await expect(page.locator('.apikey-item').nth(0).locator('.apikey-paused-badge')).toHaveCount(0)
    await expect(page.locator('.apikey-item').nth(1).locator('.apikey-paused-badge')).toHaveText('Paused')
    await expect(page.locator('.apikey-item').nth(2).locator('.apikey-paused-badge')).toContainText('Paused until')
  })

  test('pauses a key indefinitely', async ({ page }) => {
    const item = page.locator('.apikey-item').nth(0)
    await item.getByRole('button', { name: 'Pause' }).click()
    await item.getByRole('button', { name: 'Pause key' }).click()
    await expect(item.locator('.apikey-paused-badge')).toHaveText('Paused')
    expect(patches).toEqual([{ id: 'k1', body: { disabled: true, paused_until: null } }])
  })

  test('pauses a key until a chosen time', async ({ page }) => {
    const item = page.locator('.apikey-item').nth(0)
    await item.getByRole('button', { name: 'Pause' }).click()
    await item.getByLabel('Until', { exact: true }).first().check()
    await item.locator('input[type="datetime-local"]').fill('2099-01-02T03:04')
    await item.getByRole('button', { name: 'Pause key' }).click()
    await expect(item.locator('.apikey-paused-badge')).toContainText('Paused until')
    expect(patches[0].body.disabled).toBe(false)
    expect(patches[0].body.paused_until).toMatch(/^2099-01-0[12]T/)
  })

  test('resumes a paused key', async ({ page }) => {
    const item = page.locator('.apikey-item').nth(1)
    await item.getByRole('button', { name: 'Resume' }).click()
    await expect(item.locator('.apikey-paused-badge')).toHaveCount(0)
    expect(patches).toEqual([{ id: 'k2', body: { disabled: false, paused_until: null } }])
  })
})
