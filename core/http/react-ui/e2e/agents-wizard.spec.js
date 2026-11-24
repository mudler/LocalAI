import { test, expect } from './coverage-fixtures.js'
import { installAgents } from './agents-fixtures.js'

// The new-agent wizard and the Finish setup card of the agent page, with the
// saved-agents API stubbed. The apps of step 3 come from stubbed add-on routes.

const KINDS = [
  { kind: 'slack', addon: 'slack', title: 'Slack', description: 'Answer in a Slack workspace.', page: 'apps', setup: 'A bot token.' },
  { kind: 'mail', addon: 'mailer', title: 'Mailer', description: 'Read and send mail.', agent_optional: true },
]
const WAVE = { kind: 'wave', addon: 'wave', title: 'Wave', description: 'Answer in Wave.', page: 'home' }

async function stubAddons(page, { kinds = KINDS, catalog = [] } = {}) {
  await page.route(/\/api\/chats\/connectors\/kinds$/, r => r.fulfill({ json: { kinds, capabilities: [] } }))
  await page.route('**/api/addons/apps', r => r.fulfill({ json: { apps: [] } }))
  await page.route('**/api/addons/dispatch', r => r.fulfill({
    json: { payload: { releases: catalog.map(c => ({ id: c.id, name: c.name, description: c.description, version: '1.0.0', sourceId: 'main', os: 'linux', arch: 'amd64', policy: {} })), total: catalog.length } },
  }))
}

const fillPurpose = async (page, name = 'helper', instructions = 'Be brief.') => {
  await page.getByLabel('Name', { exact: false }).first().fill(name)
  await page.getByLabel('Instructions', { exact: false }).first().fill(instructions)
}
const next = (page) => page.getByTestId('wizard-next').click()
const created = (s) => s.calls.filter(c => c.method === 'POST' && c.url === '/api/chats/agents')

test.describe('New agent wizard', () => {
  test('four steps with a step bar and a summary rail; Continue needs a name and instructions', async ({ page }) => {
    const s = await installAgents(page)
    await stubAddons(page)
    await page.goto('/app/agents/new')
    const steps = page.getByTestId('wizard-steps')
    await expect(steps.getByRole('button')).toHaveText(['1Purpose', '2Abilities', '3Where it works', '4Review'])
    await expect(page.getByTestId('wizard-step-purpose')).toHaveAttribute('aria-current', 'step')
    // Four templates, as today.
    await expect(page.locator('.aed-tpl__card')).toHaveCount(4)
    await expect(page.getByTestId('wizard-next')).toBeDisabled()
    await expect(page.getByTestId('wizard-step-review')).toBeDisabled()
    // The model is a quiet line until the person chooses one.
    await expect(page.getByTestId('wizard-model')).toContainText('the one you pick in each chat')
    await fillPurpose(page)
    await expect(page.getByTestId('wizard-rail-name')).toHaveText('helper')
    await next(page)
    await expect(page.getByRole('heading', { name: 'How much may it do on its own?' })).toBeFocused()
    // The level cards, none picked; the line says the defaults apply.
    await expect(page.getByTestId('agent-presets').locator('input[type=radio]:checked')).toHaveCount(0)
    await expect(page.getByTestId('agent-preset-state')).toContainText('default tools')
    await expect(page.getByTestId('wizard-rail-level')).toHaveText('Default tools')
    // Files keeps today's default.
    await expect(page.getByTestId('agent-folder-scope-all')).toHaveAttribute('aria-pressed', 'true')
    // A level replaces the defaults and the rail follows; Undo brings them back.
    await page.getByTestId('agent-preset-readonly').click()
    await expect(page.getByTestId('wizard-rail-level')).toHaveText('Look only')
    await expect(page.getByTestId('wizard-rail-files')).toHaveText('No files')
    await page.getByTestId('agent-preset-applied-undo').click()
    await expect(page.getByTestId('wizard-rail-level')).toHaveText('Default tools')
    await page.getByTestId('agent-preset-assistant').click()
    // The 27 switches sit under a disclosure.
    await expect(page.getByTestId('agent-tools-panel')).toHaveCount(0)
    await page.getByTestId('wizard-fine').locator('summary').click()
    await expect(page.getByTestId('agent-tools-panel')).toBeVisible()
    await next(page)
    await expect(page.getByTestId('wizard-pane-where')).toBeVisible()
    await page.getByTestId('wizard-app-kind:slack/slack').click()
    await expect(page.getByTestId('wizard-rail-apps')).toContainText('Slack')
    await next(page)
    await expect(page.getByRole('heading', { name: 'Ready to create' })).toBeVisible()
    // Back through the step bar.
    await page.getByTestId('wizard-step-purpose').click()
    await expect(page.getByLabel('Name', { exact: false }).first()).toHaveValue('helper')
    await page.getByTestId('wizard-step-review').click()
    await page.getByTestId('wizard-create').click()
    await expect(page).toHaveURL(/\/app\/agents\/a3$/)
    expect(created(s)).toHaveLength(1)
    expect(created(s)[0].body).toMatchObject({ name: 'helper', instructions: 'Be brief.' })
    expect(created(s)[0].body.builtins_off).toContain('files')
    // The chosen app is a row of the Finish setup card, not a connection.
    const row = page.getByTestId('agent-setup-row')
    await expect(row).toHaveCount(1)
    await expect(row).toContainText('Slack')
    await expect(page.getByTestId('agent-setup-count')).toHaveText('0 of 1 done')
  })

  test('skipping every optional step sends what the blank form sends', async ({ page }) => {
    const s = await installAgents(page)
    await stubAddons(page)
    await page.goto('/app/agents/new')
    await fillPurpose(page)
    await next(page)
    await next(page)
    await next(page)
    // Files is "All my files" by default: the Review warns and changes nothing.
    const warning = page.getByTestId('wizard-files-warning')
    await expect(warning).toContainText('It can see all your files')
    await expect(warning.getByRole('button', { name: 'Limit to folders' })).toBeVisible()
    await page.getByTestId('wizard-create').click()
    await expect(page).toHaveURL(/\/app\/agents\/a3$/)
    expect(created(s)).toHaveLength(1)
    expect(created(s)[0].body).toEqual({
      name: 'helper', description: '', instructions: 'Be brief.', model: '', network: 'off', memory: 'mine_and_own',
      mcp_servers: [], addons: [], collections: [], skills: [], builtins_off: [], builtins_on: [],
      builtins: ['triggers.propose', 'triggers.run', 'talkback.channels', 'files.upload', 'memory.write'],
    })
    // Nothing was chosen, so there is no card.
    await expect(page.getByTestId('agent-setup')).toHaveCount(0)
  })

  test('Limit to folders takes the person to the Files choice without changing it', async ({ page }) => {
    await installAgents(page)
    await stubAddons(page)
    await page.goto('/app/agents/new')
    await fillPurpose(page)
    await next(page); await next(page); await next(page)
    await page.getByTestId('wizard-limit-folders').click()
    await expect(page.getByTestId('wizard-pane-abilities')).toBeVisible()
    await expect(page.getByTestId('agent-folder-scope-all')).toHaveAttribute('aria-pressed', 'true')
    await page.getByTestId('agent-folder-scope-one').click()
    await expect(page.getByTestId('agent-folder')).toBeVisible()
  })

  test('a starting point fills the text only, and suggests its level without applying it', async ({ page }) => {
    await installAgents(page)
    await stubAddons(page)
    await page.goto('/app/agents/new')
    await page.locator('[data-template="researcher"]').click()
    await expect(page.getByLabel('Name', { exact: false }).first()).toHaveValue('research-assistant')
    await next(page)
    await expect(page.getByTestId('wizard-template-level')).toContainText('Look only')
    await expect(page.getByTestId('wizard-rail-level')).toHaveText('Default tools')
  })

  test('step 3 is built from the server: an extra add-on and a not-installed one appear with no UI change', async ({ page }) => {
    await installAgents(page)
    await stubAddons(page, { kinds: [...KINDS, WAVE], catalog: [{ id: 'newchat', name: 'New chat', description: 'Answer in New chat.' }] })
    await page.goto('/app/agents/new')
    await fillPurpose(page)
    await next(page); await next(page)
    const chat = page.getByTestId('wizard-group-chat')
    await expect(chat.getByRole('heading', { name: 'Chat apps' })).toBeVisible()
    await expect(chat.locator('[data-app]')).toHaveCount(2)
    await expect(chat.locator('[data-app="kind:wave/wave"]')).toContainText('Answer in Wave.')
    // The add-on's own setup text, and the generic line when it has none.
    await expect(chat.locator('[data-app="kind:slack/slack"]')).toContainText('A bot token.')
    await expect(chat.locator('[data-app="kind:wave/wave"]')).toContainText('Finish setting up Wave')
    // A kind that holds credentials only, a tool add-on and a catalog entry are tools and services.
    const tools = page.getByTestId('wizard-group-tools')
    await expect(tools.getByRole('heading', { name: 'Tools and services' })).toBeVisible()
    await expect(tools.locator('[data-app="kind:mailer/mail"]')).toBeVisible()
    await expect(tools.locator('[data-app="tool:office"]')).toBeVisible()
    await expect(tools.locator('[data-app="catalog:newchat"]')).toContainText('Not installed')
    await page.getByTestId('wizard-app-kind:wave/wave').click()
    await expect(page.getByTestId('wizard-app-kind:wave/wave')).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByTestId('wizard-where-note')).toContainText('does not connect it')
  })

  test('with no chat apps the step still opens and says so', async ({ page }) => {
    await installAgents(page)
    await stubAddons(page, { kinds: [] })
    await page.goto('/app/agents/new')
    await fillPurpose(page)
    await next(page); await next(page)
    await expect(page.getByTestId('wizard-group-chat')).toHaveCount(0)
    await expect(page.getByTestId('wizard-group-tools').locator('[data-app="tool:office"]')).toBeVisible()
  })
})

const WANTED = [
  { key: 'kind:slack/slack', type: 'kind', addon: 'slack', kind: 'slack', page: 'apps', title: 'Slack', setup: 'A bot token.', credentialed: false },
  { key: 'kind:wave/wave', type: 'kind', addon: 'wave', kind: 'wave', page: 'home', title: 'Wave', setup: '', credentialed: false },
  { key: 'tool:office', type: 'tool', addon: 'office', kind: '', page: '', title: 'Office', setup: '', credentialed: true },
  { key: 'catalog:newchat', type: 'catalog', addon: 'newchat', kind: '', page: '', title: 'New chat', setup: '', credentialed: false },
]
const SLACK_BINDING = { id: 'b2', name: 'Team app', kind: 'slack', addon: 'slack', active: true, agent_id: 'a2', enabled: true, accept_any: false, allow: ['U1'], has_token: true }

const seed = (page, agent, state) => page.addInitScript(([k, v]) => { try { if (localStorage.getItem(k) === null) localStorage.setItem(k, v) } catch { /* private window */ } }, [`localai:agent-setup:${agent}`, JSON.stringify(state)])

test.describe('Finish setup card', () => {
  test('one row per chosen app, done from the server, with the right button target', async ({ page }) => {
    await installAgents(page, { bindings: [SLACK_BINDING] })
    await stubAddons(page, { kinds: [...KINDS, WAVE] })
    await seed(page, 'a2', { wanted: WANTED, dismissed: false })
    await page.goto('/app/agents/a2')
    const card = page.getByTestId('agent-setup')
    await expect(card).toBeVisible()
    await expect(card.getByTestId('agent-setup-row')).toHaveCount(4)
    // Slack has a connection for this agent on the server: done, no button.
    const slack = card.locator('[data-setup="kind:slack/slack"]')
    await expect(slack).toHaveAttribute('data-done', 'true')
    await expect(slack.getByTestId('agent-setup-action')).toHaveCount(0)
    await expect(card.getByTestId('agent-setup-count')).toHaveText('1 of 4 done')
    await expect(card.getByTestId('agent-setup-progress')).toHaveAttribute('value', '1')
    // A chat app opens the page of its kind, on the add sheet, with this agent.
    await expect(card.locator('[data-setup="kind:wave/wave"]').getByTestId('agent-setup-action')).toHaveAttribute('href', '/app/addons/wave/home?new=1&agent=a2')
    await expect(card.locator('[data-setup="kind:wave/wave"]')).toContainText('Finish setting up Wave')
    // A tool add-on with an account opens the agent editor.
    await expect(card.locator('[data-setup="tool:office"]').getByTestId('agent-setup-action')).toHaveAttribute('href', '/app/agents/a2/edit#aed-sec-capabilities')
    // An add-on that is not installed opens its catalog entry.
    await expect(card.locator('[data-setup="catalog:newchat"]')).toContainText('Not installed')
    await expect(card.locator('[data-setup="catalog:newchat"]').getByTestId('agent-setup-action')).toHaveAttribute('href', '/app/addons?tab=browse&q=newchat')
  })

  test('the card is on top of the page and Hide removes it for good', async ({ page }) => {
    await installAgents(page, { bindings: [SLACK_BINDING] })
    await stubAddons(page)
    await seed(page, 'a2', { wanted: WANTED.slice(0, 1).concat(WANTED.slice(2, 3)), dismissed: false })
    await page.goto('/app/agents/a2')
    await expect(page.getByTestId('agent-setup')).toBeVisible()
    await page.getByTestId('agent-setup-dismiss').click()
    await expect(page.getByTestId('agent-setup')).toHaveCount(0)
    expect(await page.evaluate(() => JSON.parse(localStorage.getItem('localai:agent-setup:a2')).dismissed)).toBe(true)
    await page.reload()
    await expect(page.getByTestId('agent-facts')).toBeVisible()
    await expect(page.getByTestId('agent-setup')).toHaveCount(0)
  })

  test('the card goes away when the server has every row, and does not need storage', async ({ page }) => {
    await installAgents(page, { bindings: [SLACK_BINDING] })
    await stubAddons(page)
    await seed(page, 'a2', { wanted: WANTED.slice(0, 1), dismissed: false })
    await page.goto('/app/agents/a2')
    await expect(page.getByTestId('agent-facts')).toBeVisible()
    await expect(page.getByTestId('agent-setup')).toHaveCount(0)
  })

  test('a browser that refuses the storage shows the page without a card', async ({ page }) => {
    await installAgents(page)
    await stubAddons(page)
    // Storage that refuses the keys of the card, as a full or blocked store does.
    await page.addInitScript(() => {
      const get = Storage.prototype.getItem
      const set = Storage.prototype.setItem
      Storage.prototype.getItem = function (k) { if (String(k).startsWith('localai:agent-setup:')) throw new Error('blocked'); return get.call(this, k) }
      Storage.prototype.setItem = function (k, v) { if (String(k).startsWith('localai:agent-setup:')) throw new Error('full'); return set.call(this, k, v) }
    })
    await page.goto('/app/agents/a2')
    await expect(page.getByTestId('agent-facts')).toBeVisible()
    await expect(page.getByTestId('agent-setup')).toHaveCount(0)
  })
})
