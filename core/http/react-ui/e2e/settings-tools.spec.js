import { test, expect } from './coverage-fixtures.js'
import { mockSettings } from './settings-fixtures.js'
import { ADMIN, installedAddon, release, sessionFor, mockAddons } from './addons-fixtures.js'

// Settings > Tools lists the built-in chat tools an add-on may replace, and the
// install dialog warns when an add-on replaces a tool another one already
// replaces. The API is stubbed.

const SHOTS = process.env.HOST_EXEC_SHOT_DIR
const shot = async (page, name) => { if (SHOTS) { await page.waitForTimeout(300); await page.screenshot({ path: `${SHOTS}/${name}.png`, fullPage: true }) } }

const bash = (over = {}) => ({
  id: 'bash', label: 'Commands (bash)', description: 'x', setting: 'chats_host_exec', enabled: true,
  provider: 'builtin', candidates: [], mode: 'ask', ...over,
})

async function openTools(page, tools) {
  const state = await mockSettings(page, { chats_host_exec: '' })
  await page.route('**/api/chats/admin/tools', route => route.fulfill({ json: { tools } }))
  await page.goto('/app/settings')
  await page.getByRole('button', { name: /^Tools/ }).click()
  await expect(page.getByTestId('settings-tools')).toBeVisible()
  return state
}

test.describe('Settings > Tools', () => {
  test('bash says who serves it and what it can reach, and its mode is a draft applied with the others', async ({ page }) => {
    const state = await openTools(page, [bash()])
    const row = page.getByTestId('settings-tools-row-bash')
    await expect(row).toContainText('Commands (bash)')
    await expect(page.getByTestId('settings-tools-bash-provider')).toHaveText('Built-in')
    await expect(page.getByTestId('settings-tools-bash-warning')).toContainText('reach the internet, your network and the LocalAI API')
    await expect(row).toContainText('Not set: the default applies (ask each time).')
    await expect(page.getByTestId('settings-tools-install-sandbox')).toHaveAttribute('href', '/app/addons?tab=browse&q=sandbox')
    await shot(page, 'settings-tools')
    await row.getByRole('radio', { name: /Allow for everyone/ }).check()
    await page.getByRole('button', { name: 'Apply' }).click()
    await expect.poll(() => state.posts).toEqual([{ chats_host_exec: 'on' }])
  })

  test('the web_search row tests each search provider and says what each did', async ({ page }) => {
    const webSearch = {
      id: 'web_search', label: 'Web search', description: 'x', setting: 'chats_web_search', enabled: true,
      provider: 'builtin', candidates: [], options: ['chats_web_searxng_url', 'chats_web_search_providers'],
    }
    await openTools(page, [webSearch])
    const queries = []
    let answer = {
      ok: true, provider: 'brave_html', host: 'search.brave.com', results: [{ title: 'LocalAI', url: 'https://localai.io/', snippet: '' }],
      providers: [
        { name: 'duckduckgo', host: 'duckduckgo.com', outcome: 'challenged', results: 0, reason: 'challenged by a bot check' },
        { name: 'brave_html', host: 'search.brave.com', outcome: 'ok', results: 20 },
        { name: 'bing_html', host: 'www.bing.com', outcome: 'blocked', results: 0, reason: 'blocked this server (429 Too Many Requests)' },
      ],
    }
    await page.route('**/api/chats/admin/web/test', route => { queries.push(route.request().postDataJSON()); return route.fulfill({ json: answer }) })
    await page.getByTestId('settings-tools-web-test-query').fill('ultime news oggi')
    await page.getByTestId('settings-tools-web-test-run').click()
    await expect(page.getByTestId('settings-tools-web-test-summary')).toHaveText('Chats get the results of search.brave.com.')
    await expect(page.getByTestId('settings-tools-web-test-duckduckgo')).toContainText('Bot check')
    await expect(page.getByTestId('settings-tools-web-test-duckduckgo')).toContainText('challenged by a bot check')
    await expect(page.getByTestId('settings-tools-web-test-brave_html')).toContainText('20 results')
    await expect(page.getByTestId('settings-tools-web-test-bing_html')).toHaveAttribute('data-outcome', 'blocked')
    expect(queries).toEqual([{ query: 'ultime news oggi' }])
    await shot(page, 'settings-tools-web-test')

    answer = { ok: false, provider: '', host: '', results: [], error: 'no search provider works. Tried duckduckgo: challenged by a bot check.', providers: [answer.providers[0]] }
    await page.getByTestId('settings-tools-web-test-run').click()
    await expect(page.getByTestId('settings-tools-web-test-summary')).toContainText('No provider answered. no search provider works. Tried duckduckgo')
  })

  test('switching bash off saves off', async ({ page }) => {
    const state = await openTools(page, [bash()])
    await page.getByTestId('settings-tools-bash-switch').click()
    await expect(page.getByTestId('settings-tools-bash-mode')).toHaveCount(0)
    await page.getByRole('button', { name: 'Apply' }).click()
    await expect.poll(() => state.posts).toEqual([{ chats_host_exec: 'off' }])
  })

  test('with two add-ons that replace bash and no choice, it asks to choose one and saves the pin', async ({ page }) => {
    const state = await openTools(page, [bash({ provider: 'other', candidates: ['other', 'sandbox'], default: 'other', installed_at: { other: '2026-09-01T10:00:00Z', sandbox: '2026-10-01T10:00:00Z' } })])
    await expect(page.getByTestId('settings-tools-bash-unpinned')).toHaveText('2 add-ons replace bash: choose which one to use.')
    await expect(page.getByTestId('settings-tools-bash-provider')).toHaveText('Add-on: other')
    await expect(page.getByTestId('settings-tools-bash-other-default')).toHaveText('Default: installed first')
    await expect(page.getByTestId('settings-tools-bash-sandbox-default')).toHaveCount(0)
    await expect(page.getByTestId('settings-tools-bash-sandbox-installed')).toContainText('Installed')
    await page.getByTestId('settings-tools-bash-providers').getByRole('radio', { name: 'sandbox' }).check()
    await page.getByRole('button', { name: 'Apply' }).click()
    await expect.poll(() => state.posts).toEqual([{ chats_tool_providers: { bash: 'sandbox' } }])
  })
})

test.describe('Install of an add-on that replaces a built-in tool', () => {
  test('warns when another installed add-on already replaces it, and still installs', async ({ page }) => {
    await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(ADMIN) }))
    await mockAddons(page, {
      installed: [installedAddon({ id: 'sandbox', title: 'Sandbox', shadows: ['bash'] })],
      releases: [release('runner', { name: 'Runner', toolCount: 1, tools: ['bash'], shadows: ['bash'] })],
    })
    await page.goto('/app/addons?tab=browse')
    await page.getByRole('button', { name: 'Install Runner' }).click()
    const warning = page.getByTestId('install-shadow-warning')
    await expect(warning).toHaveText('This add-on replaces the built-in bash. Another add-on (Sandbox) already does. Only one can be active; you choose which in Settings > Tools. Until you choose, the one installed first serves.')
    await expect(page.getByTestId('install-dialog').getByRole('button', { name: 'Install and turn on' })).toBeEnabled()
    await shot(page, 'install-shadow-warning')
  })
})

test.describe('Settings > Tools: skills that add-ons replace', () => {
  async function openWithSkills(page, skills) {
    const state = await mockSettings(page, { chats_host_exec: '' })
    await page.route('**/api/chats/admin/tools', route => route.fulfill({ json: { tools: [bash()], skills } }))
    await page.goto('/app/settings')
    await page.getByRole('button', { name: /^Tools/ }).click()
    await expect(page.getByTestId('settings-tools')).toBeVisible()
    return state
  }

  test('lists who serves a replaced skill and saves the choice in the same pin map as the tools', async ({ page }) => {
    const state = await openWithSkills(page, [
      { id: 'xlsx', provider: 'office', candidates: ['office', 'sheets'], default: 'office', titles: { office: 'Office', sheets: 'Sheets' } },
      { id: 'docx', provider: 'builtin', candidates: ['office'], titles: { office: 'Office' }, pinned: 'office', note: 'not running' },
    ])
    await expect(page.getByTestId('settings-tools-skills')).toContainText('Skills that add-ons replace')
    await expect(page.getByTestId('settings-tools-skill-xlsx-provider')).toHaveText('Add-on: Office')
    await expect(page.getByTestId('settings-tools-xlsx-unpinned')).toHaveText('2 add-ons replace xlsx: choose which one to use.')
    await expect(page.getByTestId('settings-tools-xlsx-office-default')).toBeVisible()
    await expect(page.getByTestId('settings-tools-skill-docx-provider')).toHaveText('Built-in')
    await expect(page.getByTestId('settings-tools-skill-docx-note')).toHaveText('The chosen add-on is not running: the built-in skill serves.')
    await expect(page.getByTestId('settings-tools-docx-providers')).toHaveCount(0)
    await shot(page, 'settings-tools-skills')
    await page.getByTestId('settings-tools-xlsx-providers').getByRole('radio', { name: 'Sheets' }).check()
    await page.getByRole('button', { name: 'Apply' }).click()
    await expect.poll(() => state.posts).toEqual([{ chats_tool_providers: { xlsx: 'sheets' } }])
  })
})

test.describe('Install of an add-on that replaces a built-in skill', () => {
  test('says which built-in skills it replaces, warns when another add-on already does, and still installs', async ({ page }) => {
    await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(ADMIN) }))
    await mockAddons(page, {
      installed: [installedAddon({ id: 'sheets', title: 'Sheets', skillShadows: ['xlsx'] })],
      releases: [release('office', { name: 'Office', skillShadows: ['xlsx', 'docx'] })],
    })
    await page.goto('/app/addons?tab=browse')
    await page.getByRole('button', { name: 'Install Office' }).click()
    const warnings = page.getByTestId('install-skill-warning')
    await expect(warnings).toHaveCount(2)
    await expect(warnings.nth(0)).toHaveText('This add-on replaces the built-in skill xlsx. Another add-on (Sheets) already does. Only one can be active; you choose which in Settings > Tools. Until you choose, the one installed first serves.')
    await expect(warnings.nth(1)).toHaveText('This add-on replaces the built-in skill docx.')
    await expect(page.getByTestId('install-dialog').getByRole('button', { name: 'Install and turn on' })).toBeEnabled()
    await shot(page, 'install-skill-warning')
  })
})

test.describe('The skills of an add-on in its details', () => {
  test('lists its skills, and lets an administrator choose it for a skill another add-on also replaces', async ({ page }) => {
    await page.route('**/api/auth/status', route => route.fulfill({ json: sessionFor(ADMIN) }))
    const office = { id: 'office', name: 'office', title: 'Office', skills: [{ name: 'xlsx', shadows: 'xlsx' }, { name: 'docx', shadows: 'docx' }, { name: 'office-tips' }] }
    await mockAddons(page, {
      apps: [{ ...installedAddon({ id: 'office', title: 'Office' }), ...office, admin: { canStart: false, canStop: true, managementUrl: '/app/addons?manage=office' } }],
      installed: [installedAddon({ id: 'office', title: 'Office', skillShadows: ['xlsx', 'docx'] })],
    })
    const settings = await mockSettings(page, { chats_tool_providers: { bash: 'sandbox' } })
    let rows = [
      { id: 'xlsx', provider: 'sheets', candidates: ['office', 'sheets'], titles: { office: 'Office', sheets: 'Sheets' } },
      { id: 'docx', provider: 'office', candidates: ['office'], titles: { office: 'Office' } },
    ]
    await page.route('**/api/chats/admin/tools', route => route.fulfill({ json: { tools: [], skills: rows } }))
    await page.goto('/app/addons?detail=office')
    const skills = page.getByTestId('app-skills')
    await expect(skills).toContainText('3 chat skills')
    await expect(page.getByTestId('app-skill-office-tips')).not.toContainText('replaces')
    await expect(page.getByTestId('app-skill-docx')).toContainText('replaces the built-in skill · serves in the chats')
    await expect(page.getByTestId('app-skill-docx-switch')).toHaveCount(0)
    const sw = page.getByTestId('app-skill-xlsx-switch')
    await expect(sw).not.toBeChecked()
    await shot(page, 'addon-skills')
    rows = [{ ...rows[0], provider: 'office', pinned: 'office' }, rows[1]]
    await sw.click()
    await expect.poll(() => settings.posts).toEqual([{ chats_tool_providers: { bash: 'sandbox', xlsx: 'office' } }])
    await expect(sw).toBeChecked()
  })
})
