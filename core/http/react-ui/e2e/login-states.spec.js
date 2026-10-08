import { test, expect } from './coverage-fixtures.js'
import { mockAccess } from './access-fixtures.js'

// The sign-in screen offers only what /api/auth/status lists, one field per
// step. login.spec.js covers the token option; these cover the variants.

const body = (req) => req.postDataJSON()

test.describe('Sign-in', () => {
  test('asks for the email, then the password, and sends both in one call', async ({ page }) => {
    await mockAccess(page, { status: 'localOnly', statusExtra: { user: null } })
    let sent = null
    await page.route('**/api/auth/login', async route => { sent = body(route.request()); await route.fulfill({ status: 401, json: { error: 'invalid email or password' } }) })
    await page.goto('/login')
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Sign in')
    await expect(page.getByLabel('Password')).toHaveCount(0)
    await page.getByLabel('Email').fill('Alice@Lab.example')
    await page.getByRole('button', { name: 'Continue' }).click()
    await expect(page.getByLabel('Email')).toHaveCount(0)
    await expect(page.getByText('Signing in as Alice@Lab.example')).toBeVisible()
    await page.getByLabel('Password').fill('wrong-password')
    await page.getByRole('button', { name: 'Sign In' }).click()
    await expect(page.getByRole('alert')).toHaveText('invalid email or password')
    expect(sent).toEqual({ email: 'Alice@Lab.example', password: 'wrong-password' })
  })

  test('Back and Use another email return to the first step with the email kept', async ({ page }) => {
    await mockAccess(page, { status: 'localOnly', statusExtra: { user: null } })
    await page.goto('/login')
    await page.getByLabel('Email').fill('alice@lab.example')
    await page.getByRole('button', { name: 'Continue' }).click()
    await page.getByRole('button', { name: 'Back' }).click()
    await expect(page.getByLabel('Email')).toHaveValue('alice@lab.example')
    await page.getByRole('button', { name: 'Continue' }).click()
    await page.getByRole('button', { name: 'Use another email' }).click()
    await expect(page.getByLabel('Email')).toHaveValue('alice@lab.example')
  })

  test('draws a provider button only for a provider the server lists', async ({ page }) => {
    await mockAccess(page, { status: 'localOnly', statusExtra: { user: null } })
    await page.goto('/login')
    await expect(page.getByRole('link', { name: /GitHub/ })).toHaveCount(0)
    await expect(page.getByRole('link', { name: /SSO/ })).toHaveCount(0)

    await mockAccess(page, { status: 'signedOut', statusExtra: { providers: ['local', 'github'], user: null } })
    await page.goto('/login')
    await expect(page.getByRole('link', { name: 'Continue with GitHub' })).toHaveAttribute('href', /\/api\/auth\/github\/login$/)
    await expect(page.getByRole('link', { name: /SSO/ })).toHaveCount(0)
    await expect(page.getByLabel('Email')).toBeVisible()
  })

  test('with only OAuth providers there is no email form', async ({ page }) => {
    await mockAccess(page, { status: 'oauthOnly' })
    await page.goto('/login')
    await expect(page.getByRole('link', { name: 'Continue with GitHub' })).toBeVisible()
    await expect(page.getByRole('link', { name: 'Continue with SSO' })).toBeVisible()
    await expect(page.getByLabel('Email')).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'Register' })).toHaveCount(0)
  })

  test('a sign-up that waits for approval says so on the sign-in screen', async ({ page }) => {
    await mockAccess(page, { status: 'localOnly' })
    let sent = null
    await page.route('**/api/auth/register', async route => { sent = body(route.request()); await route.fulfill({ json: { pending: true, message: '' } }) })
    await page.goto('/login')
    await page.getByRole('button', { name: 'Register' }).click()
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Create an account')
    await page.getByLabel('Email').fill('dave@lab.example')
    await page.getByRole('button', { name: 'Continue' }).click()
    await page.getByLabel('Name').fill('Dave')
    await page.getByLabel('Password', { exact: true }).fill('correct horse battery')
    await page.getByLabel('Confirm Password').fill('correct horse battery')
    await page.getByRole('button', { name: 'Register' }).click()
    const pending = page.getByTestId('login-pending')
    await expect(pending).toContainText('Account created.')
    await expect(pending).toContainText('An admin needs to approve it')
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Sign in')
    expect(sent).toEqual({ email: 'dave@lab.example', password: 'correct horse battery', name: 'Dave' })
  })

  test('registration refuses mismatched passwords before it asks the server', async ({ page }) => {
    await mockAccess(page, { status: 'localOnly' })
    let calls = 0
    await page.route('**/api/auth/register', route => { calls++; return route.fulfill({ json: {} }) })
    await page.goto('/login')
    await page.getByRole('button', { name: 'Register' }).click()
    await page.getByLabel('Email').fill('dave@lab.example')
    await page.getByRole('button', { name: 'Continue' }).click()
    await page.getByLabel('Password', { exact: true }).fill('correct horse battery')
    await page.getByLabel('Confirm Password').fill('different')
    await page.getByRole('button', { name: 'Register' }).click()
    await expect(page.getByRole('alert')).toHaveText('Passwords do not match')
    expect(calls).toBe(0)
  })

  test('a weak password the server allows is sent after the person says so', async ({ page }) => {
    await mockAccess(page, { status: 'localOnly' })
    const sent = []
    await page.route('**/api/auth/register', async route => {
      sent.push(body(route.request()))
      if (sent.length === 1) return route.fulfill({ status: 400, json: { error: 'Password is weak', overridable: true } })
      return route.fulfill({ json: { pending: true } })
    })
    await page.goto('/login')
    await page.getByRole('button', { name: 'Register' }).click()
    await page.getByLabel('Email').fill('dave@lab.example')
    await page.getByRole('button', { name: 'Continue' }).click()
    await page.getByLabel('Password', { exact: true }).fill('password1234')
    await page.getByLabel('Confirm Password').fill('password1234')
    await page.getByRole('button', { name: 'Register' }).click()
    await expect(page.getByRole('alert')).toContainText('Password is weak')
    await page.getByLabel('Use this password anyway').check()
    await page.getByRole('button', { name: 'Register' }).click()
    await expect(page.getByTestId('login-pending')).toBeVisible()
    expect(sent[1].acknowledge_weak_password).toBe(true)
  })

  test('with invite-only registration the sign-in screen offers no Register link', async ({ page }) => {
    await mockAccess(page, { status: 'inviteOnly', statusExtra: { user: null } })
    await page.goto('/login')
    await expect(page.getByRole('button', { name: 'Register' })).toHaveCount(0)
  })

  test('a signed-in person is sent to the app', async ({ page }) => {
    await mockAccess(page, { status: 'admin' })
    await page.goto('/login')
    await page.waitForURL(/\/app\/?$/)
  })
})

test.describe('First admin', () => {
  test('the first person creates the admin account, with no way to sign in', async ({ page }) => {
    await mockAccess(page, { status: 'firstAdmin' })
    let sent = null
    await page.route('**/api/auth/register', async route => { sent = body(route.request()); await route.fulfill({ status: 400, json: { error: 'stop here' } }) })
    await page.goto('/login')
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Create the admin account')
    await expect(page.getByText('this account becomes the admin')).toBeVisible()
    await expect(page.getByText('Already have an account?')).toHaveCount(0)
    await page.getByLabel('Email').fill('alice@lab.example')
    await page.getByRole('button', { name: 'Continue' }).click()
    await expect(page.getByLabel('Invite Code')).toHaveCount(0)
    await page.getByLabel('Password', { exact: true }).fill('correct horse battery')
    await page.getByLabel('Confirm Password').fill('correct horse battery')
    await page.getByRole('button', { name: 'Create Admin Account' }).click()
    await expect(page.getByRole('alert')).toHaveText('stop here')
    expect(sent).toEqual({ email: 'alice@lab.example', password: 'correct horse battery', name: '' })
  })
})

test.describe('API-key-only server', () => {
  test('asks for one key and says what it is for', async ({ page }) => {
    await mockAccess(page, { status: 'keyOnly' })
    let sent = null
    await page.route('**/api/auth/token-login', async route => { sent = body(route.request()); await route.fulfill({ status: 401, json: { error: 'Invalid token' } }) })
    await page.goto('/login')
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Enter your API key')
    await expect(page.getByText('This server is protected by a key.')).toBeVisible()
    await expect(page.getByLabel('Email')).toHaveCount(0)
    await expect(page.getByRole('link', { name: /GitHub|SSO/ })).toHaveCount(0)
    await page.getByLabel('API key').fill('  sk-test  ')
    await page.getByRole('button', { name: 'Sign In' }).click()
    await expect(page.getByRole('alert')).toHaveText('Invalid token')
    expect(sent).toEqual({ token: 'sk-test' })
  })

  test('an empty key is refused in the page', async ({ page }) => {
    await mockAccess(page, { status: 'keyOnly' })
    await page.goto('/login')
    await page.getByRole('button', { name: 'Sign In' }).click()
    await expect(page.getByRole('alert')).toHaveText('Please enter a token')
  })
})

test.describe('Invite page', () => {
  test('fills in the code from the link and sends it with the registration', async ({ page }) => {
    await mockAccess(page, { status: 'localOnly', statusExtra: { user: null, registrationMode: 'invite' } })
    let sent = null
    await page.route('**/api/auth/register', async route => { sent = body(route.request()); await route.fulfill({ json: { pending: true } }) })
    await page.goto('/invite/3f9a1c2d77be0a14')
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Join LocalAI')
    await page.getByLabel('Email').fill('erin@lab.example')
    await page.getByRole('button', { name: 'Continue' }).click()
    const code = page.getByLabel('Invite Code')
    await expect(code).toHaveValue('3f9a1c2d77be0a14')
    await expect(code).toHaveAttribute('readonly', '')
    await expect(page.getByText('Invite code from the link')).toBeVisible()
    await page.getByLabel('Password', { exact: true }).fill('correct horse battery')
    await page.getByLabel('Confirm Password').fill('correct horse battery')
    await page.getByRole('button', { name: 'Register' }).click()
    await expect(page.getByTestId('login-pending')).toBeVisible()
    expect(sent).toMatchObject({ email: 'erin@lab.example', inviteCode: '3f9a1c2d77be0a14' })
  })

  test('the OAuth buttons carry the code', async ({ page }) => {
    await mockAccess(page, { status: 'signedOut', statusExtra: { user: null } })
    await page.goto('/invite/abc123')
    await expect(page.getByRole('link', { name: 'Continue with GitHub' })).toHaveAttribute('href', /invite_code=abc123/)
  })

  test('an OAuth redirect that lacks a valid invite says so', async ({ page }) => {
    await mockAccess(page, { status: 'localOnly', statusExtra: { user: null } })
    await page.goto('/login?error=invite_required')
    await expect(page.getByRole('alert')).toHaveText('A valid invite code is required to register')
  })
})

test.describe('Sign-in: phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })
  test('the brand panel becomes a strip and the form fits', async ({ page }) => {
    await mockAccess(page, { status: 'localOnly', statusExtra: { user: null } })
    await page.goto('/login')
    const brand = await page.locator('.lg-brand').boundingBox()
    const form = await page.locator('.lg-form').boundingBox()
    expect(brand.height).toBeLessThan(120)
    expect(form.y).toBeGreaterThanOrEqual(brand.y + brand.height - 1)
    expect(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)).toBe(false)
  })
})
