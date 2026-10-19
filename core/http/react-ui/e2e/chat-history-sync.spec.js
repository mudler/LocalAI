import { test, expect } from './coverage-fixtures.js'
import { mockChat, chatOf, pair, answerWith, CHATS_KEY } from './chat-fixtures.js'

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

  let down = false

  await page.route(/\/api\/conversations(\/[^?]*)?(\?.*)?$/, async (route) => {
    if (down) return route.abort('internetdisconnected')
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
    if (method === 'DELETE' && !id) {
      for (const liveId of live.keys()) tombs.set(liveId, { id: liveId, version: 9, deletedAt: T0 })
      live.clear()
      return route.fulfill({ json: { status: 'ok' } })
    }
    return route.fulfill({ status: 404, json: { error: 'not found' } })
  })

  return {
    release: () => release(),
    setDown: (value) => { down = value },
    liveIds: () => [...live.keys()].sort(),
    saves: (id) => requests.filter(r => r.method === 'PUT' && (id === undefined || r.id === id)),
    deletes: (id) => requests.filter(r => r.method === 'DELETE' && r.id === id),
    deleteAlls: () => requests.filter(r => r.method === 'DELETE' && !r.id),
    bulk: () => requests.filter(r => r.id === 'bulk'),
  }
}

// Signs the browser in as `who()`, read on every request so a spec can sign
// one account out and another in on the same browser.
async function signedInAs(page, who) {
  await page.route('**/api/auth/status', route => route.fulfill({
    json: { authEnabled: true, staticApiKeyRequired: false, providers: ['local'], hasUsers: true, user: who() },
  }))
}

const ALICE = { id: 'u-alice', name: 'Alice', email: 'alice@example.com', role: 'admin', permissions: {} }
const BOB = { id: 'u-bob', name: 'Bob', email: 'bob@example.com', role: 'admin', permissions: {} }

// Each account has its own browser cache (src/utils/chatStore.js).
const keyOf = (user) => `${CHATS_KEY}:${user.id}`

const storedChats = (page, user = ALICE) =>
  page.evaluate((key) => JSON.parse(localStorage.getItem(key) || '{}'), keyOf(user))

// Open Chat signed in as Alice with these conversations in her cache.
async function openChat(page, chats, active = chats[0]?.id) {
  await page.addInitScript(([key, data]) => {
    if (!localStorage.getItem(key)) localStorage.setItem(key, JSON.stringify(data))
  }, [keyOf(ALICE), { chats, activeChatId: active, lastSaved: Date.now() }])
  await page.goto('/app/chat')
  await page.getByTestId('chat-composer').waitFor()
}

// The server half per account: the session decides whose conversations a
// request reads and writes. Saves for an account in `failSaves` answer 500,
// as a first save that does not get through.
async function fakeAccountServer(page, who, { failSaves = [] } = {}) {
  const accounts = new Map()
  const failing = new Set(failSaves)
  const requests = []
  const of = (userId) => {
    if (!accounts.has(userId)) accounts.set(userId, new Map())
    return accounts.get(userId)
  }
  await page.route(/\/api\/conversations(\/[^?]*)?(\?.*)?$/, async (route) => {
    const req = route.request()
    const user = who().id
    const live = of(user)
    const path = new URL(req.url()).pathname
    const rest = path.slice(path.indexOf('/api/conversations') + '/api/conversations'.length)
    const id = rest.startsWith('/') ? decodeURIComponent(rest.slice(1)) : null
    const method = req.method()
    const body = method === 'PUT' ? req.postDataJSON() : null
    requests.push({ user, method, id, body })
    if (method === 'GET' && !id) return route.fulfill({ json: { conversations: [...live.values()], deleted: [] } })
    if (method === 'PUT' && id) {
      if (failing.has(user)) return route.fulfill({ status: 500, json: { error: 'database is locked' } })
      const saved = { ...body, id, version: (live.get(id)?.version || 0) + 1 }
      live.set(id, saved)
      return route.fulfill({ json: saved })
    }
    return route.fulfill({ status: 404, json: { error: 'not found' } })
  })
  return {
    recover: (userId) => failing.delete(userId),
    saves: (userId) => requests.filter(r => r.user === userId && r.method === 'PUT'),
    liveTexts: (userId) => [...of(userId).values()].map(c => JSON.stringify(c.history)),
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
    await signedInAs(page, () => ALICE)
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

test.describe('Chat history belongs to the signed-in account', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
  })

  test('a draft one account could not save is not uploaded into the next account on this browser', async ({ page }) => {
    let who = ALICE
    await signedInAs(page, () => who)
    const server = await fakeAccountServer(page, () => who, { failSaves: [ALICE.id] })
    await answerWith(page, 'Answer meant for Alice')

    await page.goto('/app/chat')
    await page.getByTestId('chat-composer').waitFor()
    await page.getByTestId('chat-input').fill('Alice private question')
    await page.getByTestId('chat-input').press('Enter')
    await expect(page.getByText('Answer meant for Alice')).toBeVisible()
    // The first save of Alice's chat does not get through.
    await expect.poll(() => server.saves(ALICE.id).some(r => JSON.stringify(r.body).includes('Alice private question'))).toBe(true)

    // Alice signs out, Bob signs in on the same browser. His list is empty.
    who = BOB
    await page.goto('/app/chat')
    await page.getByTestId('chat-composer').waitFor()

    // Bob's first save pass is the one Alice's draft must not be part of.
    await expect.poll(() => server.saves(BOB.id).length).toBeGreaterThan(0)
    await page.waitForTimeout(1_000)
    expect(server.saves(BOB.id).filter(r => JSON.stringify(r.body).includes('Alice private'))).toHaveLength(0)
    expect(server.liveTexts(BOB.id).join('\n')).not.toContain('Alice private')
    await openMenu(page)
    await expect(page.getByTestId('chats-row').filter({ hasText: 'Alice private question' })).toHaveCount(0)
  })

  test('the same account still gets its unsaved draft back and uploads it', async ({ page }) => {
    let who = ALICE
    await signedInAs(page, () => who)
    const server = await fakeAccountServer(page, () => who, { failSaves: [ALICE.id] })
    await answerWith(page, 'Answer meant for Alice')

    await page.goto('/app/chat')
    await page.getByTestId('chat-composer').waitFor()
    await page.getByTestId('chat-input').fill('Alice private question')
    await page.getByTestId('chat-input').press('Enter')
    await expect(page.getByText('Answer meant for Alice')).toBeVisible()
    await expect.poll(() => server.saves(ALICE.id).length).toBeGreaterThan(0)

    // Bob uses the browser in between; then Alice signs in again and the
    // server takes writes again.
    who = BOB
    await page.goto('/app/chat')
    await page.getByTestId('chat-composer').waitFor()
    await expect.poll(() => server.saves(BOB.id).length).toBeGreaterThan(0)

    who = ALICE
    server.recover(ALICE.id)
    await page.goto('/app/chat')
    await page.getByTestId('chat-composer').waitFor()
    await expect.poll(() => server.liveTexts(ALICE.id).join('\n')).toContain('Alice private question')
    await openMenu(page)
    await expect(page.getByTestId('chats-row').filter({ hasText: 'Alice private question' })).toHaveCount(1)
  })
})

test.describe('Deletes stay pending until the server confirms them', () => {
  const remote = () => [
    chatOf('a', 'Keep me', 'qwen3-8b', pair('q', 'a'), { version: 1 }, T0),
    chatOf('b', 'Delete me', 'qwen3-8b', pair('q', 'b'), { version: 1 }, T0 - 1_000),
  ]

  test.beforeEach(async ({ page }) => {
    await mockChat(page)
    await signedInAs(page, () => ALICE)
  })

  // Opens Chat with nothing in the browser and waits until the list has been
  // reconciled: the empty chat Chat starts with is uploaded only after that.
  async function openSynced(page, server) {
    await page.goto('/app/chat')
    await page.getByTestId('chat-composer').waitFor()
    await expect.poll(() => server.saves().length).toBeGreaterThan(0)
    await openMenu(page)
    await expect(page.getByTestId('chats-row').filter({ hasText: 'Delete me' })).toHaveCount(1)
  }

  async function deleteRow(page, name) {
    const row = page.getByTestId('chats-row').filter({ hasText: name })
    await row.hover()
    await row.getByTitle('Delete chat').click()
    const toast = page.getByTestId('chat-undo-toast')
    await toast.getByRole('button', { name: 'Dismiss' }).click()
    await expect(toast).toHaveCount(0)
  }

  async function reopen(page) {
    await page.goto('/app/chat')
    await page.getByTestId('chat-composer').waitFor()
    await openMenu(page)
  }

  test('a delete made while the server is unreachable is sent later and the chat does not come back', async ({ page, context }) => {
    const server = await fakeHistoryServer(page, { conversations: remote() })
    await openSynced(page, server)

    server.setDown(true)
    await context.setOffline(true)
    await deleteRow(page, 'Delete me')
    await expect(page.getByTestId('chats-row').filter({ hasText: 'Delete me' })).toHaveCount(0)
    await page.waitForTimeout(500)

    server.setDown(false)
    await context.setOffline(false)
    await reopen(page)
    await expect.poll(() => server.liveIds().filter(id => id === 'a' || id === 'b')).toEqual(['a'])
    await expect(page.getByTestId('chats-row').filter({ hasText: 'Keep me' })).toHaveCount(1)
    await expect(page.getByTestId('chats-row').filter({ hasText: 'Delete me' })).toHaveCount(0)
  })

  test('"clear all" made while the server is unreachable is sent later and nothing comes back', async ({ page, context }) => {
    const server = await fakeHistoryServer(page, { conversations: remote() })
    await openSynced(page, server)

    server.setDown(true)
    await context.setOffline(true)
    await page.getByRole('button', { name: 'Clear all' }).click()
    await page.getByRole('button', { name: 'Delete all' }).click()
    await page.waitForTimeout(500)

    server.setDown(false)
    await context.setOffline(false)
    await reopen(page)
    await expect.poll(() => server.liveIds().filter(id => id === 'a' || id === 'b')).toEqual([])
    await expect(page.getByTestId('chats-row').filter({ hasText: 'Keep me' })).toHaveCount(0)
    await expect(page.getByTestId('chats-row').filter({ hasText: 'Delete me' })).toHaveCount(0)
  })

  test('clear all persists before navigation can cancel the debounced save', async ({ page }) => {
    const server = await fakeHistoryServer(page)
    server.setDown(true)
    // Two chats: with a single chat the menu offers no Clear all.
    await openChat(page, [
      chatOf('draft', 'Unsynced draft', 'qwen3-8b', pair('private draft', 'answer'), {}, T0),
      chatOf('other', 'Other draft', 'qwen3-8b', pair('second draft', 'answer'), {}, T0 - 1000),
    ])
    await openMenu(page)
    // Freeze the debounce: assert the cache produced by the delete itself.
    await page.clock.install()
    await page.clock.pauseAt(new Date())
    await page.getByRole('button', { name: 'Clear all' }).click()
    await page.getByRole('button', { name: 'Delete all' }).click()
    const stored = await storedChats(page)
    expect(stored.chats.map(c => c.id)).not.toContain('draft')
    expect(stored.chats.map(c => c.id)).not.toContain('other')
    expect(stored.pendingDeletes).toEqual(['*'])

    await page.clock.resume()
    server.setDown(false)
    await reopen(page)
    await expect.poll(() => server.deleteAlls().length).toBe(1)
    await page.waitForTimeout(1_000)
    expect(server.saves('draft')).toHaveLength(0)
    expect(server.saves('other')).toHaveLength(0)
    await expect(page.getByTestId('chats-row').filter({ hasText: 'Unsynced draft' })).toHaveCount(0)
  })

  test('a delete the server confirmed is not sent again and does not come back', async ({ page }) => {
    const server = await fakeHistoryServer(page, { conversations: remote() })
    await openSynced(page, server)

    await deleteRow(page, 'Delete me')
    await expect.poll(() => server.deletes('b').length).toBe(1)
    expect(server.liveIds()).not.toContain('b')
    // The server has the request; wait until the page has its answer too.
    // Reopening earlier leaves the delete unconfirmed, and it is rightly sent again.
    await expect.poll(async () => (await storedChats(page)).pendingDeletes).toEqual([])

    await reopen(page)
    await expect(page.getByTestId('chats-row').filter({ hasText: 'Keep me' })).toHaveCount(1)
    await expect(page.getByTestId('chats-row').filter({ hasText: 'Delete me' })).toHaveCount(0)
    await page.waitForTimeout(1_000)
    expect(server.deletes('b')).toHaveLength(1)
  })
})

test.describe('Chat history from Home and without an account', () => {
  test('a delete on the Home page reaches the server through Chat', async ({ page }) => {
    await mockChat(page)
    await signedInAs(page, () => ALICE)
    const server = await fakeHistoryServer(page, {
      conversations: [
        chatOf('a', 'Keep me', 'qwen3-8b', pair('q', 'kept answer'), { version: 1 }, T0),
        chatOf('b', 'Delete me', 'qwen3-8b', pair('q', 'deleted answer'), { version: 1 }, T0 - 1_000),
      ],
    })
    await page.addInitScript(([key, data]) => {
      if (!localStorage.getItem(key)) localStorage.setItem(key, JSON.stringify(data))
    }, [keyOf(ALICE), {
      chats: [
        chatOf('a', 'Keep me', 'qwen3-8b', pair('q', 'kept answer'), { version: 1 }, Date.now()),
        chatOf('b', 'Delete me', 'qwen3-8b', pair('q', 'deleted answer'), { version: 1 }, Date.now() - 1_000),
      ],
      activeChatId: 'a',
    }])

    await page.goto('/app')
    const row = page.getByTestId('home-conversation').filter({ hasText: 'Delete me' })
    await row.hover()
    await row.getByRole('button', { name: /delete conversation/i }).click()
    await expect(page.getByTestId('home-undo-toast')).toBeVisible()
    // Resuming another chat makes the delete final and opens Chat.
    await page.getByTestId('home-conversation').filter({ hasText: 'Keep me' }).locator('.home-row__open').click()
    await expect(page).toHaveURL(/\/app\/chat/)

    await expect.poll(() => server.deletes('b').length).toBe(1)
    await openMenu(page)
    await expect(page.getByTestId('chats-row').filter({ hasText: 'Keep me' })).toHaveCount(1)
    await expect(page.getByTestId('chats-row').filter({ hasText: 'Delete me' })).toHaveCount(0)
  })

  test('nobody signed in: chats stay in the browser and nothing is sent', async ({ page }) => {
    await mockChat(page)
    const server = await fakeHistoryServer(page, {})
    await page.addInitScript(([key, data]) => {
      if (!localStorage.getItem(key)) localStorage.setItem(key, JSON.stringify(data))
    }, [CHATS_KEY, { chats: [chatOf('local', 'Browser only', 'qwen3-8b', pair('q', 'a'), {}, T0)], activeChatId: 'local' }])
    await page.goto('/app/chat')
    await page.getByTestId('chat-composer').waitFor()
    await openMenu(page)
    await expect(page.getByTestId('chats-row').filter({ hasText: 'Browser only' })).toHaveCount(1)
    await page.waitForTimeout(1_000)
    expect(server.saves()).toHaveLength(0)
  })
})
