import { test, expect } from './coverage-fixtures.js'
import { startMcpFixture } from './mcp-fixture.js'
import { startChat, transcript, watchProblems, b64 } from './journey-fixtures.js'

// MCP connectors against the real test server and a real MCP server over HTTP
// (e2e/mcp-fixture.js): the connector form, the connection test, the private
// address rule of the settings, the tools panel and an MCP App page shown live
// under its tool line. The server is the one thing outside LocalAI.

test.describe.configure({ mode: 'serial' })

let mcp
let allowedBefore

test.beforeAll(async () => {
  mcp = await startMcpFixture()
})
test.afterAll(async () => { await mcp?.close() })

async function setAllowPrivate(request, on) {
  const r = await request.post('/api/settings', { data: { mcp_connectors_allow_private: on } })
  expect(r.ok()).toBe(true)
}

async function addConnector(page, name, url) {
  await page.goto('/app/mcp-connectors')
  await page.getByRole('button', { name: 'Add MCP server' }).click()
  await page.getByLabel('Name', { exact: true }).fill(name)
  await page.getByLabel('Address').fill(url)
  await page.getByRole('button', { name: 'Save' }).click()
  return page.getByTestId('mcp-row').filter({ hasText: name })
}

test('a connector on a private address is refused until an administrator allows it, then connects', async ({ page, request }) => {
  allowedBefore = (await (await request.get('/api/settings')).json())?.mcp_connectors_allow_private
  await setAllowPrivate(request, false)
  const name = `blocked-${Date.now().toString(36)}`
  const row = await addConnector(page, name, mcp.url)
  await expect(row).toBeVisible()
  await row.getByRole('button', { name: 'Test connection' }).click()
  await expect(row.getByTestId('mcp-test-result')).toContainText(/private|internal/i)

  await setAllowPrivate(request, true)
  await row.getByRole('button', { name: 'Test connection' }).click()
  await expect(row.getByTestId('mcp-test-result')).toContainText('Connected')
  await expect(row.getByTestId('mcp-test-result')).toContainText('echo')

  await row.getByRole('button', { name: 'Delete' }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Delete' }).click()
  await expect(row).toHaveCount(0)
})

test('an enabled connector is in the tools panel, its app shows live under the tool line, and a switch takes it away', async ({ page, request }) => {
  await setAllowPrivate(request, true)
  const watch = watchProblems(page, { ignore: [/notifications\/events/] })
  const name = `clock-${Date.now().toString(36)}`
  const created = await request.post('/api/chats/mcp-connectors', { data: { name, transport: 'http', url: mcp.url, enabled: true } })
  expect(created.ok()).toBe(true)
  const connector = await created.json()

  await startChat(page, `what time ADDON_TOOL_CALL:get-time:${b64({})}`)
  await expect(transcript(page).getByText(/get-time/).first()).toBeVisible({ timeout: 20_000 })
  const frame = page.getByTestId('chat-app').locator('iframe').first()
  await expect(frame).toBeVisible({ timeout: 20_000 })
  await expect(frame).toHaveAttribute('sandbox', 'allow-scripts')

  const panel = page.getByTestId('chat-tools')
  await panel.getByTestId('chat-tools-button').click()
  const sw = panel.getByRole('checkbox', { name: new RegExp(name) })
  await expect(sw).toBeChecked()
  const chatId = page.url().split('/').pop()
  await sw.uncheck()
  await expect.poll(async () => (await (await request.get(`/api/chats/${chatId}/tools`)).json()).off).toContain(`connector:${connector.id}`)
  // The server refuses the app page of a connector the chat switched off.
  const refused = await request.get(`/api/chats/${chatId}/mcp-apps/resource?server=connector:${connector.id}&uri=ui://clock/app.html`)
  expect(refused.status()).toBe(403)

  await request.delete(`/api/chats/mcp-connectors/${connector.id}`)
  expect(watch.problems().filter(p => !/403/.test(p))).toEqual([])
})

// A tool name that two connectors share is offered under each connector's name
// ("<connector>__<tool>"), so neither hides the other and the model can call both.
test('two connectors that offer a tool of the same name still let the model call it', async ({ page, request }) => {
  await setAllowPrivate(request, true)
  const ids = []
  for (const n of ['one', 'two']) {
    const r = await request.post('/api/chats/mcp-connectors', { data: { name: `dup-${n}`, transport: 'http', url: mcp.url, enabled: true } })
    ids.push((await r.json()).id)
  }
  try {
    const id = await startChat(page, `what time ADDON_TOOL_CALL:dup-two__get-time:${b64({})}`)
    await expect(page.getByTestId('chat-app').locator('iframe').first()).toBeVisible({ timeout: 20_000 })
    const lines = (await (await request.get(`/api/chats/${id}/messages`)).json()).messages
    const call = lines.find(m => m.role === 'tool' && m.text.startsWith('dup-two__get-time'))
    expect(call?.app?.server).toBe(`connector:${ids[1]}`)
  } finally {
    for (const id of ids) await request.delete(`/api/chats/mcp-connectors/${id}`)
  }
})

test.afterAll(async ({ request }) => {
  if (allowedBefore !== undefined) await setAllowPrivate(request, Boolean(allowedBefore))
})
