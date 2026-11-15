import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { installAgents } from './agents-fixtures.js'

// Memory On or Off: the switch and the save levels of the Memory tab of Your
// settings, and the memory row of the chat Tools menu (a private chat). The API
// is stubbed by a small model of what the server keeps.

const SHOTS = process.env.MEMORY_SHOTS || ''
async function shot(page, name, options = {}) {
  if (!SHOTS) return
  for (const theme of ['light', 'dark']) {
    await page.evaluate((t) => document.documentElement.setAttribute('data-theme', t), theme)
    await page.waitForTimeout(500)
    await page.screenshot({ path: `${SHOTS}/${name}-${theme}-${page.viewportSize().width}.png`, ...options })
  }
  await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'light'))
}

const LIMITS = { index_lines: 40, index_bytes: 2048, profile_bytes: 4096, turn_bytes: 6144 }
const NOTES = { 'Memory/index.md': '- home: lives in Rome -> home.md\n- units: metric, 24-hour clock -> units.md\n', 'Memory/home.md': 'Rome.\n', 'Memory/units.md': 'Metric.\n' }

// The memory slot as the server lists it: the built-in provider, an add-on that
// declares the slot, and Off. `picked` is the stored choice; `down` stops the
// add-on, which turns the slot off without moving it to the built-in provider.
function slotView(s) {
  const failed = s.picked === 'fake-notes' && s.down
  const off = s.picked === 'off' || failed
  return {
    id: 'memory', version: 1, title: 'Memory', description: 'Short notes the assistant reads before it answers and may save.',
    providers: [
      { id: 'builtin', title: 'Built-in', description: 'Plain files in your Memory folder. Nothing leaves this machine.', kind: 'builtin', available: true, selected: s.picked === 'builtin' },
      { id: 'fake-notes', title: 'Fake notes', description: 'Keeps notes in a service.', kind: 'addon', available: !s.down, properties: { remote: true, undo: 'none' }, roles: ['read', 'forget'], selected: s.picked === 'fake-notes' },
      { id: 'off', title: 'Off', kind: 'off', available: true, selected: s.picked === 'off' },
    ],
    resolution: { slot: 'memory', selected: s.picked, stored: true, active: off ? 'off' : s.picked, off,
      ...(failed ? { code: 'provider_unavailable', addon_id: 'fake-notes', addon_title: 'Fake notes' } : {}) },
    ...(s.health ? { health: { cooling_down: true, last: 'timeout', notice: s.health } } : {}),
  }
}

async function serveAccount(page, { enabled = true, save = 'ask', messaging = 'ask', fail = false, picked = 'builtin', down = false, health = '', forget = 200 } = {}) {
  const s = { enabled, save, messaging, puts: [], picks: [], exports: 0, removed: [], picked, down, health, forgets: 0 }
  await mockChat(page)
  await page.route('**/api/chats/providers/memory', (route) => {
    const req = route.request()
    if (req.method() === 'PUT') {
      const body = req.postDataJSON()
      s.picks.push(body.provider)
      s.picked = body.provider
      s.enabled = body.provider !== 'off'
    }
    return route.fulfill({ json: slotView(s) })
  })
  await page.route('**/api/chats/memory', (route) => {
    const req = route.request()
    if (req.method() === 'DELETE') {
      s.forgets++
      if (forget !== 200) return route.fulfill({ status: forget, json: { error: { message: 'this provider has no forget' } } })
      return route.fulfill({ json: { done: true } })
    }
    if (req.method() === 'PUT') {
      const body = req.postDataJSON()
      s.puts.push(body)
      if (fail) return route.fulfill({ status: 503, json: { error: { message: 'the memory settings cannot be stored on this server' } } })
      if (typeof body.enabled === 'boolean') s.enabled = body.enabled
      if (body.save_mode) s.save = body.save_mode
      if (body.messaging_save) s.messaging = body.messaging_save
    }
    return route.fulfill({ json: { enabled: s.enabled, enabled_set: s.puts.length > 0, save_mode: s.save, messaging_save: s.messaging, save_mode_set: false, provider: s.enabled ? s.picked : 'off', available: true } })
  })
  await page.route('**/api/chats/memory/export*', (route) => { s.exports++; return route.fulfill({ status: 200, contentType: 'application/zip', body: 'PK' }) })
  await page.route('**/api/chats/context-files', route => route.fulfill({ json: {
    limits: LIMITS,
    groups: [{ source: 'memory', scope: 'user', max_bytes: 2048, max_lines: 40, files: Object.entries(NOTES).map(([p, t]) => ({ path: p, name: p.split('/').pop(), size: t.length, ...(p.endsWith('index.md') ? { index: true, lines: 2 } : {}) })) }],
  } }))
  await page.route('**/api/chats/files?*', route => route.fulfill({ json: [] }))
  await page.route('**/api/chats/files/content*', (route) => {
    if (route.request().method() === 'DELETE') s.removed.push(1)
    return route.fulfill({ status: 200, body: '' })
  })
  await page.route(/\/api\/chats\/files(\?.*)?$/, (route) => {
    if (route.request().method() === 'DELETE') { s.removed.push(new URL(route.request().url()).searchParams.get('path')); return route.fulfill({ json: { moved: 1 } }) }
    return route.fulfill({ json: [] })
  })
  return s
}

test.describe('Settings, Memory: the switch and how a note is saved', () => {
  test('puts the On or Off switch first and stores a change at once', async ({ page }) => {
    const s = await serveAccount(page)
    await page.goto('/app/account?tab=memory')
    const card = page.getByTestId('memory-switch-card')
    await expect(card).toHaveAttribute('data-state', 'on')
    await expect(card.getByRole('heading', { name: 'Memory is on' })).toBeVisible()
    const sw = page.getByRole('switch', { name: 'Memory is on' })
    await expect(sw).toHaveAttribute('aria-checked', 'true')
    // The switch is above everything else on the page.
    const top = async (id) => (await page.getByTestId(id).boundingBox()).y
    expect(await top('memory-switch-card')).toBeLessThan(await top('memory-save-card'))
    expect(await top('memory-save-card')).toBeLessThan(await top('memory-notes-section'))
    await shot(page, 'settings-memory-on', { fullPage: true })

    await sw.click()
    await expect.poll(() => s.puts).toEqual([{ enabled: false }])
    await expect(card).toHaveAttribute('data-state', 'off')
    await expect(page.getByRole('switch', { name: 'Memory is off' })).toHaveAttribute('aria-checked', 'false')
    await expect(page.getByText('Your notes stay, and you can still read, export and delete them.')).toBeVisible()
    // The notes stay viewable while memory is off.
    await expect(page.getByTestId('memory-group')).toBeVisible()
    await expect(page.getByTestId('memory-export')).toBeEnabled()
    await shot(page, 'settings-memory-off', { fullPage: true })

    await page.getByRole('switch', { name: 'Memory is off' }).click()
    await expect.poll(() => s.puts.at(-1)).toEqual({ enabled: true })
    await expect(card).toHaveAttribute('data-state', 'on')
  })

  test('lists the four save levels, marks the default and stores the one picked', async ({ page }) => {
    const s = await serveAccount(page)
    await page.goto('/app/account?tab=memory')
    const levels = page.getByRole('radiogroup', { name: 'Saving a note' })
    await expect(levels.getByRole('radio')).toHaveCount(4)
    await shot(page, 'memory-settings-default', { fullPage: true })
    await expect(levels.locator('.ms-level__title')).toHaveText(['Save it and tell me', 'Ask me firstDefault', 'Always confirm', 'Never'])
    await expect(levels.getByRole('radio', { name: /^Ask me first/ })).toBeChecked()
    await expect(page.getByTestId('memory-save-ask')).toContainText('Default')
    await levels.getByRole('radio', { name: /^Save it and tell me/ }).click()
    await expect.poll(() => s.puts).toEqual([{ save_mode: 'auto' }])
    await expect(levels.getByRole('radio', { name: /^Save it and tell me/ })).toBeChecked()
    await levels.getByRole('radio', { name: /^Always confirm/ }).click()
    await expect.poll(() => s.puts.at(-1)).toEqual({ save_mode: 'always' })
    await expect(levels.getByRole('radio', { name: /^Always confirm/ })).toBeChecked()
    await expect(page.getByTestId('memory-save-always')).toContainText('nothing can loosen it')
    await levels.getByRole('radio', { name: /Never/ }).click()
    await expect.poll(() => s.puts.at(-1)).toEqual({ save_mode: 'never' })
    await shot(page, 'memory-settings-levels', { fullPage: true })
  })

  test('has an In messaging apps row: Ask first by default, stores the choice, and says when it cannot loosen the level', async ({ page }) => {
    const s = await serveAccount(page)
    await page.goto('/app/account?tab=memory')
    const row = page.getByRole('radiogroup', { name: 'In messaging apps' })
    await expect(row.locator('.ms-level__title')).toHaveText(['Ask me firstDefault', 'Save it for a verified person in a private chat', 'Never'])
    await expect(row.getByRole('radio', { name: /^Ask me first/ })).toBeChecked()
    await expect(page.getByTestId('memory-messaging-paired_private')).toContainText('Never looser than the setting above.')
    await expect(page.getByTestId('memory-messaging-locked')).toHaveCount(0)
    await row.getByRole('radio', { name: /verified person in a private chat/ }).click()
    await expect.poll(() => s.puts).toEqual([{ messaging_save: 'paired_private' }])
    await expect(row.getByRole('radio', { name: /verified person/ })).toBeChecked()
    await row.getByRole('radio', { name: /^Never/ }).click()
    await expect.poll(() => s.puts.at(-1)).toEqual({ messaging_save: 'never' })
    // The levels above and the row are independent controls: picking a level does not touch the row.
    await page.getByRole('radiogroup', { name: 'Saving a note' }).getByRole('radio', { name: /^Always confirm/ }).click()
    await expect.poll(() => s.puts.at(-1)).toEqual({ save_mode: 'always' })
    await expect(page.getByTestId('memory-messaging-locked')).toContainText('Always confirm, so this row cannot save a note at once')
    await page.getByRole('radiogroup', { name: 'Saving a note' }).getByRole('radio', { name: /^Never/ }).click()
    await expect(page.getByTestId('memory-messaging-locked')).toContainText('Your setting above is Never')
    await shot(page, 'memory-settings-messaging', { fullPage: true })
  })

  test('lists the providers from the server and stores the one picked', async ({ page }) => {
    const s = await serveAccount(page)
    await page.goto('/app/account?tab=memory')
    const picker = page.getByTestId('provider-picker-memory')
    const group = picker.getByRole('radiogroup', { name: 'Provider' })
    await expect(group.locator('.ms-level__title')).toHaveText(['Built-in', 'Fake notesRemoteCannot be undone', 'Off'])
    await expect(group.getByRole('radio', { name: /^Built-in/ })).toBeChecked()
    await expect(picker.getByTestId('provider-picker-memory-failed')).toHaveCount(0)
    await group.getByRole('radio', { name: /^Fake notes/ }).click()
    await expect.poll(() => s.picks).toEqual(['fake-notes'])
    await expect(group.getByRole('radio', { name: /^Fake notes/ })).toBeChecked()
    await shot(page, 'settings-memory-provider', { fullPage: true })
    // Off here and the switch above are one answer.
    await group.getByRole('radio', { name: /^Off/ }).click()
    await expect.poll(() => s.picks).toEqual(['fake-notes', 'off'])
    await expect(page.getByTestId('memory-switch-card')).toHaveAttribute('data-state', 'off')
    await page.getByRole('switch', { name: 'Memory is off' }).click()
    await expect.poll(() => s.puts.at(-1)).toEqual({ enabled: true })
    await expect(page.getByTestId('memory-switch-card')).toHaveAttribute('data-state', 'on')
  })

  test('says which add-on stopped, keeps the choice, and offers the built-in provider', async ({ page }) => {
    const s = await serveAccount(page, { picked: 'fake-notes', down: true })
    await page.goto('/app/account?tab=memory')
    const notice = page.getByTestId('provider-picker-memory-failed')
    await expect(notice).toContainText('Fake notes is not available')
    await expect(notice).toContainText('Nothing moved to the built-in provider')
    const group = page.getByTestId('provider-picker-memory').getByRole('radiogroup', { name: 'Provider' })
    await expect(group.getByRole('radio', { name: /^Fake notes/ })).toBeChecked()
    await expect(page.getByTestId('provider-picker-memory-fake-notes')).toContainText('Not available')
    await shot(page, 'settings-memory-provider-down', { fullPage: true })
    s.down = false
    await page.getByTestId('provider-picker-memory-use-builtin').click()
    await expect.poll(() => s.picks).toEqual(['builtin'])
    await expect(notice).toHaveCount(0)
  })

  test('shows the notice of a provider the host leaves alone', async ({ page }) => {
    await serveAccount(page, { picked: 'fake-notes', health: 'Fake notes did not answer three times. LocalAI will try it again in a minute.' })
    await page.goto('/app/account?tab=memory')
    const notice = page.getByTestId('provider-picker-memory-health')
    await expect(notice).toBeVisible()
    await expect(notice).toContainText('The provider is paused')
    await expect(notice).toContainText('will try it again in a minute')
    await shot(page, 'settings-memory-provider-paused', { fullPage: true })
  })

  test('forgets everything from a provider that can, after a confirm dialog', async ({ page }) => {
    const s = await serveAccount(page, { picked: 'fake-notes' })
    await page.goto('/app/account?tab=memory')
    await page.getByTestId('provider-picker-memory-forget-button').click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('Forget everything from Fake notes?')
    await expect(dialog).toContainText('This cannot be undone')
    expect(s.forgets).toBe(0)
    await shot(page, 'settings-memory-provider-forget-dialog')
    await dialog.getByRole('button', { name: 'Cancel' }).click()
    await expect(dialog).toHaveCount(0)
    expect(s.forgets).toBe(0)
    await page.getByTestId('provider-picker-memory-forget-button').click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Forget everything' }).click()
    await expect.poll(() => s.forgets).toBe(1)
    await expect(page.getByRole('alertdialog')).toHaveCount(0)
  })

  test('offers no forget for the built-in provider, Off or a provider that is down', async ({ page }) => {
    await serveAccount(page)
    await page.goto('/app/account?tab=memory')
    await expect(page.getByTestId('provider-picker-memory')).toBeVisible()
    await expect(page.getByTestId('provider-picker-memory-forget')).toHaveCount(0)
    await page.getByTestId('provider-picker-memory').getByRole('radio', { name: /^Off/ }).click()
    await expect(page.getByTestId('memory-switch-card')).toHaveAttribute('data-state', 'off')
    await expect(page.getByTestId('provider-picker-memory-forget')).toHaveCount(0)
  })

  test('a choice the server did not store is not shown as stored', async ({ page }) => {
    const s = await serveAccount(page, { fail: true })
    await page.goto('/app/account?tab=memory')
    await page.getByRole('switch', { name: 'Memory is on' }).click()
    await expect(page.getByTestId('memory-settings-save-error')).toContainText('cannot be stored')
    await expect(page.getByTestId('memory-switch-card')).toHaveAttribute('data-state', 'on')
    expect(s.puts).toEqual([{ enabled: false }])
  })

  test('exports the notes, and forgets everything only after a confirm step', async ({ page }) => {
    const s = await serveAccount(page)
    await page.goto('/app/account?tab=memory')
    const download = page.waitForEvent('download')
    await page.getByTestId('memory-export').click()
    expect((await download).url()).toMatch(/\/api\/chats\/memory\/export$/)
    await page.getByTestId('memory-clear').click()
    await expect(page.getByTestId('memory-clear-confirm')).toBeVisible()
    expect(s.removed).toEqual([])
    await shot(page, 'settings-memory-forget', { fullPage: true })
    await page.getByTestId('memory-clear-yes').click()
    await expect.poll(() => s.removed.length).toBe(3)
  })

  test('has no horizontal scroll and keeps the switch reachable on a phone', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 800 })
    await serveAccount(page)
    await page.goto('/app/account?tab=memory')
    await expect(page.getByRole('switch', { name: 'Memory is on' })).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    await shot(page, 'settings-memory-on', { fullPage: true })
  })
})

const chat = { id: 'c1', title: 'Quotes', model: 'qwen3-8b', kind: 'chat', created: '2026-10-25T10:00:00Z', updated: '2026-10-25T10:00:00Z', running: false, last_turn: '', pending: [] }
const MEMORY_ROW = { group: 'memory', tools: ['context_read'], off: false, usable: true, provider: 'builtin', providers: { context_read: 'builtin' },
  title: 'Memory', description: 'The model can read your notes before it answers and save new ones. Turn it off to make this a private chat: nothing is read from your notes or saved to them. Your other chats are not affected.',
  when_off: 'This is a private chat. The model neither reads nor saves your notes.' }

async function serveChat(page, { user = false, agent = false } = {}) {
  const state = { off: false, puts: [] }
  const memory = () => {
    if (state.off) return { on: false, code: 'memory_off_chat', blocked_by: 'chat', reason: 'Memory is off in this chat: it is a private chat.', fix: { kind: 'open_tools_menu', target: 'memory' } }
    if (user) return { on: false, code: 'memory_off_user', blocked_by: 'user', reason: 'Memory is off in your settings.', fix: { kind: 'open_memory_settings', target: 'memory' } }
    if (agent) return { on: false, code: 'memory_off_agent', blocked_by: 'agent', reason: 'Memory is off for the agent of this chat.' }
    return { on: true }
  }
  const tools = () => ({ addons: [], builtins: [
    { group: 'web', tools: ['web_search'], off: false, usable: true, provider: 'builtin', providers: { web_search: 'builtin' } },
    { ...MEMORY_ROW, off: state.off, usable: !user, reason: user ? 'memory is off in your settings' : undefined },
  ], off: [], servers: [], connectors: [], memory: memory() })
  await mockChat(page)
  await page.route('**/api/chats/c1/events', route => route.abort())
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: [] } }))
  await page.route(/\/api\/chats\/c1(\?.*)?$/, route => route.fulfill({ json: chat }))
  await page.route('**/api/chats/c1/addons', route => route.fulfill({ json: { addons: [] } }))
  await page.route('**/api/chats/c1/tools', route => route.fulfill({ json: tools() }))
  await page.route('**/api/chats/c1/builtins', (route) => {
    const body = route.request().postDataJSON()
    state.puts.push(body)
    state.off = (body.off || []).includes('memory')
    return route.fulfill({ json: tools() })
  })
  return state
}

const open = async (page) => {
  await page.goto('/app/chats/c1')
  await page.getByTestId('chat-tools-button').click()
  await expect(page.getByTestId('chat-tools-menu')).toBeVisible()
}

test.describe('Chat Tools menu: memory and the private chat', () => {
  test('a Memory row with a plain effect line, on by default', async ({ page }) => {
    await serveChat(page)
    await open(page)
    const sw = page.getByRole('switch', { name: 'Memory', exact: true })
    await expect(sw).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByTestId('chat-tools-item').filter({ has: sw })).toContainText('Turn it off to make this a private chat')
    await expect(page.getByTestId('chat-memory-line')).toHaveCount(0)
    await expect(page.getByTestId('chat-memory-chip')).toHaveCount(0)
    await shot(page, 'chat-tools-memory-on')
  })

  test('switching it off makes a private chat, remembered by the server, with a chip and one line', async ({ page }) => {
    const s = await serveChat(page)
    await open(page)
    await page.getByRole('switch', { name: 'Memory', exact: true }).click()
    await expect.poll(() => s.puts).toEqual([{ off: ['memory'] }])
    await expect(page.getByRole('switch', { name: 'Memory', exact: true })).toHaveAttribute('aria-checked', 'false')
    await expect(page.getByTestId('chat-tools-item').filter({ has: page.getByRole('switch', { name: 'Memory', exact: true }) })).toContainText('This is a private chat')
    await expect(page.getByTestId('chat-memory-line')).toContainText('Memory is off here.')
    await expect(page.getByTestId('chat-memory-line')).toContainText('private chat')
    await expect(page.getByTestId('chat-memory-line')).toContainText('Turn it on in the Tools menu')
    await expect(page.getByTestId('chat-memory-chip')).toHaveText('Private chat')
    await shot(page, 'chat-tools-memory-private')
    await page.getByRole('switch', { name: 'Memory', exact: true }).click()
    await expect.poll(() => s.puts.at(-1)).toEqual({ off: [] })
    await expect(page.getByTestId('chat-memory-chip')).toHaveCount(0)
  })

  test('when memory is off in the settings the row cannot be used and the line says where to turn it on', async ({ page }) => {
    await serveChat(page, { user: true })
    await open(page)
    const sw = page.getByRole('switch', { name: 'Memory', exact: true })
    await expect(sw).toBeDisabled()
    await expect(sw).toHaveAccessibleDescription(/Off in your settings/)
    const line = page.getByTestId('chat-memory-line')
    await expect(line).toHaveAttribute('data-code', 'user')
    await expect(line).toContainText('Memory is off here.')
    await expect(line.getByRole('link', { name: 'Settings, Memory' })).toHaveAttribute('href', /\/app\/account\?tab=memory/)
    await expect(page.getByTestId('chat-memory-chip')).toHaveText('Memory off')
    await shot(page, 'chat-tools-memory-user-off')
  })

  test('a chat of an agent that turned memory off says so', async ({ page }) => {
    await serveChat(page, { agent: true })
    await open(page)
    await expect(page.getByTestId('chat-memory-line')).toContainText('The agent of this chat has memory switched off.')
  })

  test('fits a phone: the line, the chip and the switch stay inside the screen', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await serveChat(page, { user: true })
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    await expect(page.getByTestId('chat-memory-line')).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    await shot(page, 'chat-tools-memory-user-off')
  })
})

test.describe('Agent form: the memory switch of the person', () => {
  const memory = (page, enabled) => page.route('**/api/chats/memory', route => route.fulfill({ json: { enabled, enabled_set: true, save_mode: 'ask', save_mode_set: false, provider: 'builtin', available: true } }))

  test('says, read only, that memory is off in the settings of the person', async ({ page }) => {
    await installAgents(page)
    await memory(page, false)
    await page.goto('/app/agents/a2/edit')
    const line = page.getByTestId('agent-memory-off')
    await expect(line).toContainText('Memory is off in your settings')
    await expect(line.getByRole('link', { name: 'Settings, Memory' })).toHaveAttribute('href', /\/app\/account\?tab=memory/)
    await shot(page, 'agent-form-memory-off', { fullPage: true })
  })

  test('says nothing while memory is on', async ({ page }) => {
    await installAgents(page)
    await memory(page, true)
    await page.goto('/app/agents/a2/edit')
    await expect(page.getByTestId('agent-tools-panel')).toBeVisible()
    await expect(page.getByTestId('agent-memory-off')).toHaveCount(0)
  })
})
