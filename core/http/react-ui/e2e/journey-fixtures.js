import { expect } from '@playwright/test'

// Helpers of the journey specs that run against the real test server: the
// mock model is offered to the model picker, and every console error, page
// error and failed request of a journey is collected so a spec can assert the
// journey was quiet.

export async function offerMockModel(page) {
  await page.route('**/api/models/capabilities', route =>
    route.fulfill({ json: { data: [{ id: 'mock-model', capabilities: ['FLAG_CHAT'] }] } }))
}

// Requests the browser cancels on its own when the page navigates away.
const BENIGN = /net::ERR_ABORTED|net::ERR_NETWORK_CHANGED/

// Watches a page. problems() lists what a quiet journey would not have.
export function watchProblems(page, { ignore = [] } = {}) {
  const found = []
  const skip = (text) => ignore.some(re => re.test(text))
  page.on('console', (m) => {
    if (m.type() === 'error' && !skip(m.text())) found.push(`console: ${m.text()}`)
  })
  page.on('pageerror', (e) => found.push(`pageerror: ${e.message}`))
  page.on('requestfailed', (r) => {
    const why = r.failure()?.errorText || ''
    if (!BENIGN.test(why) && !skip(r.url())) found.push(`request failed: ${r.method()} ${new URL(r.url()).pathname} ${why}`)
  })
  page.on('response', (r) => {
    const path = new URL(r.url()).pathname
    if (r.status() >= 400 && !skip(path) && !skip(`${r.status()} ${path}`)) found.push(`http ${r.status()}: ${r.request().method()} ${path}`)
  })
  return { problems: () => [...found] }
}

export const composer = (page) => page.getByTestId('chat-input')
export const transcript = (page) => page.getByRole('region', { name: 'Conversation' })

// Home is where a chat starts: its message box sends the first message, and
// the chat opens at /app/chats/<id>.
export const homeBox = (page) => page.locator('.home-textarea')

export async function sendFromHome(page, text) {
  await page.goto('/app')
  await homeBox(page).fill(text)
  // Send waits for the model picker to choose a model.
  await expect(page.locator('.home-send-btn')).toBeEnabled({ timeout: 15_000 })
  await page.locator('.home-send-btn').click()
}

// The empty chat, before its first message: /new on Home. /app/chats as an
// address opens Home, so this is how a spec reaches the empty chat's message
// box (to attach a file first, or to read its notices and shortcuts).
export async function openNewChat(page) {
  await page.goto('/app')
  await expect(homeBox(page)).toBeVisible({ timeout: 15_000 })
  await homeBox(page).fill('/new')
  await homeBox(page).press('Enter')
  await page.waitForURL(/\/app\/chats(\?|$)/)
  await expect(page.getByTestId('chats-page')).toBeVisible()
}

export async function startChat(page, text) {
  await offerMockModel(page)
  await sendFromHome(page, text)
  await page.waitForURL(/\/app\/chats\/[0-9a-f]+$/)
  return page.url().split('/').pop()
}

export function b64(obj) {
  return Buffer.from(JSON.stringify(obj)).toString('base64url')
}
