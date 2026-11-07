// SPDX-License-Identifier: MIT
import { test, expect } from './coverage-fixtures.js'
import { installAgents, AGENTS } from './agents-fixtures.js'

// Sharing a saved agent: the list with agents shared with you, the owner's
// Share dialog with the things the agent uses, the read-only page of a
// person the agent is shared with, and the notice of a chat with it. The
// agents API and the sharing API are stubbed; starting the chat is not. The
// grantee's own start of a chat, with accounts, is covered by the Go tests of
// the route (core/http/agent_share_route_test.go).

const SHARE_ID = 'r0123456789abcdef0123456789abcde'

const owned = (a, sharedWith = 0) => ({ ...a, access: { role: 'owner', sharedWith, sharing: true, share_id: sharedWith ? SHARE_ID : undefined } })

const SHARED = {
  id: 's1', name: 'release-notes', description: 'Drafts release notes from the merged changes.', model: 'qwen3-8b',
  instructions: '', mcp_servers: [], addons: [], collections: [], skills: [], builtins: [], builtins_off: [], builtins_on: [],
  created: '2026-10-03T10:00:00Z', updated: '2026-10-04T10:00:00Z',
  access: { role: 'use', owner: 'Alice', everyone: false, sharing: false },
}

const REFERENCES = [
  { kind: 'collection', id: 'handbook', label: 'handbook', share: { kind: 'collection', resource_id: 'rcoll000000000000000000000000001', options: ['view'], roles: { view: 'viewer' } } },
  { kind: 'connection', id: 'b-alice', addon_id: 'mail', label: 'mail connection 1', share: { kind: 'connection', resource_id: 'rconn000000000000000000000000001', options: ['use'], roles: { use: 'use' } } },
  { kind: 'addon', id: 'office', label: 'office' },
  { kind: 'skill', id: 'citations', label: 'citations' },
  { kind: 'folder', id: '12', label: 'Projects', mode: 'read' },
]

const NEEDS = [
  { kind: 'addon', label: 'office', state: 'ok' },
  { kind: 'collection', label: 'handbook', state: 'missing' },
  { kind: 'skill', label: 'citations', state: 'unavailable' },
]

async function installSharing(page, { agents, needs = NEEDS } = {}) {
  const s = await installAgents(page, { agents: agents || [owned(AGENTS[0], 2), owned(AGENTS[1]), SHARED] })
  s.shareCalls = []
  const rec = (route) => {
    const r = route.request()
    s.shareCalls.push({ method: r.method(), url: new URL(r.url()).pathname, body: r.postData() ? r.postDataJSON() : null, confirm: r.headers()['x-localai-confirm'] || '' })
  }
  await page.route(/\/api\/chats\/agents\/[^/]+\/share$/, route => { rec(route); return route.fulfill({ json: { kind: 'agent', id: SHARE_ID } }) })
  await page.route(/\/api\/chats\/agents\/[^/]+\/references$/, route => route.fulfill({ json: { references: REFERENCES } }))
  await page.route(/\/api\/chats\/agents\/[^/]+\/needs$/, route => route.fulfill({ json: { needs } }))
  await page.route(/\/api\/chats\/agents\/[^/]+\/runs(\?.*)?$/, route => route.fulfill({ json: { runs: [], total: 0, failed: 0, running: 0 } }))
  const grants = []
  await page.route(/\/api\/shares\/[^/]+\/[^/]+\/grants(\/[^/?]+)?(\?.*)?$/, route => {
    rec(route)
    const r = route.request()
    if (r.method() === 'PUT') {
      const principal = decodeURIComponent(new URL(r.url()).pathname.split('/').pop())
      grants.push({ userId: principal, displayName: principal === 'u-bob' ? 'Bob' : '', role: r.postDataJSON().role })
    }
    return route.fulfill({ json: { grants } })
  })
  await page.route(/\/api\/shares\/[^/]+\/[^/]+\/users(\?.*)?$/, route => route.fulfill({ json: { users: [{ id: 'u-bob', displayName: 'Bob' }] } }))
  return s
}

test.describe('Shared agents', () => {
  test('the list marks the agents shared with you and the ones you share', async ({ page }) => {
    await installSharing(page)
    await page.goto('/app/agents')
    await expect(page.getByTestId('agents-list').locator('.ag-row')).toHaveCount(3)
    const shared = page.locator('[data-agent="release-notes"]')
    await expect(shared).toHaveAttribute('data-role', 'use')
    await expect(shared.getByTestId('agent-shared')).toContainText('Shared by Alice')
    await expect(shared.getByTestId('agent-shared')).toContainText('Can use')
    await expect(page.locator('[data-agent="research-assistant"]').getByTestId('agent-shared')).toContainText('Shared with 2')
    await expect(page.locator('[data-agent="code-reviewer"]').getByTestId('agent-shared')).toHaveCount(0)
    // A person the agent is shared with can only open it from the menu.
    await shared.getByRole('button', { name: 'More actions for release-notes' }).click()
    await expect(page.getByRole('menuitem', { name: 'Open' })).toBeVisible()
    await expect(page.getByRole('menuitem', { name: 'Edit' })).toHaveCount(0)
    await expect(page.getByRole('menuitem', { name: 'Share' })).toHaveCount(0)
    await expect(page.getByRole('menuitem', { name: 'Delete' })).toHaveCount(0)
  })

  test('the owner shares with a person after deciding each thing the agent uses', async ({ page }) => {
    const s = await installSharing(page)
    await page.goto('/app/agents')
    await page.locator('[data-agent="research-assistant"]').getByRole('button', { name: 'More actions for research-assistant' }).click()
    await page.getByRole('menuitem', { name: 'Share' }).click()
    const dialog = page.getByTestId('agent-share-dialog')
    await expect(dialog).toBeVisible()
    await expect(dialog.getByTestId('share-ref')).toHaveCount(2)
    await expect(dialog.getByTestId('share-ref-own')).toHaveCount(3)
    await expect(dialog.getByTestId('share-ref-own').first()).toContainText('They need access of their own')
    await dialog.getByTestId('share-search').fill('Bob')
    const add = dialog.getByTestId('share-user').getByRole('button', { name: /Add/ })
    await expect(add).toBeDisabled()
    await expect(dialog.getByTestId('share-refs-pending')).toBeVisible()
    // The choices are the ones the server offers for each thing: a
    // collection is view, a connection is use.
    await expect(dialog.getByLabel('Access to handbook').locator('option')).toHaveCount(3)
    await expect(dialog.getByLabel('Access to handbook').locator('option[value="use"]')).toHaveCount(0)
    await dialog.getByLabel('Access to handbook').selectOption('view')
    await expect(add).toBeDisabled()
    await dialog.getByLabel('Access to mail connection 1').selectOption('use')
    await expect(add).toBeEnabled()
    await add.click()
    await expect(dialog.getByTestId('share-grant')).toHaveCount(1)
    await expect.poll(() => s.shareCalls.filter(c => c.method === 'PUT').map(c => [c.url, c.body.role, c.confirm])).toEqual([
      [`/api/shares/agent/${SHARE_ID}/grants/u-bob`, 'use', SHARE_ID],
      ['/api/shares/collection/rcoll000000000000000000000000001/grants/u-bob', 'viewer', 'rcoll000000000000000000000000001'],
      ['/api/shares/connection/rconn000000000000000000000000001/grants/u-bob', 'use', 'rconn000000000000000000000000001'],
    ])
  })

  test('a person the agent is shared with reads its page, sees what is missing and can chat', async ({ page }) => {
    await installSharing(page)
    await page.goto('/app/agents/s1')
    await expect(page.getByTestId('agent-page')).toBeVisible()
    await expect(page.getByTestId('agent-page-shared-by')).toContainText('Shared by Alice')
    await expect(page.getByTestId('agent-chat')).toBeVisible()
    await expect(page.getByRole('link', { name: 'Edit' })).toHaveCount(0)
    await expect(page.getByTestId('agent-share')).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'More actions for release-notes' })).toHaveCount(0)
    await expect(page.getByTestId('agent-triggers')).toHaveCount(0)
    const needs = page.getByTestId('agent-need')
    await expect(needs).toHaveCount(3)
    await expect(needs.nth(1)).toHaveAttribute('data-state', 'missing')
    await expect(needs.nth(1)).toContainText('Not shared with you')
    await expect(needs.nth(2)).toContainText('Not available to you')
  })

  test('a chat with a shared agent says what is missing and what it asks for, and starts on the real chat route', async ({ page, request }) => {
    // The agent is a real one of the test server, so POST /api/chats runs
    // the server's own handler and lookup; only the listing presents it as
    // shared (the test server has no accounts to share it between).
    const made = await request.post('/api/chats/agents', { data: { name: 'release-notes-real', model: 'mock-model', network: 'all', builtins_on: ['saved_agents'] } })
    expect(made.status()).toBe(201)
    const real = await made.json()
    try {
      const s = await installSharing(page, {
        agents: [owned(AGENTS[0], 2), { ...SHARED, id: real.id, model: 'mock-model' }],
        needs: [...NEEDS, { kind: 'network', label: 'all', state: 'ask' }, { kind: 'tool-group', label: 'saved_agents', state: 'ask' }],
      })
      // The chat-start route is not stubbed: it reaches the server.
      await page.route(/\/api\/chats(\?.*)?$/, route => (route.request().method() === 'POST' ? route.continue() : route.fallback()))
      await page.goto('/app/agents')
      await page.locator('[data-agent="release-notes"]').getByRole('button', { name: 'Chat' }).click()
      await expect(page).toHaveURL(/\/app\/chats$/)
      const notice = page.getByTestId('agent-needs-notice')
      await expect(notice).toBeVisible()
      await expect(notice).toContainText('This agent needs 2 things you do not have')
      await expect(notice).toContainText('handbook, citations')
      const asks = page.getByTestId('agent-needs-asks')
      await expect(asks).toContainText('This agent asks for 2 things your chat starts without')
      await expect(asks).toContainText('Network: all')
      await expect(asks).toContainText('Tool group: saved_agents')
      const started = page.waitForResponse(r => new URL(r.url()).pathname === '/api/chats' && r.request().method() === 'POST')
      await page.getByTestId('chat-input').fill('hello')
      await page.getByTestId('chat-send').click()
      const res = await started
      expect(res.status(), await res.text()).toBe(201)
      expect(res.request().postDataJSON()).toMatchObject({ agent_id: real.id })
      const chat = await res.json()
      expect(chat).toMatchObject({ agent_id: real.id, model: 'mock-model' })
      expect(s.createdChats).toHaveLength(0)
      await request.delete(`/api/chats/${chat.id}`)
    } finally {
      await request.delete(`/api/chats/agents/${real.id}`)
    }
  })

  test('nothing is said when the person has everything, and no sharing shows without accounts', async ({ page }) => {
    const plain = AGENTS.map(a => ({ ...a, access: { role: 'owner', sharing: false } }))
    await installSharing(page, { agents: plain, needs: [{ kind: 'addon', label: 'office', state: 'ok' }] })
    await page.goto('/app/agents')
    await expect(page.getByTestId('agents-list').locator('.ag-row')).toHaveCount(2)
    await expect(page.getByTestId('agent-shared')).toHaveCount(0)
    await page.locator('[data-agent="code-reviewer"]').getByRole('button', { name: 'More actions for code-reviewer' }).click()
    await expect(page.getByRole('menuitem', { name: 'Edit' })).toBeVisible()
    await expect(page.getByRole('menuitem', { name: 'Share' })).toHaveCount(0)
  })
})
