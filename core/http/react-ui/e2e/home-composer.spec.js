import { test, expect } from './coverage-fixtures.js'
import { mockHome } from './home-fixtures.js'

// The composer of Home is the chat composer: files, the saved agent, the Tools
// and Skills menus and the draft all work before the chat exists. Send creates
// the chat with the model, the agent and the drafted switches, sends the first
// message with its files and opens the chat. The API is stubbed in the shapes
// of the server (core/schema/chats.go).

const CONV = { id: 'n1', title: 'New chat', model: 'qwen3-8b-instruct', kind: 'chat', created: '2026-10-30T10:00:00Z', updated: '2026-10-30T10:00:00Z', running: false }

async function server(page, { failSend = false, agents = [] } = {}) {
  const seen = { created: [], sent: [], deleted: [] }
  await page.route(/\/api\/chats(\?.*)?$/, async route => {
    if (route.request().method() === 'POST') {
      const body = route.request().postDataJSON()
      seen.created.push(body)
      return route.fulfill({ status: 201, json: { ...CONV, model: body.model || CONV.model } })
    }
    return route.fulfill({ json: { conversations: [], persistent: true, files: true } })
  })
  await page.route(/\/api\/chats\/n1\/messages(\?.*)?$/, route => {
    if (route.request().method() === 'POST') {
      const req = route.request()
      seen.sent.push({ type: req.headers()['content-type'] || '', body: req.postDataBuffer()?.toString('utf8') || '' })
      if (failSend) return route.fulfill({ status: 413, json: { error: { message: 'the file is too large' } } })
      return route.fulfill({ json: { started: true } })
    }
    return route.fulfill({ json: { messages: [] } })
  })
  await page.route('**/api/chats/n1/events', route => route.abort())
  await page.route('**/api/chats/n1', route => {
    if (route.request().method() === 'DELETE') { seen.deleted.push('n1'); return route.fulfill({ json: {} }) }
    return route.fulfill({ json: { ...CONV, pending: [] } })
  })
  // What a new chat could switch: one group of built-in tools, one add-on and
  // two skills, every switch in its default state.
  await page.route(/\/api\/chats\/draft\/tools(\?.*)?$/, route => route.fulfill({ json: {
    builtins: [{ group: 'web', tools: ['web_search', 'web_fetch'], off: false, usable: true, provider: 'builtin', providers: {} }],
    addons: [{ id: 'office', tools: 4, off: false }], servers: [], connectors: [], off: [],
  } }))
  await page.route(/\/api\/chats\/draft\/addons(\?.*)?$/, route => route.fulfill({ json: { addons: [{ id: 'office', tools: 4, off: false }], commands: 'none' } }))
  await page.route(/\/api\/chats\/skills(\?.*)?$/, route => route.fulfill({ json: { skills: [
    { id: 'builtin:pdf', name: 'pdf', description: 'Read and write PDF files', layer: 'builtin', enabled: true },
    { id: 'personal:notes', name: 'notes', description: 'Keep notes', layer: 'personal', enabled: true },
  ] } }))
  await page.route('**/api/chats/agents', route => route.fulfill({ json: { agents } }))
  return seen
}

async function ready(page) {
  await page.goto('/app')
  await expect(page.locator('.home-greeting')).toBeVisible({ timeout: 15_000 })
  await expect(page.getByTestId('home-model-chip')).toContainText('qwen3-8b-instruct')
}

test.describe('Home composer', () => {
  test.beforeEach(async ({ page }) => {
    await mockHome(page)
  })

  test('a file attached on Home arrives with the first message of the new chat', async ({ page }) => {
    const seen = await server(page)
    await ready(page)
    await page.getByTestId('home-input').fill('What is in this file?')
    await page.getByTestId('chat-file-input').setInputFiles({ name: 'notes.txt', mimeType: 'text/plain', buffer: Buffer.from('remember the milk') })
    await expect(page.getByTestId('home-composer').getByText('notes.txt')).toBeVisible()
    await page.getByTestId('home-send').click()
    await expect(page).toHaveURL(/\/app\/chats\/n1$/)
    expect(seen.created).toHaveLength(1)
    expect(seen.sent).toHaveLength(1)
    expect(seen.sent[0].type).toContain('multipart/form-data')
    expect(seen.sent[0].body).toContain('What is in this file?')
    expect(seen.sent[0].body).toContain('filename="notes.txt"')
    expect(seen.sent[0].body).toContain('remember the milk')
  })

  test('a pasted image is attached, not typed', async ({ page }) => {
    await server(page)
    await ready(page)
    const box = page.getByTestId('home-input')
    await box.focus()
    await box.evaluate((el) => {
      const dt = new DataTransfer()
      dt.items.add(new File([new Uint8Array([137, 80, 78, 71])], 'image.png', { type: 'image/png' }))
      el.dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true }))
    })
    await expect(page.getByTestId('home-composer').getByText(/^pasted-image-\d+-1\.png$/)).toBeVisible()
    await expect(box).toHaveValue('')
  })

  test('tools and skills switched off on Home are stored with the new chat', async ({ page }) => {
    const seen = await server(page)
    await ready(page)
    await page.getByTestId('chat-tools-button').click()
    const tools = page.getByTestId('chat-tools-menu')
    await expect(tools).toContainText('A new chat starts with these switches.')
    await tools.getByRole('switch', { name: 'office', exact: true }).click()
    await expect(tools.getByRole('switch', { name: 'office', exact: true })).toHaveAttribute('aria-checked', 'false')
    await page.keyboard.press('Escape')

    await page.getByTestId('chat-skills-button').click()
    await page.getByRole('switch', { name: 'pdf', exact: true }).click()
    await expect(page.getByTestId('chat-skills-summary')).toHaveText('1 of 2 on')
    await page.keyboard.press('Escape')

    await page.getByTestId('home-input').fill('Plan my week')
    await page.getByTestId('home-input').press('Enter')
    await expect(page).toHaveURL(/\/app\/chats\/n1$/)
    expect(seen.created).toHaveLength(1)
    expect(seen.created[0].model).toBe('qwen3-8b-instruct')
    expect(seen.created[0].switches).toEqual({ addons_off: ['office'], skills_off: ['builtin:pdf'] })
  })

  test('a chat with nothing switched sends no switches', async ({ page }) => {
    const seen = await server(page)
    await ready(page)
    await page.getByTestId('home-input').fill('hello')
    await page.getByTestId('home-send').click()
    await expect(page).toHaveURL(/\/app\/chats\/n1$/)
    expect(seen.created[0]).not.toHaveProperty('switches')
  })

  test('the saved agent chosen on Home starts the chat', async ({ page }) => {
    const seen = await server(page, { agents: [{ id: 'a1', name: 'Reviewer', model: '' }] })
    await ready(page)
    await page.getByTestId('chat-agent-picker').getByRole('button').click()
    // Choosing closes the menu.
    await page.getByRole('radio', { name: 'Reviewer' }).click()
    await expect(page.getByTestId('chat-agent-picker').getByRole('button')).toContainText('Reviewer')
    await page.getByTestId('home-input').fill('Review this')
    await page.getByTestId('home-send').click()
    await expect(page).toHaveURL(/\/app\/chats\/n1$/)
    expect(seen.created[0].agent_id).toBe('a1')
  })

  test('the slash menu of Home opens in the rich composer', async ({ page }) => {
    await server(page)
    await ready(page)
    await page.getByTestId('home-input').fill('/')
    await expect(page.getByRole('listbox', { name: 'Actions' })).toBeVisible()
    await expect(page.getByTestId('chat-tools-button')).toBeVisible()
  })

  test('the draft of Home survives a reload and is gone once sent', async ({ page }) => {
    await server(page)
    await ready(page)
    await page.getByTestId('home-input').fill('half a thought')
    // The draft is written a moment after the last key.
    await page.waitForTimeout(500)
    await page.reload()
    await expect(page.getByTestId('home-input')).toHaveValue('half a thought')
    await page.getByTestId('home-send').click()
    await expect(page).toHaveURL(/\/app\/chats\/n1$/)
    await page.goto('/app')
    await expect(page.getByTestId('home-input')).toHaveValue('')
  })

  test('a message that cannot go removes the new chat and keeps the draft', async ({ page }) => {
    const seen = await server(page, { failSend: true })
    await ready(page)
    await page.getByTestId('home-input').fill('try this')
    await page.getByTestId('chat-file-input').setInputFiles({ name: 'big.bin', mimeType: 'application/octet-stream', buffer: Buffer.alloc(16) })
    await page.getByTestId('home-send').click()
    await expect(page.getByText('the file is too large')).toBeVisible()
    await expect(page).toHaveURL(/\/app$/)
    await expect.poll(() => seen.deleted.length).toBe(1)
    await expect(page.getByTestId('home-input')).toHaveValue('try this')
    await expect(page.getByTestId('home-composer').getByText('big.bin')).toBeVisible()
  })

  test('a file alone can be sent', async ({ page }) => {
    const seen = await server(page)
    await ready(page)
    await expect(page.getByTestId('home-send')).toBeDisabled()
    await page.getByTestId('chat-file-input').setInputFiles({ name: 'q1.csv', mimeType: 'text/csv', buffer: Buffer.from('a,b') })
    await expect(page.getByTestId('home-send')).toBeEnabled()
    await page.getByTestId('home-send').click()
    await expect(page).toHaveURL(/\/app\/chats\/n1$/)
    expect(seen.sent[0].body).toContain('filename="q1.csv"')
  })
})
