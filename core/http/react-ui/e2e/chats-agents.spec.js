import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// The agents page and the agent picker of a new chat, with the API stubbed.

function agentsApi(page, initial = []) {
  const s = { agents: structuredClone(initial), calls: [], createdChats: [] }
  const record = (m, url, body) => s.calls.push({ method: m, url: new URL(url).pathname, body })
  return {
    s,
    async install() {
      await page.route('**/api/chats/skills*', route => route.fulfill({ json: { skills: [{ name: 'review', layer: 'personal', description: 'x', enabled: true }], notes: [] } }))
      await page.route('**/api/agents/collections*', route => route.fulfill({ json: { collections: ['docs', 'notes'] } }))
      await page.route(/\/api\/chats\/agents\/[^/?]+$/, route => {
        const req = route.request()
        const id = new URL(req.url()).pathname.split('/').pop()
        record(req.method(), req.url(), req.postDataJSON?.())
        if (req.method() === 'DELETE') {
          s.agents = s.agents.filter(a => a.id !== id)
          return route.fulfill({ status: 204, body: '' })
        }
        const b = req.postDataJSON()
        s.agents = s.agents.map(a => (a.id === id ? { ...a, ...b } : a))
        return route.fulfill({ json: s.agents.find(a => a.id === id) })
      })
      await page.route(/\/api\/chats\/agents(\?.*)?$/, route => {
        const req = route.request()
        if (req.method() === 'POST') {
          const b = req.postDataJSON()
          record('POST', req.url(), b)
          const a = { id: `a${s.agents.length + 1}`, mcp_servers: [], addons: [], collections: [], skills: [], ...b }
          s.agents.push(a)
          return route.fulfill({ status: 201, json: a })
        }
        return route.fulfill({ json: { agents: s.agents } })
      })
      await page.route(/\/api\/chats(\?.*)?$/, route => {
        const req = route.request()
        if (req.method() === 'POST') {
          const b = req.postDataJSON()
          record('POST', req.url(), b)
          s.createdChats.push(b)
          return route.fulfill({ status: 201, json: { id: 'c1', title: 'New chat', model: b.model || 'qwen3-8b', kind: 'chat', agent_id: b.agent_id, running: false, created: '2026-10-27T10:00:00Z', updated: '2026-10-27T10:00:00Z' } })
        }
        return route.fulfill({ json: { conversations: [], persistent: true, files: true } })
      })
    },
  }
}

test.describe('Chat agents', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
  })

  test('creates an agent with its grants and lists it', async ({ page }) => {
    const api = agentsApi(page)
    await api.install()
    await page.goto('/app/chats/agents')

    await expect(page.getByText('No agents yet.')).toBeVisible()
    await page.getByRole('button', { name: 'New agent' }).click()
    await page.getByLabel('Name', { exact: true }).fill('Reviewer')
    await page.getByLabel('Instructions').fill('Answer in French.')
    await expect(page.getByRole('checkbox', { name: /Always use one model/ })).not.toBeChecked()
    await page.getByRole('combobox', { name: 'Skills +' }).fill('review')
    await page.getByRole('combobox', { name: 'Skills +' }).press('Enter')
    await page.getByRole('combobox', { name: 'Collections +' }).fill('docs')
    await page.getByRole('combobox', { name: 'Collections +' }).press('Enter')
    await expect(page.getByTestId('agent-skills').getByRole('listitem')).toHaveText(['review'])
    await page.getByRole('button', { name: 'Save' }).click()

    await expect(page.getByTestId('agent-row')).toHaveCount(1)
    await expect(page.getByTestId('agent-row')).toContainText('Reviewer')
    await expect(page.getByTestId('agent-row')).toContainText('1 collection, 1 skill')
    expect(api.s.calls.find(c => c.method === 'POST').body).toMatchObject({
      name: 'Reviewer', instructions: 'Answer in French.', skills: ['review'], collections: ['docs'], addons: [], mcp_servers: [], model: '',
    })
  })

  test('edits and deletes an agent, and an empty grant list reads as no tools', async ({ page }) => {
    const api = agentsApi(page, [{ id: 'a1', name: 'Bare', description: '', instructions: '', model: '', mcp_servers: [], addons: [], collections: [], skills: [] }])
    await api.install()
    await page.goto('/app/chats/agents')

    await expect(page.getByTestId('agent-row')).toContainText('Gives no tools')
    await page.getByRole('button', { name: 'Edit' }).click()
    await page.getByLabel('Name', { exact: true }).fill('Bare 2')
    await page.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByTestId('agent-row')).toContainText('Bare 2')
    expect(api.s.calls.find(c => c.method === 'PUT')).toMatchObject({ url: '/api/chats/agents/a1', body: { name: 'Bare 2' } })

    await page.getByRole('button', { name: 'Delete' }).click()
    await expect(page.getByTestId('agent-row')).toHaveCount(0)
  })

  test('shows the reason when the server refuses an agent', async ({ page }) => {
    const api = agentsApi(page)
    await api.install()
    await page.route(/\/api\/chats\/agents(\?.*)?$/, route => (route.request().method() === 'POST'
      ? route.fulfill({ status: 409, json: { error: { message: 'you already have an agent with this name', type: 'conflict', code: 409 } } })
      : route.fulfill({ json: { agents: [] } })))
    await page.goto('/app/chats/agents')
    await page.getByRole('button', { name: 'New agent' }).click()
    await page.getByLabel('Name', { exact: true }).fill('Dup')
    await page.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByRole('alert')).toContainText('already have an agent')
  })

  test('starts a chat with the agent chosen on the agents page', async ({ page }) => {
    const api = agentsApi(page, [{ id: 'a1', name: 'Reviewer', description: 'checks', instructions: '', model: 'qwen3-8b', mcp_servers: [], addons: [], collections: [], skills: [] }])
    await api.install()
    await page.goto('/app/chats/agents')
    await page.getByRole('button', { name: 'Chat', exact: true }).click()
    await expect(page).toHaveURL(/\/app\/chats$/)
    await expect(page.getByLabel('Agent')).toHaveValue('a1')

    await page.getByRole('textbox', { name: 'Message' }).fill('hello')
    await page.getByRole('button', { name: 'Send' }).click()
    await expect.poll(() => api.s.createdChats.length).toBe(1)
    expect(api.s.createdChats[0]).toMatchObject({ agent_id: 'a1' })
  })
})
