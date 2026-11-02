// SPDX-License-Identifier: MIT
import { test, expect } from './coverage-fixtures.js'

async function setup(page, models = ['alpha', 'beta']) {
  await page.route('**/api/**', route => route.fulfill({ json: {} }))
  await page.route('**/api/auth/status', route => route.fulfill({ json: { authEnabled: false } }))
  await page.route('**/api/models/capabilities', route => route.fulfill({ json: { data: models.map(id => ({ id, capabilities: ['FLAG_CHAT'] })) } }))
  await page.goto('/app/group-chat')
}
async function participants(page) {
  await page.getByLabel('Model to add').selectOption('alpha')
  await page.getByRole('button', { name: 'Add participant', exact: true }).click()
  await page.getByLabel('Model to add').selectOption('beta')
  await page.getByRole('button', { name: 'Add participant', exact: true }).click()
}
function sse(content = 'Reply', extra = '') {
  return `data: ${JSON.stringify({ choices: [{ delta: { reasoning_content: 'private reasoning', content } }] })}\n\n${extra}data: [DONE]\n\n`
}

test('manual turns share attributed history and lock setup', async ({ page }) => {
  const requests = []
  await page.route('**/v1/chat/completions', async route => {
    requests.push(route.request().postDataJSON())
    await route.fulfill({ contentType: 'text/event-stream', body: sse(`Reply ${requests.length}`) })
  })
  await setup(page)
  await participants(page)
  await page.getByLabel('Name for alpha').fill('Planner')
  await page.getByLabel('Objective').fill('Design a garden')
  await page.getByLabel('Moderator message').fill('Use native plants')
  await page.getByRole('button', { name: 'Add message', exact: true }).click()
  await page.getByRole('button', { name: 'Give Planner a turn', exact: true }).click()
  await expect(page.getByText('Reply 1', { exact: true })).toBeVisible()
  await expect(page.getByLabel('Objective')).toBeDisabled()
  await page.getByRole('button', { name: 'Give beta a turn', exact: true }).click()
  await expect(page.getByText('Reply 2', { exact: true })).toBeVisible()
  expect(requests.map(r => r.model)).toEqual(['alpha', 'beta'])
  expect(requests[0].messages[0].content).toContain('Planner')
  expect(JSON.stringify(requests[1].messages)).toContain('Planner (alpha)')
  expect(JSON.stringify(requests[1].messages)).toContain('Reply 1')
  expect(JSON.stringify(requests[1].messages)).not.toContain('private reasoning')
  expect(requests[1].messages.at(-1).role).toBe('user')
  expect(requests[1].messages.filter(m => m.role === 'assistant')).toEqual([])
  await page.getByRole('button', { name: 'New conversation', exact: true }).click()
  await expect(page.getByText('Reply 1', { exact: true })).toHaveCount(0)
  await expect(page.getByLabel('Objective')).toBeEnabled()
})

test('bounded rounds run in order and reject invalid names', async ({ page }) => {
  const requests = []
  await page.route('**/v1/chat/completions', route => {
    requests.push(route.request().postDataJSON().model)
    return route.fulfill({ contentType: 'text/event-stream', body: sse() })
  })
  await setup(page)
  await participants(page)
  await page.getByLabel('Name for beta').fill('alpha')
  await expect(page.getByRole('button', { name: 'Run one round', exact: true })).toBeDisabled()
  await page.getByLabel('Name for beta').fill('  ')
  await expect(page.getByRole('button', { name: 'Run one round', exact: true })).toBeDisabled()
  await page.getByLabel('Name for beta').fill('beta')
  await page.getByLabel('Number of rounds').fill('11')
  await expect(page.getByRole('button', { name: 'Run rounds', exact: true })).toBeDisabled()
  await page.getByLabel('Number of rounds').fill('2')
  await page.getByRole('button', { name: 'Run rounds', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Run rounds', exact: true })).toBeEnabled()
  expect(requests).toEqual(['alpha', 'beta', 'alpha', 'beta'])
  await page.getByRole('button', { name: 'Run one round', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Run one round', exact: true })).toBeEnabled()
  expect(requests).toEqual(['alpha', 'beta', 'alpha', 'beta', 'alpha', 'beta'])
})

test('stop cancels the run and prevents overlapping turns', async ({ page }) => {
  let count = 0
  let release
  await page.route('**/v1/chat/completions', async route => {
    count++
    await new Promise(resolve => { release = resolve })
    await route.fulfill({ contentType: 'text/event-stream', body: sse('late reply') }).catch(() => {})
  })
  await setup(page)
  await participants(page)
  await page.getByRole('button', { name: 'Run one round', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Give beta a turn', exact: true })).toBeDisabled()
  await expect.poll(() => count).toBe(1)
  await page.getByRole('button', { name: 'Stop', exact: true }).click()
  release()
  await expect(page.getByText('Incomplete', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Run one round', exact: true })).toBeEnabled()
  expect(count).toBe(1)
  await expect(page.getByText('late reply', { exact: true })).toHaveCount(0)
})

for (const [name, response, message] of [
  ['HTTP failure', { status: 500, json: { error: { message: 'Backend failed' } } }, 'Backend failed'],
  ['SSE failure', { contentType: 'text/event-stream', body: 'data: {"error":{"message":"Stream failed"}}\n\n' }, 'Stream failed'],
  ['empty response', { contentType: 'text/event-stream', body: 'data: [DONE]\n\n' }, 'The model returned no text.'],
]) {
  test(`${name} stops remaining turns`, async ({ page }) => {
    let count = 0
    await page.route('**/v1/chat/completions', route => { count++; return route.fulfill(response) })
    await setup(page)
    await participants(page)
    await page.getByRole('button', { name: 'Run one round', exact: true }).click()
    await expect(page.getByRole('alert')).toContainText(message)
    expect(count).toBe(1)
  })
}

test('empty model discovery and chat permission', async ({ page }) => {
  await setup(page, [])
  await expect(page.getByText('No installed chat models available.', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Add participant', exact: true })).toBeDisabled()
  await page.route('**/api/auth/status', route => route.fulfill({ json: { authEnabled: true, user: { role: 'user', permissions: {} } } }))
  await page.reload()
  await expect(page).toHaveURL(/\/app\/?$/)
})

async function stagedStream(page) {
  await page.addInitScript(() => {
    const original = window.fetch
    window.groupRequests = []
    window.groupAborted = false
    window.fetch = async (input, options) => {
      if (!String(input).endsWith('/v1/chat/completions')) return original(input, options)
      window.groupRequests.push(JSON.parse(options.body))
      return new Response(new ReadableStream({
        start(controller) {
          controller.enqueue(new TextEncoder().encode('data: {"choices":[{"delta":{"content":"unfinished text"}}]}\n\n'))
          options.signal.addEventListener('abort', () => {
            window.groupAborted = true
            controller.error(new DOMException('Aborted', 'AbortError'))
          })
        },
      }), { headers: { 'Content-Type': 'text/event-stream' } })
    }
  })
}

test('partial abort is excluded from later prompts and leaving cancels a stream', async ({ page }) => {
  await stagedStream(page)
  await setup(page)
  await participants(page)
  await page.getByRole('button', { name: 'Run one round', exact: true }).click()
  await expect(page.getByText('unfinished text', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Stop', exact: true }).click()
  await expect(page.getByText('Incomplete', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Give beta a turn', exact: true }).click()
  const requests = await page.evaluate(() => window.groupRequests)
  expect(requests).toHaveLength(2)
  expect(JSON.stringify(requests[1])).not.toContain('unfinished text')
  await page.evaluate(() => { window.groupAborted = false })
  await page.getByRole('link', { name: 'Back to Chat', exact: true }).click()
  await expect.poll(() => page.evaluate(() => window.groupAborted)).toBe(true)
  await page.getByRole('link', { name: 'Group chat', exact: true }).click()
  await expect(page.getByText('unfinished text', { exact: true })).toHaveCount(0)
})

for (const [name, body] of [
  ['truncated stream', 'data: {"choices":[{"delta":{"content":"partial"}}]}\n\n'],
  ['malformed stream', 'data: not-json\n\n'],
]) {
  test(`${name} fails without scheduling the next participant`, async ({ page }) => {
    let count = 0
    await page.route('**/v1/chat/completions', route => { count++; return route.fulfill({ contentType: 'text/event-stream', body }) })
    await setup(page)
    await participants(page)
    await page.getByRole('button', { name: 'Run one round', exact: true }).click()
    await expect(page.getByRole('alert')).toBeVisible()
    expect(count).toBe(1)
  })
}
