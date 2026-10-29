import { test, expect, request as pwRequest } from '@playwright/test'
import { haveBinary, startRealServer } from './real-server.js'

// Authentication against a real local-ai binary with LOCALAI_AUTH on: the
// first account becomes the administrator, a wrong password is refused, API
// keys are created and revoked on the account page, and the per-user features
// (chats, collections, add-ons) take the matching pages and routes away.
//
//   LOCALAI_BIN=<local-ai built with -tags auth> MOCK_BACKEND_BIN=<mock-backend> \
//     PLAYWRIGHT_EXTERNAL_SERVER=1 npx playwright test e2e/real-auth.spec.js
//
// Skipped when the two binaries are not given, so the default run is unchanged.
test.skip(!haveBinary, 'needs LOCALAI_BIN and MOCK_BACKEND_BIN')
test.describe.configure({ mode: 'serial' })

const ADMIN = { email: 'admin@example.test', password: 'Correct-horse-9-battery', name: 'Admin' }
const USER = { email: 'carol@example.test', password: 'Another-long-pass-7', name: 'Carol' }

let server

// The server limits sign-ins, so the specs sign in once through the API and
// reuse the session; only the sign-in specs go through the form.
let adminKey
let carolState
let adminState
let carolId

async function apiLogin(u) {
  const ctx = await pwRequest.newContext({ baseURL: server.url })
  const r = await ctx.post('/api/auth/login', { data: { email: u.email, password: u.password } })
  expect(r.ok(), await r.text()).toBe(true)
  return ctx
}

test.beforeAll(async () => {
  server = await startRealServer({ auth: true })
  const anon = await pwRequest.newContext({ baseURL: server.url })
  for (const u of [ADMIN, USER]) {
    const r = await anon.post('/api/auth/register', { data: u })
    expect(r.ok(), await r.text()).toBe(true)
  }
  const admin = await apiLogin(ADMIN)
  adminState = await admin.storageState()
  const key = await admin.post('/api/auth/api-keys', { data: { name: 'setup' } })
  adminKey = (await key.json()).key
  const carol = await apiLogin(USER)
  carolState = await carol.storageState()
  const users = await (await admin.get('/api/auth/admin/users')).json()
  carolId = (users.users || users).find(u => u.email === USER.email).id
})
test.afterAll(async () => { await server?.close() })

async function signIn(page, u) {
  await page.goto(`${server.url}/login`)
  await page.getByLabel('Email').fill(u.email)
  await page.getByRole('button', { name: /continue/i }).click()
  await page.getByLabel('Password').fill(u.password)
  await page.getByRole('button', { name: /sign in/i }).click()
  await expect(page).toHaveURL(/\/app/)
}

test('an anonymous visitor is sent to the login page and the API refuses them', async ({ page, request }) => {
  await page.goto(`${server.url}/app/chats`)
  await expect(page).toHaveURL(/\/login/)
  const r = await request.get(`${server.url}/api/chats`)
  expect(r.status()).toBe(401)
})

test('the first account is the administrator and signs in through the form', async ({ page }) => {
  await signIn(page, ADMIN)
  await expect(page).toHaveURL(/\/app/)
  await expect(page.getByRole('link', { name: 'Administration' })).toBeVisible()
  await page.goto(`${server.url}/app/chats`)
  await expect(page.getByTestId('chats-page')).toBeVisible()
})

test('a wrong password is refused with a message and stays on the login page', async ({ page }) => {
  await page.goto(`${server.url}/login`)
  await page.getByLabel('Email').fill(ADMIN.email)
  await page.getByRole('button', { name: /continue/i }).click()
  await page.getByLabel('Password').fill('not-the-password-1')
  await page.getByRole('button', { name: /sign in/i }).click()
  await expect(page.getByRole('alert')).toBeVisible()
  await expect(page).toHaveURL(/\/login/)
})

test('an API key made on the account page works for the API and stops working when revoked', async ({ browser, request }) => {
  const context = await browser.newContext({ storageState: adminState })
  const page = await context.newPage()
  await page.goto(`${server.url}/app/account?tab=keys`)
  const panel = page.getByTestId('api-keys-panel')
  await expect(panel).toBeVisible()
  await panel.getByPlaceholder('Key name (e.g. my-app)').fill('spec key')
  await panel.getByRole('button', { name: 'Create', exact: true }).click()
  const secret = (await panel.locator('code').first().innerText()).trim()
  const auth = { Authorization: `Bearer ${secret}` }
  const ok = await request.get(`${server.url}/api/chats`, { headers: auth })
  expect(ok.status()).toBe(200)

  await panel.getByRole('button', { name: 'I copied it' }).click()
  await panel.getByRole('button', { name: /Revoke/ }).first().click()
  const dialog = page.getByRole('alertdialog')
  if (await dialog.isVisible().catch(() => false)) await dialog.getByRole('button', { name: /Revoke/ }).click()
  // Revoking waits a few seconds so it can be undone; then the key stops working.
  await expect.poll(async () => (await request.get(`${server.url}/api/chats`, { headers: auth })).status(), { timeout: 30_000 }).toBe(401)
  await context.close()
})

const setPerms = async (request, perms) => {
  const r = await request.put(`${server.url}/api/auth/admin/users/${carolId}/permissions`, { data: perms, headers: { Authorization: `Bearer ${adminKey}` } })
  expect(r.ok(), await r.text()).toBe(true)
}

async function carolPage(browser) {
  const context = await browser.newContext({ storageState: carolState })
  return { context, page: await context.newPage() }
}

test('a user whose chats feature is off loses the Chats page, the Files page and the chats API', async ({ browser, request }) => {
  await setPerms(request, { chats: false })
  const { context, page } = await carolPage(browser)
  await page.goto(`${server.url}/app`)
  const nav = page.getByRole('complementary', { name: 'Primary navigation' })
  await expect(nav.getByRole('link', { name: 'Home' })).toBeVisible()
  await expect(nav.getByRole('link', { name: 'Chats' })).toHaveCount(0)
  await expect(nav.getByRole('link', { name: 'Files' })).toHaveCount(0)
  expect((await page.request.get(`${server.url}/api/chats`)).status()).toBe(403)
  await page.goto(`${server.url}/app/chats`)
  await expect(page.getByTestId('chats-page')).toHaveCount(0)

  await setPerms(request, { chats: true })
  await page.goto(`${server.url}/app/chats`)
  await expect(page.getByTestId('chats-page')).toBeVisible()
  await context.close()
})

test('a user whose collections feature is off cannot open Memory or call the collections API', async ({ browser, request }) => {
  await setPerms(request, { collections: false })
  const { context, page } = await carolPage(browser)
  expect([403, 404]).toContain((await page.request.get(`${server.url}/api/collections`)).status())
  await page.goto(`${server.url}/app/collections`)
  await expect(page.getByRole('heading', { name: 'Memory' })).toHaveCount(0)
  // The same page opens once the feature is back, so the absence above is the permission.
  await setPerms(request, { collections: true })
  await page.goto(`${server.url}/app/collections`)
  await expect(page.getByRole('heading', { name: 'Memory' }).first()).toBeVisible()
  await context.close()
})

test('a user without the add-ons feature gets no Add-ons entry and the add-ons API refuses them', async ({ browser, request }) => {
  await setPerms(request, { addons: false })
  const { context, page } = await carolPage(browser)
  await page.goto(`${server.url}/app`)
  await expect(page.getByRole('complementary', { name: 'Primary navigation' }).getByRole('link', { name: 'Add-ons' })).toHaveCount(0)
  expect([403, 404, 503]).toContain((await page.request.get(`${server.url}/api/addons/apps`)).status())
  await setPerms(request, { addons: true })
  await page.goto(`${server.url}/app`)
  await expect(page.getByRole('complementary', { name: 'Primary navigation' }).getByRole('link', { name: 'Add-ons' })).toBeVisible()
  await context.close()
})
