import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// The files of a message in the transcript, with the API stubbed: the words
// in the bubble, images as thumbnails, other files as chips, a file that is
// gone as a muted chip, and an old message whose text holds the list of files
// the model got. The list and its paths are never on the page.

// A 1x1 PNG.
const PNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==', 'base64')
const FOLDER = '/Chats/2026-10-30 ci sei?'

const photo = (name, extra = {}) => ({ path: `${FOLDER}/${name}`, name, size: 48213, mime: 'image/jpeg', version: 1, ...extra })

async function chatWith(page, messages, { files = [], missing = [] } = {}) {
  const detail = {
    id: 'c1', title: 'ci sei?', model: 'qwen3-8b', kind: 'chat', running: false, last_turn: 'done',
    created: '2026-10-30T10:00:00Z', updated: '2026-10-30T10:00:00Z', pending: [],
  }
  await page.route('**/api/chats/c1/events', route => route.abort())
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages } }))
  await page.route('**/api/chats/c1/files', route => route.fulfill({
    json: { files, folder: `/workspace/home${FOLDER}/`, folder_path: FOLDER },
  }))
  await page.route('**/api/chats/files/content?*', route => {
    const path = new URL(route.request().url()).searchParams.get('path')
    if (missing.includes(path)) return route.fulfill({ status: 404, json: { error: 'not found' } })
    return route.fulfill({ status: 200, contentType: 'image/png', body: PNG })
  })
  await page.route('**/api/chats/c1', route => route.fulfill({ json: detail }))
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({
    json: { conversations: [{ ...detail, pending: undefined }], persistent: true, files: true },
  }))
}

const user = (seq, text, attachments) => ({ seq, turn: seq, role: 'user', text, at: '2026-10-30T10:00:00Z', ...(attachments ? { attachments } : {}) })
const answer = (seq, text) => ({ seq, turn: seq - 1, role: 'assistant', text, at: '2026-10-30T10:00:05Z' })

test.describe('Chat attachments', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
  })

  test('a photo and its question show as a thumbnail and the words', async ({ page }) => {
    await chatWith(page, [user(1, 'whats in this photo', [photo('photo_1_y.jpg')]), answer(2, 'A small logo.')])
    await page.goto('/app/chats/c1')

    const msg = page.getByTestId('chat-message').first()
    await expect(msg.getByTestId('message-content')).toHaveText('whats in this photo')
    const img = msg.getByTestId('attachment-image').locator('img')
    await expect(img).toHaveAttribute('alt', 'photo_1_y.jpg')
    await expect(img).toHaveAttribute('src', /\/api\/chats\/files\/content\?path=%2FChats%2F2026-10-30\+ci\+sei%3F%2Fphoto_1_y\.jpg&inline=1$/)
    await expect.poll(() => img.evaluate(el => el.naturalWidth)).toBeGreaterThan(0)
    await expect(page.getByTestId('chat-thread')).not.toContainText('The user attached')
    await expect(page.getByTestId('chat-thread')).not.toContainText('/workspace/home')
  })

  test('a message with only files has chips and no empty bubble', async ({ page }) => {
    await chatWith(page, [user(1, '', [
      { path: `${FOLDER}/q1.csv`, name: 'q1.csv', size: 2048, mime: 'text/csv' },
      { path: `${FOLDER}/report.pdf`, name: 'report.pdf', size: 3 * 1024 * 1024, mime: 'application/pdf' },
    ])])
    await page.goto('/app/chats/c1')

    const msg = page.getByTestId('chat-message').first()
    await expect(msg.getByTestId('message-content')).toHaveCount(0)
    const chips = msg.getByTestId('attachment')
    await expect(chips).toHaveCount(2)
    await expect(chips.nth(0)).toContainText('q1.csv')
    await expect(chips.nth(0)).toContainText('2.0 KB')
    await expect(chips.nth(1)).toContainText('3.0 MB')
    await expect(chips.nth(1).getByRole('link', { name: 'Download report.pdf' }))
      .toHaveAttribute('href', /\/api\/chats\/files\/content\?path=%2FChats%2F2026-10-30\+ci\+sei%3F%2Freport\.pdf$/)
  })

  test('an old message with the list in its text shows only the words and the photo', async ({ page }) => {
    await chatWith(page, [user(1, 'whats in this photo\n\nThe user attached these files:\n- /workspace/home/Chats/2026-10-30 ci sei?/photo_5985526360434217157_y.jpg')])
    await page.goto('/app/chats/c1')

    const msg = page.getByTestId('chat-message').first()
    await expect(msg.getByTestId('message-content')).toHaveText('whats in this photo')
    await expect(msg.getByTestId('attachment-image').locator('img')).toHaveAttribute('alt', 'photo_5985526360434217157_y.jpg')
    await expect(page.getByTestId('chat-thread')).not.toContainText('The user attached')
    await expect(page.getByTestId('chat-thread')).not.toContainText('/workspace/home')
  })

  test('a file that is gone is a muted chip, not a broken image', async ({ page }) => {
    const gone = photo('gone.jpg')
    await chatWith(page, [
      user(1, 'marked by the server', [{ ...gone, missing: true }]),
      user(2, 'deleted since', [photo('late.jpg')]),
    ], { missing: [`${FOLDER}/late.jpg`] })
    await page.goto('/app/chats/c1')

    for (const [i, name] of [[0, 'gone.jpg'], [1, 'late.jpg']]) {
      const msg = page.getByTestId('chat-message').nth(i)
      const chip = msg.getByTestId('attachment')
      await expect(chip).toHaveAttribute('data-missing', 'true')
      await expect(chip).toContainText(name)
      await expect(chip).toContainText('File no longer available')
      await expect(chip.getByRole('link')).toHaveCount(0)
      await expect(msg.locator('img')).toHaveCount(0)
    }
  })

  test('several images show four tiles at most, and +N shows the rest', async ({ page }) => {
    await chatWith(page, [user(1, 'compare', [1, 2, 3, 4, 5, 6].map(n => photo(`p${n}.jpg`)))])
    await page.goto('/app/chats/c1')

    const msg = page.getByTestId('chat-message').first()
    await expect(msg.getByTestId('attachment-image')).toHaveCount(3)
    const more = msg.getByTestId('attachment-more')
    await expect(more).toHaveText('+3')
    await expect(more).toHaveAccessibleName('Show 3 more')
    await more.click()
    await expect(msg.getByTestId('attachment-image')).toHaveCount(6)
    await expect(more).toHaveCount(0)
  })

  test('the keyboard opens an image in the files pane', async ({ page }) => {
    const a = photo('photo_1_y.jpg')
    await chatWith(page, [user(1, 'whats in this photo', [a])], {
      files: [{ name: a.name, path: a.path, guest_path: `/workspace/home${a.path}`, size: a.size, mime: a.mime, origin: 'user', turn: 1, at: '2026-10-30T10:00:00Z' }],
    })
    await page.goto('/app/chats/c1')

    const tile = page.getByRole('button', { name: 'Open photo_1_y.jpg' })
    await tile.focus()
    await page.keyboard.press('Enter')
    const pane = page.getByTestId('chat-files-pane')
    await expect(pane).toBeVisible()
    await expect(pane.getByTestId('file-preview')).toContainText('photo_1_y.jpg')
  })

  test('a file chip opens the files pane at its file', async ({ page }) => {
    const a = { path: `${FOLDER}/q1.csv`, name: 'q1.csv', size: 2048, mime: 'text/csv' }
    await chatWith(page, [user(1, 'sum it', [a])], {
      files: [{ ...a, guest_path: `/workspace/home${a.path}`, origin: 'user', turn: 1, at: '2026-10-30T10:00:00Z' }],
    })
    await page.goto('/app/chats/c1')

    await page.getByRole('button', { name: 'Open q1.csv' }).click()
    await expect(page.getByTestId('chat-files-pane').getByTestId('file-preview')).toContainText('q1.csv')
  })

  test('copy and edit take the words only', async ({ page }) => {
    await chatWith(page, [user(1, 'whats in this photo\n\nThe user attached these files:\n- /workspace/home/Chats/x/a.jpg')])
    await page.goto('/app/chats/c1')

    const msg = page.getByTestId('chat-message').first()
    await msg.hover()
    await msg.getByRole('button', { name: 'Edit message' }).click()
    await expect(msg.getByRole('textbox', { name: 'Edit message' })).toHaveValue('whats in this photo')
  })

  test('the composer shows the files it will send with the same chip', async ({ page }) => {
    await chatWith(page, [user(1, 'hi')])
    await page.goto('/app/chats/c1')

    await page.getByTestId('chat-file-input').setInputFiles([
      { name: 'shot.png', mimeType: 'image/png', buffer: PNG },
      { name: 'notes.txt', mimeType: 'text/plain', buffer: Buffer.from('alpha') },
    ])
    const staged = page.getByTestId('chat-composer').getByTestId('attachment')
    await expect(staged).toHaveCount(2)
    await expect(staged.nth(0)).toHaveClass(/cx-att/)
    await expect(staged.nth(0).locator('img')).toHaveAttribute('src', /^blob:/)
    await expect(staged.nth(1)).toContainText('notes.txt')
    await expect(staged.nth(1)).toContainText('5 B')
    await staged.nth(1).getByRole('button', { name: /notes\.txt/ }).click()
    await expect(staged).toHaveCount(1)
  })
})
