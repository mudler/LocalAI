import { test, expect } from './coverage-fixtures.js'

async function seedModelChat(page) {
  await page.addInitScript(() => {
    const now = Date.now()
    localStorage.setItem('localai_chats_data', JSON.stringify({
      chats: [{
        id: 'mcp-status-chat',
        name: 'MCP status',
        model: 'test-model',
        history: [],
        systemPrompt: '',
        mcpMode: false,
        mcpServers: [],
        clientMCPServers: [],
        temperature: null,
        topP: null,
        topK: null,
        tokenUsage: { prompt: 0, completion: 0, total: 0 },
        contextSize: null,
        createdAt: now,
        updatedAt: now,
      }],
      activeChatId: 'mcp-status-chat',
      lastSaved: now,
    }))
  })
}

async function mockMCPModel(page) {
  await page.route('**/api/models/capabilities', route => route.fulfill({
    json: { data: [{ id: 'test-model', capabilities: ['FLAG_CHAT'] }] },
  }))
  await page.route('**/api/models/config-json/test-model', route => route.fulfill({
    json: {
      name: 'test-model',
      mcp: { remote: 'mcpServers:\n  ordino:\n    url: http://ordino:8080/mcp' },
    },
  }))
}

test('configured MCP server remains visible with its discovery error', async ({ page }) => {
  await seedModelChat(page)
  await mockMCPModel(page)

  let discoveryRequests = 0
  await page.route('**/v1/mcp/servers/test-model', route => {
    discoveryRequests++
    return route.fulfill({
      json: {
        model: 'test-model',
        servers: [{
          name: 'ordino',
          type: 'remote',
          tools: [],
          error: 'connection failed: lookup ordino.internal: no such host',
        }],
      },
    })
  })

  await page.goto('/app/chat')
  await expect(page.getByRole('button', { name: 'test-model' })).toBeVisible({ timeout: 10_000 })

  await page.locator('.chat-mcp-dropdown > button').click()
  await page.getByRole('button', { name: 'Servers', exact: true }).click()

  const server = page.locator('.chat-mcp-server-item', { hasText: 'ordino' })
  await expect(server).toBeVisible()
  await expect(server).toContainText('lookup ordino.internal: no such host')
  await expect(server.locator('.chat-mcp-server-status--error')).toBeVisible()
  await expect(server.getByRole('checkbox')).toBeDisabled()

  await page.getByRole('button', { name: 'Client', exact: true }).click()
  await page.getByRole('button', { name: 'Servers', exact: true }).click()
  await expect.poll(() => discoveryRequests).toBeGreaterThanOrEqual(2)
})

test('server-list request errors are shown in the MCP menu', async ({ page }) => {
  await seedModelChat(page)
  await mockMCPModel(page)
  await page.route('**/v1/mcp/servers/test-model', route => route.fulfill({
    status: 500,
    json: { message: 'invalid MCP configuration: missing mcpServers map' },
  }))

  await page.goto('/app/chat')
  await expect(page.getByRole('button', { name: 'test-model' })).toBeVisible({ timeout: 10_000 })

  await page.locator('.chat-mcp-dropdown > button').click()
  await page.getByRole('button', { name: 'Servers', exact: true }).click()
  await expect(page.getByRole('alert')).toContainText('invalid MCP configuration: missing mcpServers map')
})

// Non-admin users cannot read /api/models/config-json (admin-only). Their MCP
// availability comes from the `mcp` flag of /api/models/capabilities plus the
// user's `mcp` feature permission.
async function mockNonAdmin(page, permissions) {
  await page.route('**/api/auth/status', route => route.fulfill({
    json: {
      authEnabled: true,
      staticApiKeyRequired: false,
      providers: ['local'],
      hasUsers: true,
      user: { id: 'u-member', name: 'Member', email: 'member@example.com', role: 'user', permissions },
    },
  }))
  let configJsonRequests = 0
  await page.route('**/api/models/config-json/**', route => {
    configJsonRequests++
    return route.fulfill({ status: 403, json: { error: 'admin access required' } })
  })
  await page.route('**/api/models/capabilities', route => route.fulfill({
    json: { data: [{ id: 'test-model', capabilities: ['FLAG_CHAT'], mcp: true }] },
  }))
  await page.route('**/v1/mcp/servers/test-model', route => route.fulfill({
    json: { model: 'test-model', servers: [{ name: 'weather', type: 'remote', tools: ['get_weather'] }] },
  }))
  return () => configJsonRequests
}

test('non-admin user with the mcp feature can select model MCP servers', async ({ page }) => {
  await seedModelChat(page)
  const configJsonRequests = await mockNonAdmin(page, { chat: true, mcp: true })

  await page.goto('/app/chat')
  await expect(page.getByRole('button', { name: 'test-model' })).toBeVisible({ timeout: 10_000 })

  await page.locator('.chat-mcp-dropdown > button').click()
  await page.getByRole('button', { name: 'Servers', exact: true }).click()
  const server = page.locator('.chat-mcp-server-item', { hasText: 'weather' })
  await expect(server).toBeVisible()
  await expect(server.getByRole('checkbox')).toBeEnabled()
  expect(configJsonRequests()).toBe(0)
})

test('non-admin user without the mcp feature gets no model MCP servers', async ({ page }) => {
  await seedModelChat(page)
  await mockNonAdmin(page, { chat: true, mcp: false })

  await page.goto('/app/chat')
  await expect(page.getByRole('button', { name: 'test-model' })).toBeVisible({ timeout: 10_000 })

  await page.locator('.chat-mcp-dropdown > button').click()
  await expect(page.getByRole('button', { name: 'Client', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Servers', exact: true })).toHaveCount(0)
})
