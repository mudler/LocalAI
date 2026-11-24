import { test, expect } from './coverage-fixtures.js'
import { mockAccess, statusFixture } from './access-fixtures.js'

// The session at the edges: a server that cannot say who is signed in, and a
// session that ends while the page is open.

test.describe('Server unreachable', () => {
  test('a failed status read says so, shows no app chrome and recovers on Try again', async ({ page }) => {
    let up = false
    await page.route('**/api/auth/status', route => up
      ? route.fulfill({ json: statusFixture('admin') })
      : route.fulfill({ status: 503, body: '<html>bad gateway</html>', contentType: 'text/html' }))
    await page.goto('/app/backends')
    const box = page.getByTestId('auth-unreachable')
    await expect(box).toBeVisible()
    await expect(box.getByText('Cannot reach the server')).toBeVisible()
    // Not read as "auth is off": no navigation and no admin pages.
    await expect(page.locator('.sidebar-nav')).toHaveCount(0)
    await expect(page.getByTestId('backends')).toHaveCount(0)
    up = true
    await box.getByRole('button', { name: 'Try again' }).click()
    await expect(box).toHaveCount(0)
    await expect(page.getByRole('heading', { name: 'Backend Management' })).toBeVisible()
  })

  test('a network failure retries by itself', async ({ page }) => {
    let up = false
    await page.route('**/api/auth/status', route => up
      ? route.fulfill({ json: statusFixture('admin') })
      : route.abort('connectionrefused'))
    await page.goto('/app/backends')
    await expect(page.getByTestId('auth-unreachable')).toBeVisible()
    up = true
    // The first automatic retry comes after two seconds.
    await expect(page.getByTestId('auth-unreachable')).toHaveCount(0, { timeout: 10_000 })
    await expect(page.getByRole('heading', { name: 'Backend Management' })).toBeVisible()
  })
})

test.describe('Session expires while the page is open', () => {
  test('a 401 from the API sends the person to the sign-in page once', async ({ page }) => {
    await mockAccess(page, { status: 'admin' })
    let expired = false
    let backendCalls = 0
    let statusCalls = 0
    await page.route('**/api/auth/status', route => {
      statusCalls++
      return route.fulfill({ json: statusFixture(expired ? 'signedOut' : 'admin') })
    })
    await page.route('**/api/backends*', route => {
      backendCalls++
      return expired
        ? route.fulfill({ status: 401, json: { error: 'session expired' } })
        : route.fulfill({ json: { backends: [] } })
    })
    await page.goto('/app/backends')
    await expect(page.getByRole('heading', { name: 'Backend Management' })).toBeVisible()
    expired = true
    const before = statusCalls
    await page.getByPlaceholder(/search backends/i).fill('whisper')
    await expect(page).toHaveURL(/\/login$/)
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Sign in')
    expect(backendCalls).toBeGreaterThan(1)
    // One status read confirmed the expiry; the sign-in page reads it once more.
    expect(statusCalls - before).toBeLessThanOrEqual(2)
  })

  test('a 401 the status route does not confirm keeps the person on the page', async ({ page }) => {
    await mockAccess(page, { status: 'admin' })
    await page.route('**/api/backends*', route => route.fulfill({ status: 401, json: { error: 'upstream refused' } }))
    await page.goto('/app/backends')
    await expect(page.getByTestId('load-error')).toBeVisible()
    await expect(page).toHaveURL(/\/app\/backends/)
  })

  test('a wrong password on the sign-in page does not loop back', async ({ page }) => {
    await mockAccess(page, { status: 'localOnly', statusExtra: { user: null } })
    await page.route('**/api/auth/login', route => route.fulfill({ status: 401, json: { error: 'invalid email or password' } }))
    await page.goto('/login')
    await page.getByLabel('Email').fill('a@lab.example')
    await page.getByRole('button', { name: 'Continue' }).click()
    await page.getByLabel('Password').fill('nope')
    await page.getByRole('button', { name: 'Sign In' }).click()
    await expect(page.getByRole('alert')).toHaveText('invalid email or password')
    await expect(page).toHaveURL(/\/login$/)
  })
})
