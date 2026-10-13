import { test, expect } from './coverage-fixtures.js'
import { mockChat, openChat, storedChats, chatOf, pair, answerWith } from './chat-fixtures.js'

// Server-side chat history (see docs/content/features/chat-history.md). The
// server keeps a tombstone for every deleted conversation and refuses to
// write it again; these specs pin the client half: a stale browser must drop
// what the server deleted, and must not lose what it typed while the list
// was loading.

const T0 = new Date('2026-10-09T12:00:00Z').getTime()

// A stand-in for /api/conversations with the server's contract: tombstones
// in the list, 410 for a write of a deleted id, versions on every write.
// `hiddenDeleted` are tombstones the list does not report yet (deleted on
// another device after this tab loaded the list). `holdList` keeps the list
// request pending until release() is called.
async function fakeHistoryServer(page, { conversations = [], deleted = [], hiddenDeleted = [], holdList = false } = {}) {
  const live = new Map(conversations.map(c => [c.id, c]))
  const tombs = new Map(deleted.map(id => [id, { id, version: 9, deletedAt: T0 }]))
  const hidden = new Set(hiddenDeleted)
  const requests = []
  let release = () => {}
  const listGate = holdList ? new Promise(resolve => { release = resolve }) : Promise.resolve()

  await page.route(/\/api\/conversations(\/[^?]*)?(\?.*)?$/, async (route) => {
    const req = route.request()
    const path = new URL(req.url()).pathname
    const rest = path.slice(path.indexOf('/api/conversations') + '/api/conversations'.length)
    const id = rest.startsWith('/') ? decodeURIComponent(rest.slice(1)) : null
    const method = req.method()
    const body = method === 'PUT' || method === 'POST' ? req.postDataJSON() : null
    requests.push({ method, id, body })

    if (method === 'GET' && !id) {
      await listGate
      return route.fulfill({ json: { conversations: [...live.values()], deleted: [...tombs.values()] } })
    }
    if (method === 'PUT' && id && id !== 'bulk') {
      if (tombs.has(id) || hidden.has(id)) {
        return route.fulfill({ status: 410, json: { error: 'conversation was deleted' } })
      }
      const saved = { ...body, id, version: (live.get(id)?.version || 0) + 1 }
      live.set(id, saved)
      return route.fulfill({ json: saved })
    }
    if (method === 'DELETE' && id) {
      live.delete(id)
      tombs.set(id, { id, version: 9, deletedAt: T0 })
      return route.fulfill({ json: { status: 'ok' } })
    }
    return route.fulfill({ status: 404, json: { error: 'not found' } })
  })

  return {
    release: () => release(),
    saves: (id) => requests.filter(r => r.method === 'PUT' && r.id === id),
    deletes: (id) => requests.filter(r => r.method === 'DELETE' && r.id === id),
    bulk: () => requests.filter(r => r.id === 'bulk'),
  }
}

const storedIds = async (page) => (await storedChats(page)).chats.map(c => c.id)

async function openMenu(page) {
  await page.keyboard.press('Control+k')
  await expect(page.getByTestId('chats-menu')).toBeVisible()
}

test.describe('Chat history sync with the server', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
  })

  test('a stale client drops a chat deleted on another device instead of uploading it again', async ({ page }) => {
    const server = await fakeHistoryServer(page, {
      conversations: [chatOf('kept', 'Kept on the server', 'qwen3-8b', pair('q', 'a'), { version: 1 }, T0)],
      deleted: ['gone'],
    })
    await openChat(page, [
      chatOf('gone', 'Deleted on the laptop', 'qwen3-8b', pair('old q', 'old a'), { version: 2 }, T0 + 5_000),
      chatOf('kept', 'Kept on the server', 'qwen3-8b', pair('q', 'a'), { version: 1 }, T0),
      chatOf('draft', 'Never synced draft', 'qwen3-8b', pair('draft q', 'draft a'), {}, T0 - 5_000),
    ], 'gone')

    // The draft is uploaded: that is the save pass the deleted chat must not be in.
    await expect.poll(() => server.saves('draft').length).toBe(1)
    expect(server.saves('gone')).toHaveLength(0)
    await expect.poll(() => storedIds(page)).toEqual(['kept', 'draft'])

    await openMenu(page)
    await expect(page.getByTestId('chats-row').filter({ hasText: 'Deleted on the laptop' })).toHaveCount(0)
    await expect(page.getByTestId('chats-row').filter({ hasText: 'Never synced draft' })).toHaveCount(1)
  })

  test('a synced chat the server no longer has is dropped, not resurrected', async ({ page }) => {
    // Another account signed in on this browser, or an admin purge: the chat
    // has a version, so it was on the server, and the server does not list it.
    const server = await fakeHistoryServer(page, { conversations: [] })
    await openChat(page, [
      chatOf('synced', 'Synced earlier', 'qwen3-8b', pair('q', 'a'), { version: 3 }, T0),
      chatOf('draft', 'Fresh draft', 'qwen3-8b', pair('q', 'a'), {}, T0 - 1_000),
    ])
    await expect.poll(() => server.saves('draft').length).toBe(1)
    expect(server.saves('synced')).toHaveLength(0)
    // An empty server list is no reason to replace the server's set with
    // this browser's cache.
    expect(server.bulk()).toHaveLength(0)
    await expect.poll(() => storedIds(page)).toEqual(['draft'])
  })

  test('a 410 on save drops the chat locally', async ({ page }) => {
    // Deleted on another device after this tab loaded the list.
    const server = await fakeHistoryServer(page, { hiddenDeleted: ['late'] })
    await openChat(page, [
      chatOf('late', 'Deleted meanwhile', 'qwen3-8b', pair('q', 'a'), {}, T0),
      chatOf('other', 'Still here', 'qwen3-8b', pair('q', 'a'), {}, T0 - 1_000),
    ])
    await expect.poll(() => server.saves('late').length).toBe(1)
    await expect.poll(() => storedIds(page)).toEqual(['other'])
    // Dropped, not retried.
    await page.waitForTimeout(1_000)
    expect(server.saves('late')).toHaveLength(1)
  })

  test('a message sent while the list is loading survives the older server copy', async ({ page }) => {
    const server = await fakeHistoryServer(page, {
      holdList: true,
      conversations: [chatOf('x', 'Release notes', 'qwen3-8b', pair('first question', 'first answer'), { version: 4 }, T0)],
    })
    await answerWith(page, 'Answer written during the probe')
    await openChat(page, [chatOf('x', 'Release notes', 'qwen3-8b', pair('first question', 'first answer'), { version: 4 }, T0)])

    await page.getByTestId('chat-input').fill('Question typed while loading')
    await page.getByTestId('chat-input').press('Enter')
    await expect(page.getByText('Answer written during the probe')).toBeVisible()

    server.release()
    // The newer local copy wins and is uploaded with the new turn in it.
    await expect.poll(() => server.saves('x').map(r => JSON.stringify(r.body.history)).join('\n'))
      .toContain('Question typed while loading')
    await expect(page.getByText('Question typed while loading')).toBeVisible()
    await expect(page.getByText('Answer written during the probe')).toBeVisible()
  })

  test('a newer copy from the server replaces the older local one', async ({ page }) => {
    const remote = chatOf('x', 'Release notes', 'qwen3-8b',
      [...pair('first question', 'first answer'), ...pair('asked on the phone', 'answered on the phone')], { version: 5 }, T0 + 60_000)
    const server = await fakeHistoryServer(page, { conversations: [remote] })
    await openChat(page, [chatOf('x', 'Release notes', 'qwen3-8b', pair('first question', 'first answer'), { version: 4 }, T0)])

    await expect(page.getByText('answered on the phone')).toBeVisible()
    // Adopting the server copy is not a change to send back.
    await page.waitForTimeout(1_000)
    expect(server.saves('x')).toHaveLength(0)
  })

  test('a delete made while the list is loading reaches the server and wins', async ({ page }) => {
    const server = await fakeHistoryServer(page, {
      holdList: true,
      conversations: [
        chatOf('a', 'Keep me', 'qwen3-8b', pair('q', 'a'), { version: 1 }, T0),
        chatOf('b', 'Delete me', 'qwen3-8b', pair('q', 'b'), { version: 1 }, T0 - 1_000),
      ],
    })
    await openChat(page, [
      chatOf('a', 'Keep me', 'qwen3-8b', pair('q', 'a'), { version: 1 }, T0),
      chatOf('b', 'Delete me', 'qwen3-8b', pair('q', 'b'), { version: 1 }, T0 - 1_000),
    ])

    await openMenu(page)
    const row = page.getByTestId('chats-row').filter({ hasText: 'Delete me' })
    await row.hover()
    await row.getByTitle('Delete chat').click()
    const toast = page.getByTestId('chat-undo-toast')
    await toast.getByRole('button', { name: 'Dismiss' }).click()
    await expect(toast).toHaveCount(0)

    server.release()
    await expect.poll(() => server.deletes('b').length).toBe(1)
    // The server still listed it when the answer left; the local delete wins.
    await expect.poll(() => storedIds(page)).toEqual(['a'])
    await expect(page.getByTestId('chats-row').filter({ hasText: 'Delete me' })).toHaveCount(0)
    expect(server.saves('b')).toHaveLength(0)
  })

  test('the LocalAI Assistant toggle survives the trip to the server and back', async ({ page }) => {
    const server = await fakeHistoryServer(page, {
      conversations: [chatOf('remote', 'Assistant on the phone', 'qwen3-8b', pair('q', 'a'), { version: 1, localaiAssistant: true }, T0)],
    })
    await openChat(page, [chatOf('local', 'Assistant draft', 'qwen3-8b', pair('q', 'a'), { localaiAssistant: true }, T0 + 1_000)])

    await expect.poll(() => server.saves('local').length).toBe(1)
    expect(server.saves('local')[0].body.localaiAssistant).toBe(true)
    await expect.poll(async () => (await storedChats(page)).chats.map(c => [c.id, !!c.localaiAssistant]))
      .toEqual([['local', true], ['remote', true]])
  })
})
