import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { mockSettings } from './settings-fixtures.js'
import { app, installedAddon, mockAddons } from './addons-fixtures.js'

// The network of a chat's commands: the Network row of the Tools menu,
// the card a blocked host asks with, and the network-policy host control on
// the Settings tab of the add-on that runs commands. The control is drawn for
// any add-on the apps list entitles to it: the specs use the Sandbox and a
// second add-on, Devbox, with the same manifest line. The API is stubbed.

// An add-on that runs commands, with the host controls the apps list gives it.
const runner = (id, title, controls = ['network-policy', 'tool-providers']) => app({
  id, name: id, title, description: 'Runs commands.', mainPage: undefined, shadows: ['bash'], tools: ['bash'], toolCount: 1,
  ui: { entry: `/api/addons/${id}/assets/${'a'.repeat(64)}/ui.js`, sdk: { min: 2, max: 2 }, controls },
})
const RUNNERS = [runner('sandbox', 'Sandbox'), runner('devbox', 'Devbox', ['network-policy'])]

const SHOTS = process.env.EGRESS_SHOT_DIR
const shot = async (page, name) => {
  if (!SHOTS) return
  for (const theme of ['light', 'dark']) {
    await page.evaluate(t => document.documentElement.setAttribute('data-theme', t), theme)
    await page.waitForTimeout(400)
    await page.screenshot({ path: `${SHOTS}/${name}-${theme}.png` })
  }
}

const DETAIL = {
  id: 'c1', title: 'Packages', model: 'qwen3-8b', kind: 'chat', created: '2026-10-30T10:00:00Z', updated: '2026-10-30T10:00:00Z',
  running: false, last_turn: 'done', pending: [],
}
const sandbox = { addons: [{ id: 'sandbox', tools: 2, off: false }], commands: 'available', commands_provider: 'sandbox', commands_isolation: 'isolated', execution_status: { readiness: 'available', isolation: 'isolated' } }
const view = (over = {}) => ({
  preset: 'off', ceiling: 'registries', effective: 'off', applies: true, applies_reason: '', addon: 'sandbox', network: { title: 'Sandbox', presets: [] },
  registries: [{ id: 'pypi', title: 'Python (PyPI)', hosts: ['pypi.org', 'files.pythonhosted.org'] }], allowed_hosts: [], rules: [], cards: [], ...over,
})

async function stubChat(page, { egress, running = false, messages = [] }) {
  const detail = { ...DETAIL, running }
  await mockAddons(page, { apps: RUNNERS, installed: RUNNERS.map(a => installedAddon({ id: a.id, title: a.title })) })
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages } }))
  await page.route('**/api/chats/c1', route => route.fulfill({ json: detail }))
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({
    json: { conversations: [{ ...detail, pending: undefined }], persistent: true, files: true, files_reason: '' },
  }))
  await page.route('**/api/chats/c1/addons', route => route.fulfill({ json: sandbox }))
  const state = { egress, puts: [], answers: [], deletes: [] }
  await page.route('**/api/chats/c1/egress', route => {
    if (route.request().method() === 'PUT') {
      const { preset } = route.request().postDataJSON()
      state.puts.push(preset)
      state.egress = { ...state.egress, preset, effective: preset }
    }
    return route.fulfill({ json: state.egress })
  })
  await page.route('**/api/chats/c1/egress/rules/**', route => {
    state.deletes.push(route.request().url().split('/egress/rules/')[1])
    return route.fulfill({ status: 204, body: '' })
  })
  await page.route('**/api/chats/c1/egress/consent', route => {
    state.answers.push(route.request().postDataJSON())
    if (state.consent) return state.consent(route)
    return route.fulfill({ status: 204, body: '' })
  })
  const queue = []
  let wake = null
  await page.route('**/api/chats/c1/events', async (route) => {
    while (queue.length === 0) await new Promise((resolve) => { wake = resolve })
    const body = queue.splice(0).join('')
    await route.fulfill({ status: 200, headers: { 'Content-Type': 'text/event-stream' }, body }).catch(() => {})
  })
  const push = (...events) => {
    for (const ev of events) queue.push(`event: ${ev.kind}\ndata: ${JSON.stringify(ev)}\n\n`)
    wake?.()
  }
  return { state, push }
}

test.describe('Network of the commands', () => {
  test.beforeEach(async ({ page }) => { await mockChat(page) })

  test('the Tools menu has a Network row up to the limit, and a panel with the rules', async ({ page }) => {
    const rules = [{ host: 'example.com', port: 443, allow: true, at: '2026-10-30T10:00:00Z', above_limit: true }]
    const { state } = await stubChat(page, { egress: view({ rules }) })
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    const row = page.getByTestId('chat-network')
    await expect(row).toContainText('Network')
    await expect(page.getByTestId('chat-network-all')).toBeDisabled()
    await expect(page.getByTestId('chat-network-all')).toHaveAttribute('title', 'Limited by your administrator')
    await expect(row).toContainText('Your administrator allows at most: Package registries.')
    await page.getByTestId('chat-network-registries').click()
    await expect.poll(() => state.puts).toEqual(['registries'])
    await expect(page.getByTestId('chat-network-registries')).toHaveAttribute('aria-checked', 'true')
    await page.getByTestId('chat-network-panel-toggle').click()
    const rule = page.getByTestId('chat-network-rule')
    await expect(rule).toContainText('example.com:443')
    await expect(rule).toContainText('above the limit, does not apply')
    await shot(page, 'network-row')
    await rule.getByRole('button', { name: 'Remove the rule of example.com:443' }).click()
    await expect.poll(() => state.deletes).toEqual(['example.com/443'])
    await expect(rule).toHaveCount(0)
  })

  test('the highlighted choice is what applies when the chat asks for more than the limit', async ({ page }) => {
    await stubChat(page, { egress: view({ preset: 'all', ceiling: 'off', effective: 'off' }) })
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    await expect(page.getByTestId('chat-network-off')).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByTestId('chat-network-all')).toHaveAttribute('aria-checked', 'false')
    await expect(page.getByTestId('chat-network-asks')).toHaveText('This chat asks for All public sites; the administrator limit is Off.')
    await expect(page.getByTestId('chat-network-limit-note')).toBeVisible()
    await shot(page, 'network-over-limit-off')
  })

  test('with the limit on registries a chat that asks for all shows registries', async ({ page }) => {
    await stubChat(page, { egress: view({ preset: 'all', ceiling: 'registries', effective: 'registries' }) })
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    await expect(page.getByTestId('chat-network-registries')).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByTestId('chat-network-all')).toHaveAttribute('aria-checked', 'false')
    await expect(page.getByTestId('chat-network-asks')).toContainText('the administrator limit is Package registries')
    await shot(page, 'network-over-limit-registries')
  })

  test('the panel lists the hosts the administrator always allows, read only', async ({ page }) => {
    await stubChat(page, { egress: view({ preset: 'registries', effective: 'registries', allowed_hosts: ['api.example.org', 'git.example.org:8443'] }) })
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    await expect(page.getByTestId('chat-network-allowed-note')).toHaveText('2 hosts are always allowed by your administrator.')
    await page.getByTestId('chat-network-panel-toggle').click()
    const list = page.getByTestId('chat-network-allowed')
    await expect(list).toContainText('Always allowed by your administrator:')
    await expect(list.getByTestId('chat-network-allowed-host')).toHaveText(['api.example.org', 'git.example.org:8443'])
    await expect(list.getByRole('button')).toHaveCount(0)
    await shot(page, 'network-allowed-hosts')
  })

  test('with the network off the panel says the hosts apply once it is on', async ({ page }) => {
    await stubChat(page, { egress: view({ allowed_hosts: ['api.example.org'] }) })
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    await expect(page.getByTestId('chat-network-allowed-note')).toHaveCount(0)
    await page.getByTestId('chat-network-panel-toggle').click()
    await expect(page.getByTestId('chat-network-allowed')).toContainText('They apply when the network is on.')
  })

  test('where commands run on this machine, the row says they use its network', async ({ page }) => {
    await stubChat(page, { egress: view({ applies: false, applies_reason: 'host' }) })
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    await expect(page.getByTestId('chat-network-unenforced')).toContainText('Commands run on this machine and use its network.')
    await expect(page.getByTestId('chat-network')).toHaveCount(0)
  })

  test('a runner that does not report enforcement shows the not-enforced line and no switcher', async ({ page }) => {
    // What the server sends when the isolated provider's status has no
    // network-policy enforcement report.
    await stubChat(page, { egress: view({ applies: false, applies_reason: 'none' }) })
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    await expect(page.getByTestId('chat-network-unenforced')).toContainText('Network-policy enforcement is not verified')
    await expect(page.getByTestId('chat-network')).toHaveCount(0)
    await shot(page, 'network-not-enforced')
  })

  test('with no add-on that draws the network controls, the Tools menu has no Network row', async ({ page }) => {
    await stubChat(page, { egress: view({ addon: undefined }) })
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    await expect(page.getByText('Tools for this chat')).toBeVisible()
    await expect(page.getByTestId('chat-network')).toHaveCount(0)
    await expect(page.getByTestId('chat-network-unenforced')).toHaveCount(0)
  })

  test('a second add-on with the same control draws the same row, and its link opens that add-on', async ({ page }) => {
    await stubChat(page, { egress: view({ ceiling: 'off', addon: 'devbox' }) })
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    await expect(page.getByTestId('chat-network')).toContainText('Off: the administrator has not allowed network access.')
    await expect(page.getByTestId('chat-network-limit-admin')).toContainText('An administrator can turn it on in the settings of Devbox.')
    const link = page.getByTestId('chat-network-limit-admin-link')
    await expect(link).toHaveText('Open the settings of Devbox')
    await expect(link).toHaveAttribute('href', '/app/addons?detail=devbox&view=settings')
  })

  test('a blocked host asks on a card that comes back after a reload and can be answered', async ({ page }) => {
    const card = { id: 'k1', host: 'pypi.org', port: 443, kind: 'connect', state: 'pending', asked_at: '2026-10-30T10:01:00Z' }
    const { state, push } = await stubChat(page, { egress: view({ cards: [card] }), running: true })
    await page.goto('/app/chats/c1')
    const consent = page.getByTestId('chat-network-consent')
    await expect(consent.getByTestId('chat-network-consent-host')).toHaveText('pypi.org:443', { timeout: 15_000 })
    await expect(consent).toContainText('This chat: Off. Limit set by your administrator: Package registries.')
    await shot(page, 'network-card')
    await page.reload()
    await expect(consent.getByTestId('chat-network-consent-host')).toHaveText('pypi.org:443', { timeout: 15_000 })
    await consent.getByTestId('chat-network-consent-chat').click()
    await expect.poll(() => state.answers).toEqual([{ id: 'k1', decision: 'chat' }])
    // The answer comes back as an event: the card collapses to one line under its tool line.
    state.egress = view({ cards: [{ ...card, state: 'chat', tool_seq: 4, answered_at: '2026-10-30T10:02:00Z', answered_by: 'admin' }] })
    push(
      { kind: 'egress_card', ids: ['k1'] },
    )
    await expect(consent).toHaveCount(0)
  })

  test('an answered card is a line under the bash call that asked', async ({ page }) => {
    const card = { id: 'k2', host: 'files.pythonhosted.org', port: 443, kind: 'connect', state: 'once', tool_seq: 2, asked_at: '2026-10-30T10:01:00Z', answered_at: '2026-10-30T10:01:30Z', answered_by: 'admin' }
    const messages = [
      { seq: 1, turn: 1, role: 'user', text: 'install requests', at: '2026-10-30T10:00:00Z' },
      { seq: 2, turn: 1, role: 'tool', text: 'bash: pip install requests\nSuccessfully installed requests', at: '2026-10-30T10:01:40Z', card: { kind: 'provider', id: 'sandbox' } },
      { seq: 3, turn: 1, role: 'assistant', text: 'Done.', at: '2026-10-30T10:01:50Z' },
    ]
    await stubChat(page, { egress: view({ cards: [card] }), messages })
    await page.goto('/app/chats/c1')
    const line = page.getByTestId('chat-network-card-line')
    await expect(line).toContainText('files.pythonhosted.org:443', { timeout: 15_000 })
    await expect(line).toContainText('Allowed once by admin')
  })

  test('a card that expired with its turn can still be answered, under its call and in the network panel', async ({ page }) => {
    const card = { id: 'k3', host: 'pypi.org', port: 443, kind: 'connect', state: 'expired', tool_seq: 2, asked_at: '2026-10-30T10:01:00Z' }
    const messages = [
      { seq: 1, turn: 1, role: 'user', text: 'install requests', at: '2026-10-30T10:00:00Z' },
      { seq: 2, turn: 1, role: 'tool', text: 'bash: pip install requests\nconnection refused', at: '2026-10-30T10:01:40Z', card: { kind: 'provider', id: 'sandbox' } },
      { seq: 3, turn: 1, role: 'assistant', text: 'The network is off.', at: '2026-10-30T10:01:50Z' },
    ]
    const { state } = await stubChat(page, { egress: view({ preset: 'registries', effective: 'registries', cards: [card] }), messages })
    await page.goto('/app/chats/c1')
    const line = page.getByTestId('chat-network-card-line')
    await expect(line).toContainText('Not answered', { timeout: 15_000 })
    await page.getByTestId('chat-network-late-chat').click()
    await expect.poll(() => state.answers).toEqual([{ id: 'k3', decision: 'chat' }])

    await page.getByTestId('chat-tools-button').click()
    await page.getByTestId('chat-network-panel-toggle').click()
    await page.getByTestId('chat-network-card-k3-deny').click()
    await expect.poll(() => state.answers).toEqual([{ id: 'k3', decision: 'chat' }, { id: 'k3', decision: 'deny' }])
    await shot(page, 'network-card-expired')
  })
})

// A second add-on, with another ID and title than the shipped Sandbox, that
// declares capabilities.network. The host draws the same row and card from its
// declaration: its title in the text and its titles for the presets.
test.describe('The network of a second add-on that declares it', () => {
  test.beforeEach(async ({ page }) => { await mockChat(page) })
  const declared = { title: 'Runbox', presets: [{ id: 'off', title: 'Closed' }, { id: 'all', title: 'Open web' }] }
  const card = { id: 'k9', host: 'example.com', port: 443, kind: 'connect', state: 'pending', asked_at: '2026-10-30T10:01:00Z' }
  const runbox = (over = {}) => view({ addon: 'devbox', network: declared, ceiling: 'all', ...over })

  test('the Tools menu names the preset buttons from the declaration', async ({ page }) => {
    await stubChat(page, { egress: runbox() })
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    await expect(page.getByTestId('chat-network-off')).toHaveText('Closed')
    await expect(page.getByTestId('chat-network-all')).toHaveText('Open web')
    await expect(page.getByTestId('chat-network-registries')).toHaveText('Package registries')
    await expect(page.getByTestId('chat-network')).toContainText('Commands reach no network.')
    await expect(page.getByTestId('chat-network')).not.toContainText('Sandbox')
  })

  test('a blocked host explains the limit without naming another add-on', async ({ page }) => {
    await stubChat(page, { egress: runbox({ ceiling: 'off', cards: [{ ...card, above_ceiling: true }] }), running: true })
    await page.goto('/app/chats/c1')
    const blocked = page.getByTestId('chat-network-blocked')
    await expect(blocked).toContainText('Network access is off for commands on this server.', { timeout: 15_000 })
    await expect(blocked).not.toContainText('Sandbox')
  })

  test('the consent card asks with the preset titles of the add-on', async ({ page }) => {
    await stubChat(page, { egress: runbox({ preset: 'off', cards: [card] }), running: true })
    await page.goto('/app/chats/c1')
    await expect(page.getByTestId('chat-network-consent')).toContainText('This chat: Closed. Limit set by your administrator: Open web.', { timeout: 15_000 })
    await expect(page.getByTestId('chat-network-consent-once')).toBeVisible()
  })
})

test.describe('A card the administrator\'s limit cannot admit', () => {
  test.beforeEach(async ({ page }) => { await mockChat(page) })
  const card = { id: 'k9', host: 'example.com', port: 443, kind: 'connect', state: 'pending', asked_at: '2026-10-30T10:01:00Z' }

  test('with the limit off the card explains instead of offering buttons that cannot work, and the row says why', async ({ page }) => {
    await stubChat(page, { egress: view({ ceiling: 'off', cards: [{ ...card, above_ceiling: true }] }), running: true })
    await page.goto('/app/chats/c1')
    const blocked = page.getByTestId('chat-network-blocked')
    await expect(blocked).toContainText('Network access is off for commands on this server.', { timeout: 15_000 })
    await expect(blocked.getByTestId('chat-network-consent-host')).toHaveText('example.com:443')
    await expect(blocked.getByTestId('chat-network-blocked-admin')).toContainText('An administrator can turn it on in the settings of Sandbox.')
    await expect(blocked.getByTestId('chat-network-blocked-admin-link')).toHaveAttribute('href', '/app/addons?detail=sandbox&view=settings')
    await expect(page.getByTestId('chat-network-consent')).toHaveCount(0)
    await expect(page.getByTestId('chat-network-consent-once')).toHaveCount(0)
    await shot(page, 'network-card-ceiling-off-dark')
    if (SHOTS) {
      await page.evaluate(() => { localStorage.setItem('localai-theme', 'light'); document.documentElement.setAttribute('data-theme', 'light') })
      await shot(page, 'network-card-ceiling-off-light')
      await page.evaluate(() => { localStorage.setItem('localai-theme', 'dark'); document.documentElement.setAttribute('data-theme', 'dark') })
    }
    await page.getByTestId('chat-tools-button').click()
    await expect(page.getByTestId('chat-network')).toContainText('Off: the administrator has not allowed network access.')
    await expect(page.getByTestId('chat-network-limit-admin-link')).toHaveAttribute('href', '/app/addons?detail=sandbox&view=settings')
    await expect(page.getByTestId('chat-network-all')).toBeDisabled()
  })

  test('a double click sends one answer, and a refusal by the limit swaps the card for the explanation', async ({ page }) => {
    const { state } = await stubChat(page, { egress: view({ ceiling: 'registries', cards: [card] }), running: true })
    state.consent = async (route) => {
      state.egress = view({ ceiling: 'off', cards: [{ ...card, state: 'refused', above_ceiling: true }] })
      await new Promise(r => setTimeout(r, 300))
      return route.fulfill({ status: 400, json: { error: { code: 400, type: 'egress_above_ceiling', message: 'the limit an administrator set for the network of commands does not admit this host' } } })
    }
    await page.goto('/app/chats/c1')
    const once = page.getByTestId('chat-network-consent-once')
    await expect(once).toBeVisible({ timeout: 15_000 })
    await once.dblclick()
    await expect(page.getByTestId('chat-network-consent-chat')).toBeDisabled()
    await expect(page.getByTestId('chat-network-consent-deny')).toBeDisabled()
    await expect.poll(() => state.answers.length).toBe(1)
    // The failure reads the card again: the buttons give way to the refusal line.
    await expect(page.getByTestId('chat-network-card-line')).toContainText("Refused: the server's network limit does not allow it")
    await expect(page.getByTestId('chat-network-consent-once')).toHaveCount(0)
    expect(state.answers).toHaveLength(1)
  })
})

// The add-on pages of the network-policy control, with the settings and the
// admin network routes stubbed.
async function stubNetworkAdmin(page, { settings = {}, egress = {}, events = [], apps = RUNNERS } = {}) {
  await mockSettings(page, settings)
  // The network policy is the owner's: the add-on page reads and writes it
  // through host-settings-read and host-settings-update, not /api/settings.
  const state = await mockAddons(page, { apps, installed: apps.map(a => installedAddon({ id: a.id, title: a.title })), hostSettings: settings })
  await page.route('**/api/chats/admin/tools', route => route.fulfill({ json: { tools: [
    { id: 'bash', label: 'bash', description: 'Run commands', setting: 'chats_host_exec', mode: 'off', provider: 'sandbox', candidates: ['devbox', 'sandbox'], titles: { sandbox: 'Sandbox', devbox: 'Devbox' } },
    { id: 'web_search', label: 'web_search', description: 'Search the web', setting: 'chats_web_search', provider: 'builtin', candidates: [] },
  ], skills: [] } }))
  await page.route('**/api/chats/admin/egress', route => route.fulfill({
    json: { ceiling: 'registries', default: 'off', allow_private: false, ports: [80, 443], extra_registries: [], allowed_hosts: [], audit_days: 30, registries: [], sandbox: 'sandbox', network: { title: 'Sandbox', presets: [] }, isolation: 'isolated', allowed_24h: 0, blocked_24h: 0, ...egress },
  }))
  await page.route(/\/api\/chats\/admin\/egress\/events(\?.*)?$/, route => route.fulfill({ json: { events } }))
  return state
}

test.describe('The network of the commands on the add-on page', () => {
  test('the old address of the network block opens the Settings tab of the add-on that draws it', async ({ page }) => {
    await stubNetworkAdmin(page)
    await page.goto('/app/settings?group=tools#settings-egress')
    await expect(page).toHaveURL(/\/app\/addons\?detail=sandbox&view=settings$/)
    await expect(page.getByTestId('host-control-network-policy')).toBeVisible({ timeout: 15_000 })
  })

  test('the Settings page keeps the built-in tools and a pointer, and none of the network settings', async ({ page }) => {
    await stubNetworkAdmin(page)
    await page.goto('/app/settings?group=tools')
    await expect(page.getByTestId('settings-tools')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('settings-egress')).toHaveCount(0)
    await expect(page.locator('[data-field="chats_egress_ceiling"]')).toHaveCount(0)
    await expect(page.locator('[data-field="chats_egress_allowed_hosts"]')).toHaveCount(0)
    // The retention also keeps the audit of the commands on this machine.
    await expect(page.locator('[data-field="chats_egress_audit_days"]')).toHaveCount(1)
    const rows = page.getByTestId('settings-network-addon')
    await expect(rows).toHaveCount(2)
    await expect(rows.first()).toContainText('Sandbox keeps the network settings of the commands it runs on its own page.')
    await expect(rows.nth(1).getByTestId('settings-network-addon-link')).toHaveAttribute('href', '/app/addons?detail=devbox&view=settings')
    // The bash row points to the add-on that serves it and draws its row.
    await expect(page.getByTestId('settings-tools-bash-addon-link')).toHaveAttribute('href', '/app/addons?detail=sandbox&view=settings')
    // A search finds no network setting here either.
    await page.getByRole('searchbox').first().fill('Always allowed hosts')
    await expect(page.getByTestId('settings-results-title')).toHaveText('No settings found')
  })

  test('without an add-on that draws it, the old address opens the Tools group with no network block', async ({ page }) => {
    await stubNetworkAdmin(page, { apps: [app({ id: 'notes', title: 'Notes' })] })
    await page.goto('/app/settings?group=tools#settings-egress')
    await expect(page).toHaveURL(/\/app\/settings\?group=tools#settings-egress$/)
    await expect(page.getByTestId('settings-tools')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('settings-network-elsewhere')).toHaveCount(0)
  })

  test('shows who runs commands, the registries and the audit, and turns the private switch on only with the phrase', async ({ page }) => {
    const settings = await stubNetworkAdmin(page, {
      settings: { chats_egress_allow_private: false, chats_egress_ceiling: 'off' },
      egress: { registries: [{ id: 'pypi', title: 'Python (PyPI)', hosts: ['pypi.org', 'files.pythonhosted.org'] }], allowed_24h: 4, blocked_24h: 1 },
      events: [{ user: 'u1', user_name: 'Sam', chat: 'c1', host: 'evil.example', port: 443, kind: 'connect', verdict: 'block', reason: 'off', at: '2026-10-30T10:00:00Z' }],
    })
    await page.goto('/app/addons?detail=sandbox&view=settings')
    const block = page.getByTestId('host-control-network-policy')
    await expect(block).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('settings-egress-serves')).toHaveText('Commands run isolated in the Sandbox add-on')
    await expect(page.getByTestId('settings-egress-counts')).toHaveText('Last 24 hours: 4 allowed, 1 blocked.')
    await expect(block.locator('[data-field="chats_egress_ceiling"]')).toBeVisible()
    // The audit retention is a runtime setting of this host, on the Settings page.
    await expect(block.locator('[data-field="chats_egress_audit_days"]')).toHaveCount(0)
    await page.getByTestId('settings-egress-registries').locator('summary').click()
    await expect(block).toContainText('pypi.org, files.pythonhosted.org')
    await page.getByTestId('settings-egress-audit-toggle').click()
    await expect(page.getByTestId('settings-egress-audit')).toContainText('evil.example:443')
    // The bash row of the tool-providers control is on the same tab; the
    // web_search row is not, since the add-on does not replace it.
    await expect(page.getByTestId('settings-tools-row-bash')).toBeVisible()
    await expect(page.getByTestId('settings-tools-row-web_search')).toHaveCount(0)
    await shot(page, 'addon-network')

    await page.getByTestId('settings-egress-private-switch').click()
    await expect(page.getByRole('button', { name: 'Apply' })).toBeDisabled()
    expect(settings.log.dispatch.filter(r => r.action === 'host-settings-update')).toHaveLength(0)
    await page.getByTestId('settings-egress-private-phrase').fill('I understand that commands can reach my private network')
    await page.getByRole('button', { name: 'Apply' }).click()
    // The owner checks the phrase too, so the patch carries it.
    const phrase = 'I understand that commands can reach my private network'
    await expect.poll(() => settings.log.hostSettings).toEqual([{ chats_egress_allow_private: true, chats_egress_private_confirm: phrase }])
    const writes = settings.log.dispatch.filter(r => r.action === 'host-settings-update')
    expect(writes).toHaveLength(1)
    expect(writes[0].operationId).toBeTruthy()
    expect(writes[0].arguments).toEqual({ expectedRevision: 1, patch: { chats_egress_allow_private: true, chats_egress_private_confirm: phrase } })
    await expect(page.getByTestId('settings-pending')).toHaveCount(0)
  })

  test('a second add-on with the same manifest line gets the same block, and only the controls it has', async ({ page }) => {
    const settings = await stubNetworkAdmin(page, { settings: { chats_egress_ceiling: 'off' } })
    await page.goto('/app/addons?detail=devbox&view=settings')
    const block = page.getByTestId('host-control-network-policy')
    await expect(block).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('host-control-tool-providers')).toHaveCount(0)
    await block.locator('[data-field="chats_egress_ceiling"] select').selectOption('registries')
    await page.getByRole('button', { name: 'Apply' }).click()
    await expect.poll(() => settings.log.hostSettings).toEqual([{ chats_egress_ceiling: 'registries' }])
  })

  test('the Always allowed hosts list adds, refuses and removes hosts and saves them with Apply', async ({ page }) => {
    const settings = await stubNetworkAdmin(page, {
      settings: { chats_egress_allow_private: false, chats_egress_ceiling: 'registries', chats_egress_allowed_hosts: 'api.example.org' },
      egress: { allowed_hosts: ['api.example.org'] },
    })
    await page.goto('/app/addons?detail=sandbox&view=settings')
    const editor = page.getByTestId('settings-hosts')
    await expect(editor.getByTestId('settings-hosts-item')).toHaveText(['api.example.org'])
    await expect(editor.getByTestId('settings-hosts-count')).toHaveText('1/64')
    const input = editor.getByTestId('settings-hosts-input')

    // *.example.com and * are accepted patterns; a star inside a name is not.
    await input.fill('api.*.example.com')
    await input.press('Enter')
    await expect(editor.getByTestId('settings-hosts-error')).toContainText('not a pattern')
    await input.fill('https://example.com')
    await input.press('Enter')
    await expect(editor.getByTestId('settings-hosts-error')).toContainText('not a host name')
    await input.fill('10.0.0.5')
    await input.press('Enter')
    await expect(editor.getByTestId('settings-hosts-error')).toContainText('private network switch')
    await input.fill('API.example.org')
    await input.press('Enter')
    await expect(editor.getByTestId('settings-hosts-error')).toContainText('already on the list')
    await expect(editor.getByTestId('settings-hosts-item')).toHaveCount(1)

    await input.fill('Git.Example.org:8443')
    await input.press('Enter')
    await input.fill('cdn.example.org, bücher.example')
    await editor.getByTestId('settings-hosts-add').click()
    await expect(editor.getByTestId('settings-hosts-item')).toHaveText(['api.example.org', 'git.example.org:8443', 'cdn.example.org', 'xn--bcher-kva.example'])
    await expect(editor.getByTestId('settings-hosts-count')).toHaveText('4/64')
    await page.locator('[data-field="chats_egress_allowed_hosts"]').evaluate(el => el.scrollIntoView({ block: 'center' }))
    await shot(page, 'settings-allowed-hosts')

    await editor.getByRole('button', { name: 'Remove cdn.example.org' }).click()
    await expect(editor.getByTestId('settings-hosts-item')).toHaveCount(3)
    await page.getByRole('button', { name: 'Apply' }).click()
    await expect.poll(() => settings.log.hostSettings).toEqual([{ chats_egress_allowed_hosts: 'api.example.org, git.example.org:8443, xn--bcher-kva.example' }])
  })
})
