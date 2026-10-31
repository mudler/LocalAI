import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { mockUser } from './home-fixtures.js'

// The built-in bash runs commands directly on this machine when no Sandbox
// serves bash. The chat says so in the Tools menu and the chip, a command that
// waits for a yes shows a card under the live fold, and a stored call says who
// ran it. The API is stubbed.

const SHOTS = process.env.HOST_EXEC_SHOT_DIR
const shot = async (page, name) => { if (SHOTS) { await page.waitForTimeout(400); await page.screenshot({ path: `${SHOTS}/${name}.png` }) } }

const DETAIL = {
  id: 'c1', title: 'Deck', model: 'qwen3-8b', kind: 'chat', created: '2026-10-25T10:00:00Z', updated: '2026-10-25T10:00:00Z',
  running: false, last_turn: 'done', pending: [],
}

// stubChat serves the chat c1 with the add-on list addons and the transcript
// messages; events() pushes server-sent events to the open stream.
async function stubChat(page, { addons, messages = [], running = false, hostConsent }) {
  const detail = { ...DETAIL, running, ...(hostConsent ? { host_consent: hostConsent } : {}) }
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages } }))
  await page.route('**/api/chats/c1', route => route.fulfill({ json: detail }))
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({
    json: { conversations: [{ ...detail, pending: undefined }], persistent: true, files: true, files_reason: '' },
  }))
  await page.route('**/api/chats/c1/addons', route => route.fulfill({ json: addons }))
  const queue = []
  let wake = null
  await page.route('**/api/chats/c1/events', async (route) => {
    while (queue.length === 0) await new Promise((resolve) => { wake = resolve })
    const body = queue.splice(0).join('')
    await route.fulfill({ status: 200, headers: { 'Content-Type': 'text/event-stream' }, body }).catch(() => {})
  })
  return (...events) => {
    for (const ev of events) queue.push(`event: ${ev.kind}\ndata: ${JSON.stringify(ev)}\n\n`)
    wake?.()
  }
}

const host = { addons: [], commands: 'available', commands_provider: 'builtin', commands_isolation: 'host' }

test.describe('Commands on this machine', () => {
  test.beforeEach(async ({ page }) => { await mockChat(page) })

  test('the Tools menu and the chip say that commands run here, not isolated, with the install link', async ({ page }) => {
    await stubChat(page, { addons: host })
    await page.goto('/app/chats/c1')
    const chip = page.getByTestId('chat-commands-chip')
    await expect(chip).toHaveText('Commands run on this machine')
    await expect(chip).toHaveAttribute('data-state', 'host')
    await page.getByTestId('chat-tools-button').click()
    const notice = page.getByTestId('chat-commands-notice')
    await expect(notice).toContainText('Commands run on this machine, with its files and network, not isolated. Install the Sandbox for isolation.')
    await expect(notice.getByTestId('chat-commands-notice-link')).toHaveAttribute('href', '/app/addons?tab=browse&q=sandbox')
    await shot(page, 'host-notice')
  })

  test('with the Sandbox off for the chat, the notice says why commands run here', async ({ page }) => {
    await stubChat(page, { addons: { ...host, addons: [{ id: 'sandbox', tools: 2, off: true }], commands_fallback: 'sandbox', commands_fallback_reason: 'off' } })
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    const notice = page.getByTestId('chat-commands-notice')
    await expect(notice).toContainText('The Sandbox is off for this chat, so commands run on this machine')
    await expect(notice.getByTestId('chat-commands-notice-link')).toHaveCount(0)
  })

  test('a card that waits comes back after a reload and can be answered', async ({ page }) => {
    const push = await stubChat(page, {
      addons: { ...host, commands_consent: 'ask' }, running: true,
      hostConsent: { id: 'k7', command: 'make deck' },
    })
    const answers = []
    await page.route('**/api/chats/c1/host-exec/consent', route => {
      answers.push(route.request().postDataJSON())
      return route.fulfill({ status: 204, body: '' })
    })
    await page.goto('/app/chats/c1')
    const card = page.getByTestId('chat-host-consent')
    await expect(card.getByTestId('chat-host-consent-command')).toHaveText('make deck', { timeout: 15_000 })
    await page.reload()
    await expect(card.getByTestId('chat-host-consent-command')).toHaveText('make deck', { timeout: 15_000 })
    await card.getByTestId('chat-host-consent-once').click()
    await expect.poll(() => answers).toEqual([{ id: 'k7', decision: 'once' }])
    // The call ends: the card goes away.
    push({ kind: 'tool_end', turn: 2, tool: { name: 'bash', ok: true, output: 'done', seq: 4 } })
    await expect(card).toHaveCount(0)
  })

  test('an answer to a card whose turn has ended shows the card as not answered, with no buttons', async ({ page }) => {
    await stubChat(page, {
      addons: { ...host, commands_consent: 'ask' }, running: true,
      hostConsent: { id: 'k9', command: 'make deck' },
    })
    await page.route('**/api/chats/c1/host-exec/consent', route => route.fulfill({
      status: 410, json: { error: { code: 410, type: 'consent_expired', message: 'the card expired: the turn that asked has ended, so the command did not run' } },
    }))
    await page.goto('/app/chats/c1')
    const card = page.getByTestId('chat-host-consent')
    await expect(card.getByTestId('chat-host-consent-command')).toHaveText('make deck', { timeout: 15_000 })
    await card.getByTestId('chat-host-consent-once').click()
    const expired = page.getByTestId('chat-host-consent-expired')
    await expect(expired).toContainText('Not answered: the turn that asked has ended, so the command did not run.')
    await expect(expired).toContainText('make deck')
    await expect(page.getByTestId('chat-host-consent-once')).toHaveCount(0)
    await expect(page.getByRole('alert')).toHaveCount(0)
  })

  test('an administrator answers the card of a waiting command: Run once', async ({ page }) => {
    const push = await stubChat(page, { addons: { ...host, commands_consent: 'ask' }, running: true })
    const answers = []
    await page.route('**/api/chats/c1/host-exec/consent', route => {
      answers.push(route.request().postDataJSON())
      return route.fulfill({ status: 204, body: '' })
    })
    await page.goto('/app/chats/c1')
    push(
      { kind: 'turn_start', turn: 2 },
      { kind: 'tool_start', turn: 2, tool: { name: 'bash', summary: 'python build_deck.py' } },
      { kind: 'host_exec_consent', turn: 2, ids: ['k1'], text: 'python build_deck.py' },
    )
    const card = page.getByTestId('chat-host-consent')
    await expect(card).toBeVisible({ timeout: 15_000 })
    await expect(card).toContainText('Run this command on this machine?')
    await expect(card).toContainText('They can read files that LocalAI can read and reach the internet and your network.')
    await expect(card.getByTestId('chat-host-consent-command')).toHaveText('python build_deck.py')
    await shot(page, 'host-consent-card')
    await card.getByTestId('chat-host-consent-once').click()
    await expect.poll(() => answers).toEqual([{ id: 'k1', decision: 'once' }])
  })

  test('Always on this instance saves the setting first, then runs the command', async ({ page }) => {
    const push = await stubChat(page, { addons: { ...host, commands_consent: 'ask' }, running: true })
    const order = []
    await page.route('**/api/settings', route => {
      if (route.request().method() !== 'POST') return route.fallback()
      order.push(['settings', route.request().postDataJSON()])
      return route.fulfill({ json: { success: true } })
    })
    await page.route('**/api/chats/c1/host-exec/consent', route => {
      order.push(['consent', route.request().postDataJSON()])
      return route.fulfill({ status: 204, body: '' })
    })
    await page.goto('/app/chats/c1')
    push(
      { kind: 'turn_start', turn: 2 },
      { kind: 'tool_start', turn: 2, tool: { name: 'bash', summary: 'ls' } },
      { kind: 'host_exec_consent', turn: 2, ids: ['k2'], text: 'ls' },
    )
    await page.getByTestId('chat-host-consent-always').click()
    await expect.poll(() => order).toEqual([['settings', { chats_host_exec: 'on' }], ['consent', { id: 'k2', decision: 'once' }]])
  })

  test('a person who is not an administrator can only refuse, and is told to ask one', async ({ page }) => {
    await mockUser(page, { chats: true })
    const push = await stubChat(page, { addons: { ...host, commands_consent: 'ask' }, running: true })
    const answers = []
    await page.route('**/api/chats/c1/host-exec/consent', route => {
      answers.push(route.request().postDataJSON())
      return route.fulfill({ status: 204, body: '' })
    })
    await page.goto('/app/chats/c1')
    push(
      { kind: 'turn_start', turn: 2 },
      { kind: 'tool_start', turn: 2, tool: { name: 'bash', summary: 'ls' } },
      { kind: 'host_exec_consent', turn: 2, ids: ['k3'], text: 'ls' },
    )
    const card = page.getByTestId('chat-host-consent')
    await expect(card).toBeVisible({ timeout: 15_000 })
    await expect(card.getByTestId('chat-host-consent-ask')).toHaveText('Only an administrator can allow commands on this machine. Ask one, or run the command yourself.')
    await expect(card.getByTestId('chat-host-consent-once')).toHaveCount(0)
    await card.getByTestId('chat-host-consent-deny').click()
    await expect.poll(() => answers).toEqual([{ id: 'k3', decision: 'deny' }])
  })

  test('a stored call says who ran it', async ({ page }) => {
    await stubChat(page, {
      addons: host,
      messages: [
        { seq: 1, turn: 1, role: 'user', text: 'list the files', at: '2026-10-25T10:00:00Z' },
        { seq: 2, turn: 1, role: 'tool', text: 'bash: ls\nnotes.txt', at: '2026-10-25T10:00:01Z', card: { kind: 'host_exec', id: 'once' } },
        { seq: 3, turn: 1, role: 'tool', text: 'bash: pwd\n/workspace', at: '2026-10-25T10:00:02Z', card: { kind: 'tool_provider', id: 'sandbox' } },
        { seq: 4, turn: 1, role: 'assistant', text: 'There is one file.', at: '2026-10-25T10:00:03Z' },
      ],
    })
    await page.goto('/app/chats/c1')
    await expect(page.getByText('There is one file.')).toBeVisible({ timeout: 15_000 })
    await page.getByTestId('chat-activity').locator('.cx-fold__head').first().click()
    const tags = page.getByTestId('chat-step-provider')
    await expect(tags).toHaveText(['This machine, allowed once', 'Sandbox'])
    await shot(page, 'host-provider-tags')
  })
})
