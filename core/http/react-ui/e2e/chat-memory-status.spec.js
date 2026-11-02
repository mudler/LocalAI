// SPDX-License-Identifier: MIT
// UI contract tests; durable worker/backend acceptance is a separate gate.
import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

const capture = (over = {}) => ({ id: 'capture /?#', destination: 'handbook', status: 'failed', reason: 'summary_failed', retry_eligible: true, ...over })
const panel = page => page.getByTestId('chat-memory-status')
// Empty threads are hidden from the accessibility tree; assert their DOM too.
const log = page => page.getByTestId('chat-thread')
async function fixture(page, rows = [capture()]) {
  await mockChat(page)
  const state = { rows, reads: 0, posts: [], reply: null, read: null }
  await page.addInitScript(() => {
    window.__memoryStreams = []
    window.EventSource = class {
      constructor(url) { this.url = url; this.listeners = {}; window.__memoryStreams.push(this); setTimeout(() => this.onopen?.(), 0) }
      addEventListener(kind, fn) { this.listeners[kind] = fn }
      close() {}
    }
    window.__memoryEmit = (kind, payload = {}) => {
      const stream = window.__memoryStreams.findLast(s => s.url.includes("/api/chats/c1/events"))
      stream.listeners[kind]?.({ data: JSON.stringify(payload) })
    }
  })
  const details = ['c1', 'c2'].map(id => ({ id, title: id, model: 'qwen3-8b', kind: 'chat', pending: [], running: false, last_turn: 'done' }))
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({ json: { conversations: details, persistent: true, files: true } }))
  await page.route(/\/api\/chats\/c[12]$/, route => route.fulfill({ json: details.find(d => route.request().url().endsWith(d.id)) }))
  await page.route(/\/api\/chats\/c[12]\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: [] } }))
  await page.route(/\/api\/chats\/c[12]\/memory$/, async route => {
    state.reads++
    if (state.read) return state.read(route)
    return route.fulfill({ json: route.request().url().includes('/c2/') ? [capture({ id: 'second', destination: 'second-chat', status: 'saved', entry_id: 'entry-second', retry_eligible: false })] : state.rows })
  })
  await page.route('**/api/chats/c1/memory/*/retry', async route => {
    state.posts.push(route.request().url())
    if (state.reply) return state.reply(route)
    state.rows = [capture({ status: 'queued', reason: '', retry_eligible: false })]
    return route.fulfill({ json: state.rows[0] })
  })
  return state
}
async function open(page) {
  await page.goto('/app/chats/c1')
  await expect(panel(page)).toBeVisible()
  await panel(page).locator('summary').click()
}
async function switchChat(page) {
  await page.evaluate(() => {
    history.pushState({}, '', '/app/chats/c2')
    window.dispatchEvent(new PopStateEvent('popstate'))
  })
  await expect(page).toHaveURL(/\/c2$/)
}
for (const theme of ['light', 'dark']) {
  test(`capture states remain separate from conversation and survive reload (${theme})`, async ({ page }) => {
    await page.addInitScript(value => localStorage.setItem('localai-theme', value), theme)
    await fixture(page, [capture({ status: 'saved', entry_id: 'exact-entry/42', retry_eligible: false }), capture({ id: 'queued', status: 'queued', next_retry: 1700000000, reason: '', retry_eligible: false }), capture({ id: 'processing', status: 'processing', reason: '', retry_eligible: false }), capture({ id: 'skipped', status: 'skipped', reason: 'private diagnostic', retry_eligible: false })])
    await open(page)
    await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
    await expect(panel(page)).toContainText('exact-entry/42')
    await expect(panel(page)).toContainText('No further details available.')
    await expect(panel(page)).not.toContainText('private diagnostic')
    await expect(panel(page).locator('time')).toHaveAttribute('datetime', '2023-11-14T22:13:20.000Z')
    await expect(panel(page).getByRole('button', { name: /Retry capture/ })).toHaveCount(0)
    await expect(log(page)).not.toContainText('handbook')
    await expect(log(page).locator('.cx-msg')).toHaveCount(0)
    await page.reload()
    await panel(page).locator('summary').focus()
    await page.keyboard.press('Enter')
    await expect(panel(page)).toContainText('exact-entry/42')
  })
}
test('eligible retry is encoded, disabled in flight and reloads fresh GET state', async ({ page }) => {
  const state = await fixture(page)
  let release
  state.reply = route => new Promise(resolve => { release = async () => {
    state.rows = [capture({ status: 'saved', entry_id: 'fresh-get-entry', retry_eligible: false })]
    await route.fulfill({ json: capture({ status: 'queued' }) }); resolve()
  } })
  await open(page)
  await panel(page).getByRole('button', { name: 'Retry capture to handbook' }).click()
  await expect(panel(page).getByRole('button', { name: 'Retry capture to handbook' })).toBeDisabled()
  await expect.poll(() => state.posts.length).toBe(1)
  expect(state.posts[0]).toContain('/memory/capture%20%2F%3F%23/retry')
  await release()
  await expect(panel(page)).toContainText('fresh-get-entry')
  await expect(log(page)).not.toContainText('fresh-get-entry')
})
for (const status of [403, 404, 409]) {
  test(`retry ${status} is an inline operational error, never a failed chat turn`, async ({ page }) => {
    const state = await fixture(page)
    state.reply = route => route.fulfill({ status, json: { error: 'private backend diagnostic' } })
    await open(page)
    await panel(page).getByRole('button', { name: 'Retry capture to handbook' }).click()
    await expect(panel(page).getByRole('alert')).toBeVisible()
    await expect(panel(page)).not.toContainText('private backend diagnostic')
    await expect(log(page)).not.toContainText('Retry')
    await expect(log(page).locator('.cx-msg')).toHaveCount(0)
  })
}
test('SSE, reconnect and missed-fanout polling re-read durable status without messages', async ({ page }) => {
  const state = await fixture(page)
  await page.clock.install()
  await open(page)
  state.rows = [capture({ status: 'processing', reason: '', retry_eligible: false })]
  await page.evaluate(() => window.__memoryEmit('memory_capture', { ids: ['capture /?#'], text: 'must not enter the transcript' }))
  await expect(panel(page)).toContainText('Processing')
  state.rows = [capture({ status: 'saved', entry_id: 'after-reconnect', retry_eligible: false })]
  await page.evaluate(() => window.__memoryStreams.findLast(s => s.url.includes("/api/chats/c1/events")).onerror())
  await expect(panel(page)).toContainText('after-reconnect')
  state.rows = [capture({ status: 'saved', entry_id: 'missed-fanout', retry_eligible: false })]
  await page.clock.fastForward(16000)
  await expect(panel(page)).toContainText('missed-fanout')
  await expect(log(page)).not.toContainText('must not enter the transcript')
  await expect(log(page).locator('.cx-msg')).toHaveCount(0)
})
test('chat switch rejects delayed GET and retry responses from the old chat', async ({ page }) => {
  const state = await fixture(page)
  await open(page)
  let releaseRead, releaseRetry
  state.read = route => new Promise(resolve => { releaseRead = async () => {
    await route.fulfill({ json: [capture({ destination: 'stale-read' })] }).catch(() => {}); resolve()
  } })
  await panel(page).getByRole('button', { name: 'Refresh memory status' }).click()
  await expect.poll(() => !!releaseRead).toBe(true)
  state.read = null
  state.reply = route => new Promise(resolve => { releaseRetry = async () => {
    await route.fulfill({ json: capture({ destination: 'stale-retry' }) }).catch(() => {}); resolve()
  } })
  await panel(page).getByRole('button', { name: 'Retry capture to handbook' }).click()
  await expect.poll(() => !!releaseRetry).toBe(true)
  await switchChat(page)
  await expect(panel(page)).toBeVisible()
  await panel(page).locator('summary').click()
  await expect(panel(page)).toContainText('entry-second')
  await releaseRead(); await releaseRetry()
  await expect(panel(page)).not.toContainText('stale-')
  await expect(panel(page)).toContainText('second-chat')
})
