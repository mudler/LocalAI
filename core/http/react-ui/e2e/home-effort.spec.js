import { test, expect } from './coverage-fixtures.js'
import { mockHome } from './home-fixtures.js'

// The reasoning effort chip is on the Home composer too. A level picked there
// is saved to the chat when it is created, before the first message goes, and
// the chat page opens showing it. The API is stubbed.

async function server(page) {
  const seen = { created: [], patches: [], order: [] }
  const chat = { id: 'n1', title: 'New chat', model: 'qwen3-8b-instruct', kind: 'chat', created: '2026-10-30T10:00:00Z', updated: '2026-10-30T10:00:00Z', running: false }
  await page.route(/\/api\/chats(\?.*)?$/, route => {
    if (route.request().method() === 'POST') {
      seen.created.push(route.request().postDataJSON())
      seen.order.push('create')
      return route.fulfill({ status: 201, json: chat })
    }
    return route.fulfill({ json: { conversations: [], persistent: true, files: true } })
  })
  await page.route(/\/api\/chats\/n1\/messages(\?.*)?$/, route => {
    if (route.request().method() === 'POST') {
      seen.order.push('send')
      return route.fulfill({ json: { started: true } })
    }
    return route.fulfill({ json: { messages: [] } })
  })
  await page.route('**/api/chats/n1/events', route => route.abort())
  await page.route('**/api/chats/n1', route => {
    if (route.request().method() === 'PATCH') {
      const body = route.request().postDataJSON()
      seen.patches.push(body)
      seen.order.push('patch')
      if (body.settings) chat.settings = body.settings
    }
    return route.fulfill({ json: { ...chat, pending: [] } })
  })
  await page.route(/\/api\/chats\/draft\/(tools|addons)(\?.*)?$/, route => route.fulfill({ json: { builtins: [], addons: [], servers: [], connectors: [], off: [] } }))
  await page.route(/\/api\/chats\/skills(\?.*)?$/, route => route.fulfill({ json: { skills: [] } }))
  await page.route('**/api/chats/agents', route => route.fulfill({ json: { agents: [] } }))
  return seen
}

test.describe('Home reasoning effort chip', () => {
  test.beforeEach(async ({ page }) => { await mockHome(page) })

  test('a level picked on Home is saved to the new chat before the message goes and the chat shows it', async ({ page }) => {
    const seen = await server(page)
    await page.goto('/app')
    await expect(page.getByTestId('home-model-chip')).toContainText('qwen3-8b-instruct')
    const chip = page.getByTestId('chat-effort-button')
    await expect(chip).toContainText('Effort: Default')
    await chip.click()
    await page.getByTestId('chat-effort-high').click()
    await expect(chip).toContainText('Effort: High')

    await page.getByTestId('home-input').fill('hello')
    await page.getByTestId('home-send').click()
    await expect(page).toHaveURL(/\/app\/chats\/n1/)
    expect(seen.patches).toEqual([{ settings: { reasoning_effort: 'high' } }])
    expect(seen.order).toEqual(['create', 'patch', 'send'])
    await expect(page.getByTestId('chat-effort-button')).toContainText('Effort: High')
  })

  test('without a pick the chat is not patched', async ({ page }) => {
    const seen = await server(page)
    await page.goto('/app')
    await expect(page.getByTestId('home-model-chip')).toContainText('qwen3-8b-instruct')
    await page.getByTestId('home-input').fill('hello')
    await page.getByTestId('home-send').click()
    await expect(page).toHaveURL(/\/app\/chats\/n1/)
    expect(seen.patches).toEqual([])
  })
})
