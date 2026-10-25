import { test, expect } from './coverage-fixtures.js'
import { chatOf, mockChat, openChat, answerWith, PNG } from './chat-fixtures.js'

// "Save attachments to collection": with a collection chosen in the chat
// settings, every original attachment is also uploaded to that collection,
// next to the unchanged inline chat request.

const sheet = (page) => page.getByTestId('chat-settings')
const STORE_KEY = 'localai_chat_attachment_collection'
const PNG_BYTES = Buffer.from(PNG.split(',')[1], 'base64')

async function mockCollections(page, { names = ['docs', 'notes'], uploadStatus = 200 } = {}) {
  const uploads = []
  let listCalls = 0
  await page.route('**/api/agents/collections', route => {
    listCalls++
    route.fulfill({ json: { collections: names, count: names.length } })
  })
  await page.route('**/api/agents/collections/*/upload*', route => {
    const req = route.request()
    const body = req.postDataBuffer()?.toString('latin1') || ''
    const name = /filename="([^"]+)"/.exec(body)?.[1]
    uploads.push({ url: req.url(), name, body })
    if (uploadStatus === 200) route.fulfill({ json: { status: 'ok', filename: name, key: name } })
    else route.fulfill({ status: uploadStatus, json: { error: 'embedding model unavailable' } })
  })
  return { uploads, listCalls: () => listCalls }
}

async function attachText(page, name = 'notes.txt', text = 'The launch is on Tuesday') {
  await page.locator('input[type=file][accept*="pdf"]').setInputFiles({ name, mimeType: 'text/plain', buffer: Buffer.from(text) })
  await expect(page.locator('.home-file-tag', { hasText: name })).toBeVisible()
}

async function send(page, text = 'Summarize') {
  await page.getByTestId('chat-input').fill(text)
  await page.getByTestId('chat-send').click()
}

const chats = [chatOf('c1', 'Attach', 'qwen3-8b', [])]

test.describe('Chat - save attachments to a collection', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
  })

  test('default is none: the chat sends inline and nothing is uploaded', async ({ page }) => {
    const { uploads } = await mockCollections(page)
    const answer = await answerWith(page, 'Tuesday.')
    await openChat(page, chats)

    await page.getByTestId('chat-settings-button').click()
    const select = sheet(page).getByLabel('Save attachments to collection')
    await expect(select).toHaveValue('')
    await expect(select.locator('option')).toHaveText(['None', 'docs', 'notes'])
    await page.keyboard.press('Escape')

    await attachText(page)
    await send(page)
    await expect(page.getByText('Tuesday.')).toBeVisible()
    expect(JSON.stringify(answer.request())).toContain('The launch is on Tuesday')
    expect(uploads).toHaveLength(0)
    await expect(page.getByTestId('chat-attachment-uploads')).toHaveCount(0)
  })

  test('with a collection chosen, each original is uploaded once and the chat is unchanged', async ({ page }) => {
    const { uploads } = await mockCollections(page)
    const answer = await answerWith(page, 'Got both.')
    await openChat(page, chats)

    await page.getByTestId('chat-settings-button').click()
    await sheet(page).getByLabel('Save attachments to collection').selectOption('docs')
    await expect.poll(() => page.evaluate(k => localStorage.getItem(k), STORE_KEY)).toBe('docs')
    await page.keyboard.press('Escape')

    await attachText(page)
    await page.locator('input[type=file][accept="image/*"]').setInputFiles({ name: 'chart.png', mimeType: 'image/png', buffer: PNG_BYTES })
    await expect(page.locator('.home-file-tag', { hasText: 'chart.png' })).toBeVisible()
    await send(page)

    await expect(page.getByText('Got both.')).toBeVisible()
    const body = JSON.stringify(answer.request())
    expect(body).toContain('The launch is on Tuesday')
    expect(body).toContain('data:image/png;base64,')

    const status = page.getByTestId('chat-attachment-upload')
    await expect(status).toHaveCount(2)
    await expect(status.filter({ hasText: 'notes.txt' })).toContainText('saved to docs')
    await expect(status.filter({ hasText: 'chart.png' })).toContainText('saved to docs')

    expect(uploads.map(u => u.name).sort()).toEqual(['chart.png', 'notes.txt'])
    expect(uploads.every(u => u.url.includes('/api/agents/collections/docs/upload'))).toBe(true)
    // The original bytes, not the inlined base64 or extracted text.
    expect(uploads.find(u => u.name === 'notes.txt').body).toContain('The launch is on Tuesday')
    expect(uploads.find(u => u.name === 'chart.png').body).toContain(PNG_BYTES.toString('latin1'))

    // Re-renders (streaming, a reload of the thread) must not upload again.
    await page.getByTestId('chat-input').fill('typing re-renders the page')
    await page.waitForTimeout(300)
    expect(uploads).toHaveLength(2)

    await page.getByRole('button', { name: 'Dismiss upload status' }).click()
    await expect(page.getByTestId('chat-attachment-uploads')).toHaveCount(0)
  })

  test('a failed upload shows the reason and does not affect the reply', async ({ page }) => {
    await page.addInitScript(([k]) => localStorage.setItem(k, 'docs'), [STORE_KEY])
    const { uploads } = await mockCollections(page, { uploadStatus: 500 })
    await answerWith(page, 'Still answered.')
    await openChat(page, chats)

    await attachText(page)
    await send(page)

    await expect(page.getByText('Still answered.')).toBeVisible()
    await expect(page.getByTestId('chat-attachment-upload')).toContainText('failed: embedding model unavailable')
    expect(uploads).toHaveLength(1)
  })

  test('Home auto-send keeps its upload while the collection list is pending', async ({ page }) => {
    await page.addInitScript(k => {
      localStorage.setItem(k, 'docs')
      localStorage.setItem('localai_index_chat_data', JSON.stringify({
        message: 'Summarize this', model: 'qwen3-8b', newChat: true,
        files: [{ name: 'notes.txt', type: 'text/plain', base64: btoa('original bytes'), textContent: 'original bytes' }],
      }))
    }, STORE_KEY)
    const { uploads } = await mockCollections(page)
    let release
    const ready = new Promise(resolve => { release = resolve })
    await page.route('**/api/agents/collections', async route => {
      await ready
      await route.fulfill({ json: { collections: ['docs'] } })
    })
    await answerWith(page, 'Answered before upload.')
    await openChat(page, chats)
    await expect(page.getByText('Answered before upload.')).toBeVisible()
    expect(uploads).toHaveLength(0)
    release()
    await expect(page.getByTestId('chat-attachment-upload')).toContainText('saved to docs')
    expect(uploads).toHaveLength(1)
    expect(uploads[0].body).toContain('original bytes')
  })

  test('a pasted image is saved under the name the composer gave it', async ({ page }) => {
    await page.addInitScript(([k]) => localStorage.setItem(k, 'docs'), [STORE_KEY])
    const { uploads } = await mockCollections(page)
    await answerWith(page, 'Seen.')
    await openChat(page, chats)
    await page.getByTestId('chat-input').focus()
    await page.getByTestId('chat-input').evaluate((el, b64) => {
      const bytes = Uint8Array.from(atob(b64), c => c.charCodeAt(0))
      const dt = new DataTransfer()
      dt.items.add(new File([bytes], 'image.png', { type: 'image/png' }))
      el.dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true }))
    }, PNG.split(',')[1])
    await expect(page.locator('.home-file-tag', { hasText: 'pasted-image-1.png' })).toBeVisible()
    await send(page, 'What is this?')
    await expect(page.getByText('Seen.')).toBeVisible()
    await expect(page.getByTestId('chat-attachment-upload')).toContainText('saved to docs')
    expect(uploads.map(u => u.name)).toEqual(['pasted-image-1.png'])
  })

  test('a remembered collection that no longer exists is treated as none', async ({ page }) => {
    await page.addInitScript(([k]) => localStorage.setItem(k, 'deleted'), [STORE_KEY])
    const { uploads } = await mockCollections(page, { names: ['docs'] })
    await answerWith(page, 'Fine.')
    await openChat(page, chats)

    await attachText(page)
    await send(page)
    await expect(page.getByText('Fine.')).toBeVisible()
    expect(uploads).toHaveLength(0)
  })

  test('opening and closing the settings sheet does not reload the collections list', async ({ page }) => {
    const collections = await mockCollections(page)
    await openChat(page, chats)
    for (let i = 0; i < 3; i++) {
      await page.getByTestId('chat-settings-button').click()
      await expect(sheet(page).getByLabel('Save attachments to collection').locator('option')).toHaveText(['None', 'docs', 'notes'])
      await page.keyboard.press('Escape')
      await expect(sheet(page)).toHaveCount(0)
    }
    expect(collections.listCalls()).toBe(1)
  })

  test('hidden when the agent pool is off', async ({ page }) => {
    await mockChat(page, { features: { distributed: false, localai_assistant: true, agents: false, mcp: true } })
    const collections = await mockCollections(page)
    await openChat(page, chats)
    await page.getByTestId('chat-settings-button').click()
    await expect(sheet(page)).toBeVisible()
    await expect(sheet(page).getByTestId('chat-attachment-collection')).toHaveCount(0)
    expect(collections.listCalls()).toBe(0)
  })

  test('hidden when the collections list cannot be read', async ({ page }) => {
    await page.route('**/api/agents/collections', route => route.fulfill({ status: 403, json: { error: 'forbidden' } }))
    await openChat(page, chats)
    await page.getByTestId('chat-settings-button').click()
    await expect(sheet(page)).toBeVisible()
    await expect(sheet(page).getByTestId('chat-attachment-collection')).toHaveCount(0)
  })
})
