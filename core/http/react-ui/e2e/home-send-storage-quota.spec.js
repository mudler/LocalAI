import { test, expect } from './coverage-fixtures.js'
import { mockHome } from './home-fixtures.js'
import { sse, PNG } from './chat-fixtures.js'

// Sending from Home must not depend on free browser storage. Home used to hand
// the message and its attachments (as base64) to the Chat page through
// localStorage; with the storage nearly full (the chat cache holds earlier
// pictures) the write threw QuotaExceededError inside the click handler, so
// Send did nothing at all: no navigation, no request, no message.
//
// The recorded case: a 1.26 MB photo from an iPhone (IMG_9456.HEIC), storage
// filled up to 1M characters below the quota. A smaller picture and the same
// photo with empty storage both worked, and are kept as counter-checks.

const MODEL = 'qwen3-8b-instruct'
const PROMPT = 'Reifendruck?'

// Incompressible bytes of the recorded size, so the base64 form (~1.68M
// characters) does not fit into the 1M characters left free below.
function photoBytes(size = 1_260_000) {
  const buf = Buffer.alloc(size)
  let x = 0x9456
  for (let i = 0; i < size; i++) {
    x = (x * 1103515245 + 12345) >>> 0
    buf[i] = x >>> 24
  }
  return buf
}

const BIG = { name: 'IMG_9456.HEIC', mimeType: 'image/heic', buffer: photoBytes() }
const SMALL = { name: 'small.png', mimeType: 'image/png', buffer: Buffer.from(PNG.split(',')[1], 'base64') }

// Fill localStorage up to its quota, then free one million characters - a
// stand-in for a chat cache full of earlier pictures. Returns the free room.
async function fillStorageNearlyFull(page) {
  return page.evaluate(() => {
    const key = 'localai_chats_data:filler'
    let lo = 0
    let hi = 20e6
    while (hi - lo > 1000) {
      const mid = Math.floor((lo + hi) / 2)
      try { localStorage.setItem(key, 'x'.repeat(mid)); lo = mid } catch { hi = mid }
    }
    localStorage.setItem(key, 'x'.repeat(lo - 1e6))
    return 1e6
  })
}

async function sendFromHome(page, file, { storageNearlyFull }) {
  const pageErrors = []
  page.on('pageerror', e => pageErrors.push(`${e.name}: ${e.message}`))
  const requests = []
  await page.route('**/v1/chat/completions', route => {
    requests.push(route.request().postDataJSON())
    return route.fulfill({ status: 200, contentType: 'text/event-stream', body: sse('Bitte 2,5 bar.') })
  })

  await page.goto('/app')
  await expect(page.locator('.home-greeting')).toBeVisible({ timeout: 15_000 })
  if (storageNearlyFull) await fillStorageNearlyFull(page)

  const composer = page.getByTestId('home-composer')
  await composer.locator('input[type=file][accept="image/*"]').setInputFiles(file)
  await expect(composer.locator('.home-file-tag', { hasText: file.name })).toBeVisible()
  await composer.locator('textarea').fill(PROMPT)
  await composer.locator('button[type=submit]').click()
  return { pageErrors, requests }
}

// What the person sees and what reaches the model: the chat opens, exactly one
// request carries the prompt and the full picture, the answer appears.
async function expectSent(page, file, { pageErrors, requests }) {
  await expect(page).toHaveURL(new RegExp(`/app/chat/${MODEL}`))
  await expect.poll(() => requests.length, { timeout: 15_000 }).toBe(1)
  const body = requests[0]
  expect(body.model).toBe(MODEL)
  const user = body.messages.filter(m => m.role === 'user').at(-1)
  const parts = Array.isArray(user.content) ? user.content : [{ type: 'text', text: user.content }]
  expect(parts.some(p => p.type === 'text' && p.text.includes(PROMPT))).toBe(true)
  const image = parts.find(p => p.type === 'image_url')
  expect(image?.image_url.url).toBe(`data:${file.mimeType};base64,${file.buffer.toString('base64')}`)
  await expect(page.getByText('Bitte 2,5 bar.')).toBeVisible()
  expect(pageErrors.filter(e => /quota/i.test(e))).toEqual([])
}

test.describe('Home send with nearly full browser storage', () => {
  test.beforeEach(async ({ page }) => {
    await mockHome(page)
  })

  test('A: a large photo is sent even when the storage cannot hold it', async ({ page }) => {
    const run = await sendFromHome(page, BIG, { storageNearlyFull: true })
    await expectSent(page, BIG, run)
  })

  test('B: a small picture with nearly full storage is sent (counter-check)', async ({ page }) => {
    const run = await sendFromHome(page, SMALL, { storageNearlyFull: true })
    await expectSent(page, SMALL, run)
  })

  test('C: the large photo with empty storage is sent (counter-check)', async ({ page }) => {
    const run = await sendFromHome(page, BIG, { storageNearlyFull: false })
    await expectSent(page, BIG, run)
  })
})
