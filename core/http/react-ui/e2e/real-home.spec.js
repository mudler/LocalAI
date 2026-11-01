import { test, expect, request as pwRequest } from '@playwright/test'
import { haveBinary, startRealServer } from './real-server.js'

// The Home console and the first run against real local-ai binaries, with no
// stubbed route: one server with no model installed, one with the mock model.
//
//   LOCALAI_BIN=<local-ai built with -tags auth> MOCK_BACKEND_BIN=<mock-backend> \
//     npx playwright test -c <config with no web server> e2e/real-home.spec.js
test.skip(!haveBinary, 'needs LOCALAI_BIN and MOCK_BACKEND_BIN')
test.describe.configure({ mode: 'serial' })

let empty
let ready

test.beforeAll(async () => {
  ;[empty, ready] = await Promise.all([startRealServer({ withModel: false }), startRealServer()])
})
test.afterAll(async () => {
  await Promise.all([empty?.close(), ready?.close()])
})

const bar = (page) => page.getByRole('combobox', { name: 'Message' })

test('first run: no model installed shows the three steps, and slash actions still work', async ({ page }) => {
  await page.goto(`${empty.url}/app`)
  await expect(page.getByTestId('home-first-run')).toBeVisible({ timeout: 20_000 })
  await expect(page.getByTestId('home-first-run').getByRole('listitem')).toHaveCount(3)
  await bar(page).focus()
  await page.keyboard.type('/')
  await expect(page.getByRole('listbox', { name: 'Actions' })).toBeVisible()
  await page.getByTestId('home-slash-settings').click()
  await expect(page).toHaveURL(/\/app\/settings/)
})

test('first run: the old chats address opens Home, which offers no way to send without a model', async ({ page }) => {
  await page.goto(`${empty.url}/app/chats`)
  await expect(page).toHaveURL(/\/app$/)
  await expect(page.getByTestId('home-first-run')).toBeVisible({ timeout: 20_000 })
  await bar(page).fill('hello')
  await expect(page.locator('.home-send-btn')).toBeDisabled()
})

test('a message typed on Home starts a real chat that answers, and Home lists it to resume', async ({ page }) => {
  await page.goto(`${ready.url}/app`)
  await expect(bar(page)).toBeVisible({ timeout: 20_000 })
  await bar(page).fill('hello from home')
  await bar(page).press('Enter')
  await expect(page).toHaveURL(/\/app\/chats\/[0-9a-f]+$/)
  await expect(page.getByRole('region', { name: 'Conversation' })).toContainText('mocked', { timeout: 30_000 })
  await page.goto(`${ready.url}/app`)
  const row = page.getByTestId('home-conversation').first()
  await expect(row).toBeVisible()
  await row.click()
  await expect(page).toHaveURL(/\/app\/chats\/[0-9a-f]+$/)
  await expect(page.getByRole('region', { name: 'Conversation' })).toContainText('hello from home')
})

test('the LocalAI Assistant entry and /assistant follow the setting, and start an assistant chat', async ({ page }) => {
  const api = await pwRequest.newContext({ baseURL: ready.url })
  await api.post('/api/settings', { data: { localai_assistant_enabled: true } })
  await page.goto(`${ready.url}/app`)
  await expect(page.getByTestId('home-assistant')).toBeVisible({ timeout: 20_000 })
  await bar(page).fill('/')
  await page.getByTestId('home-slash-assistant').click()
  await expect(page).toHaveURL(/\/app\/chats\/[0-9a-f]+$/)
  const id = page.url().split('/').pop()
  const chat = await (await api.get(`/api/chats/${id}`)).json()
  expect(chat.title).toBe('LocalAI Assistant')

  await api.post('/api/settings', { data: { localai_assistant_enabled: false } })
  await page.goto(`${ready.url}/app`)
  await expect(bar(page)).toBeVisible({ timeout: 20_000 })
  await expect(page.getByTestId('home-assistant')).toHaveCount(0)
  await bar(page).fill('/ass')
  await expect(page.getByTestId('home-slash-assistant')).toHaveCount(0)
  const refused = await api.post('/api/chats', { data: { model: 'mock-model', assistant: true } })
  expect(refused.ok()).toBe(false)
  await api.post('/api/settings', { data: { localai_assistant_enabled: true } })
})
