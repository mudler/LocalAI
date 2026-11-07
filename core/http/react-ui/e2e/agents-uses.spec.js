import { test, expect } from './coverage-fixtures.js'
import { AGENTS } from './agents-fixtures.js'
import { installUses, row } from './agents-uses-fixtures.js'

// The agent editor, "What it can use": built-in tools, add-ons (one row each)
// and MCP servers in one section. The API is stubbed. The server's rule for the
// account is covered by Go tests; here the sentence must follow the form.

const SHOTS = process.env.USES_SHOTS || ''
async function shot(page, name) {
  if (!SHOTS) return
  const width = page.viewportSize().width
  const section = page.locator('#aed-sec-capabilities')
  for (const theme of ['light', 'dark']) {
    await page.evaluate((t) => document.documentElement.setAttribute('data-theme', t), theme)
    await page.waitForTimeout(300)
    await section.screenshot({ path: `${SHOTS}/${name}-${width}-${theme}.png` })
  }
  await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'light'))
}
const open = async (page, url = '/app/agents/a2/edit', opts) => {
  const s = await installUses(page, opts)
  await page.goto(url)
  await expect(page.getByTestId('agent-edit')).toBeVisible()
  return s
}
const now = (r) => r.getByTestId('agent-addon-now')
const save = (page) => page.getByRole('button', { name: /Create agent|Save changes/i }).first().click()

test.describe('The section', () => {
  test('every checkbox of the editor is the kit checkbox, in the add-on rows, Memory, Skills and Files', async ({ page }) => {
    await open(page, '/app/agents/a9/edit', { agents: [{ ...AGENTS[1], id: 'a9', addons: ['tables', 'tracker'], skills: ['pdf'], folders: [{ path: '/Projects/Acme', mode: 'write', state: 'ok' }], folder: { path: '/Projects/Acme', mode: 'write', state: 'ok' } }] })
    await page.getByTestId('agent-uses-addons').locator('details').evaluateAll(ds => ds.forEach(d => { d.open = true }))
    await expect(page.getByTestId('agent-addon-resource').first()).toBeVisible()
    const boxes = page.locator('[data-testid=agent-edit] input[type=checkbox]')
    expect(await boxes.count()).toBeGreaterThan(8)
    const plain = await boxes.evaluateAll(els => els.filter(e => !e.classList.contains('dk-check') || getComputedStyle(e).appearance !== 'none').map(e => e.closest('label')?.textContent?.trim() || e.outerHTML))
    expect(plain).toEqual([])
  })

  test('presets, built-in tools, add-ons and MCP servers are one section, in that order, with no Connections card', async ({ page }) => {
    await open(page)
    const uses = page.getByTestId('agent-uses')
    const order = await uses.locator('[data-testid=agent-presets], [data-testid=agent-uses-builtins], [data-testid=agent-uses-addons], [data-testid=agent-uses-mcp]').evaluateAll(els => els.map(e => e.getAttribute('data-testid')))
    expect(order).toEqual(['agent-presets', 'agent-uses-builtins', 'agent-uses-addons', 'agent-uses-mcp'])
    await expect(page.getByTestId('agent-preset-note')).toContainText('Add-ons, accounts, MCP servers and skills stay as they are')
    await expect(page.locator('#aed-sec-connections')).toHaveCount(0)
    await expect(page.getByTestId('agent-nav-connections')).toHaveCount(0)
    await shot(page, 'new')
  })

  test('a level changes the built-in tools and leaves add-ons, accounts and skills alone', async ({ page }) => {
    await open(page)
    const tracker = row(page, 'Tracker')
    await tracker.getByLabel('Home account').check()
    await row(page, 'Office').getByRole('switch', { name: 'Office' }).click()
    await page.getByTestId('agent-preset-readonly').click()
    await expect(tracker.getByRole('switch', { name: 'Tracker' })).toBeChecked()
    await expect(tracker.getByLabel('Home account')).toBeChecked()
    await expect(row(page, 'Office').getByRole('switch', { name: 'Office' })).toBeChecked()
  })
})

test.describe('Accounts and the Right now sentence', () => {
  test('a row with two accounts and none picked is amber, and says what a chat and a run get', async ({ page }) => {
    await open(page)
    const tracker = row(page, 'Tracker')
    await tracker.getByRole('switch', { name: 'Tracker' }).click()
    await expect(now(tracker)).toHaveAttribute('data-tone', 'warn')
    await expect(now(tracker)).toContainText('A chat asks you to pick one')
    await expect(now(tracker)).toContainText('a run with nobody there gets none')
    // The section on the nav and its card say something needs the person.
    await expect(page.getByTestId('agent-nav-capabilities')).toHaveAttribute('data-needs', 'true')
    await shot(page, 'warning')
    await tracker.getByLabel('Home account').check()
    await expect(now(tracker)).toHaveAttribute('data-tone', 'ok')
    await expect(now(tracker)).toContainText('using Home account')
    await expect(page.getByTestId('agent-nav-capabilities')).not.toHaveAttribute('data-needs', 'true')
  })

  test('the only account is used without a pick, and a row without a credential needs none', async ({ page }) => {
    await open(page)
    const lamp = row(page, 'Lamp control')
    await lamp.getByRole('switch', { name: 'Lamp control' }).click()
    await expect(now(lamp)).toContainText('Hall lamp, your only account')
    await expect(now(lamp)).toHaveAttribute('data-tone', 'ok')
    const office = row(page, 'Office')
    await office.getByRole('switch', { name: 'Office' }).click()
    await expect(now(office)).toContainText('It needs no account')
  })

  test('switching off keeps the account for later, and it is sent', async ({ page }) => {
    const s = await open(page)
    const tracker = row(page, 'Tracker')
    await tracker.getByLabel('Work account').check()
    await tracker.getByRole('switch', { name: 'Tracker' }).click()
    await expect(tracker.getByTestId('agent-addon-kept')).toHaveText('Kept for later')
    await expect(now(tracker)).toContainText('Accounts kept for later: Work account')
    await page.getByLabel('Name', { exact: false }).first().fill('Triage')
    await save(page)
    await expect.poll(() => s.calls.find(c => c.method === 'PUT')?.body.connections).toEqual({ tracker: ['b1'] })
    expect(s.calls.find(c => c.method === 'PUT').body.addons).not.toContain('tracker')
  })

  test('the tools are listed, and the ones that change data are marked', async ({ page }) => {
    await open(page)
    const tracker = row(page, 'Tracker')
    await tracker.getByTestId('agent-addon-tools').locator('summary').click()
    const items = tracker.getByTestId('agent-addon-tools').locator('li')
    await expect(items).toHaveCount(9)
    await expect(items.filter({ hasText: 'create_issue' })).toContainText('changes data')
    await expect(items.filter({ hasText: 'get_issue' })).not.toContainText('changes data')
    await tracker.getByRole('switch', { name: 'Tracker' }).click()
    await tracker.getByLabel('Home account').check()
    await expect(now(tracker)).toContainText('9 tools (4 read, 5 change data)')
  })
})

test.describe('Skills that come with an add-on', () => {
  test('switching on turns on its skills with an Undo, and switching off turns off only those', async ({ page }) => {
    const s = await open(page, '/app/agents/a2/edit')
    const office = row(page, 'Office')
    await expect(office.getByTestId('agent-addon-skills').locator('summary')).toContainText('2 skills turn on with it')
    await office.getByRole('switch', { name: 'Office' }).click()
    const note = office.getByTestId('agent-addon-applied')
    await expect(note).toContainText('Office switched on')
    await expect(note).toContainText('2 skills turned on with it: pdf, xlsx')
    await expect(office.getByTestId('agent-addon-skills').locator('summary')).toContainText('2 of 2 skills on')
    await shot(page, 'switched-on')
    // Undo puts both back.
    await note.getByTestId('agent-addon-applied-undo').click()
    await expect(office.getByRole('switch', { name: 'Office' })).not.toBeChecked()
    await expect(note).toHaveCount(0)
    await office.getByRole('switch', { name: 'Office' }).click()
    await page.getByTestId('agent-addon-applied-keep').click()
    // One skill by hand, then the add-on off: only the add-on's own go; the personal one stays.
    await page.getByTestId('agent-skills-panel').getByRole('switch', { name: 'citations' }).click()
    await office.getByTestId('agent-addon-skills').locator('summary').click()
    await office.getByRole('checkbox', { name: 'xlsx' }).uncheck()
    await expect(office.getByTestId('agent-addon-skills').locator('summary')).toContainText('1 of 2 skills on')
    await office.getByRole('switch', { name: 'Office' }).click()
    await page.getByRole('button', { name: 'Save Changes' }).first().click()
    await expect.poll(() => s.calls.find(c => c.method === 'PUT')?.body).toMatchObject({ addons: [], skills: ['citations'] })
  })

  test('the standalone Skills list marks a skill with its add-on, and says when the add-on is off', async ({ page }) => {
    await open(page, '/app/agents/a2/edit')
    const skills = page.getByTestId('agent-skills-panel')
    await expect(skills.getByTestId('agent-skills-source').filter({ hasText: 'from Office' })).toHaveCount(2)
    await expect(skills.getByTestId('agent-skills-item').filter({ hasText: 'pdf' })).toContainText('Its add-on is off')
    await skills.getByRole('switch', { name: 'pdf' }).click()
    await expect(page.getByTestId('agent-effective').locator('[data-code=skill_addon_off]')).toContainText('does nothing')
    await row(page, 'Office').getByRole('switch', { name: 'Office' }).click()
    await expect(skills.getByTestId('agent-skills-item').filter({ hasText: 'pdf' })).not.toContainText('Its add-on is off')
  })
})

test.describe('Resources it may use', () => {
  test('the list is named with the words of the add-on, an empty list says none, and the picks are sent', async ({ page }) => {
    const s = await open(page)
    const tables = row(page, 'Tables')
    const list = tables.getByTestId('agent-addon-resources')
    await expect(list).toContainText('Which databases it may use')
    await expect(tables.getByTestId('agent-addon-resource')).toHaveCount(4)
    await expect(tables.getByTestId('agent-addon-resources-help')).toContainText('None ticked: it can use no database')
    await shot(page, 'resources')
    // Ticking one switches the add-on on, at read.
    await list.getByRole('checkbox', { name: 'Sales' }).check()
    await expect(tables.getByRole('switch', { name: 'Tables' })).toBeChecked()
    await expect(now(tables)).toContainText('on 1 ticked databases, read only')
    await list.getByRole('checkbox', { name: 'People' }).check()
    await tables.getByTestId('agent-addon-resource').filter({ hasText: 'People' }).getByTestId('agent-addon-resource-mode').selectOption('write')
    await expect(now(tables)).toContainText('on 2 ticked databases; it may change 1')
    // A viewer cannot be given the write mode.
    await list.getByRole('checkbox', { name: 'Ledger' }).check()
    await expect(tables.getByTestId('agent-addon-resource').filter({ hasText: 'Ledger' }).locator('option[value=write]')).toBeDisabled()
    await list.getByRole('checkbox', { name: 'Ledger' }).uncheck()
    await page.getByLabel('Name', { exact: false }).first().fill('Analyst')
    await save(page)
    await expect.poll(() => s.calls.find(c => c.method === 'PUT')?.body.resources).toEqual({ tables: [{ id: 'db-sales', mode: 'read' }, { id: 'db-people', mode: 'write' }] })
  })

  test('switching it on ticks all of them to read, so it works at once; unticking all makes it amber with the fix said', async ({ page }) => {
    await open(page)
    const tables = row(page, 'Tables')
    await tables.getByRole('switch', { name: 'Tables' }).click()
    await expect(tables.getByTestId('agent-addon-resource').getByRole('checkbox', { checked: true })).toHaveCount(4)
    await expect(now(tables)).toContainText('on 4 ticked databases, read only')
    for (const name of ['Sales', 'People', 'Ledger', 'Field notes']) await tables.getByRole('checkbox', { name }).uncheck()
    await expect(now(tables)).toHaveAttribute('data-tone', 'warn')
    await expect(now(tables)).toContainText('no database is ticked, so it can use none. Tick one, above.')
    const line = page.locator('[data-testid=agent-effective-line][data-code=agent_resources_none]')
    await expect(line).toContainText('no database is ticked')
    await expect(line.getByTestId('agent-effective-fix')).toBeVisible()
  })

  test('all read only is said in the Effective view with its code', async ({ page }) => {
    await open(page)
    const tables = row(page, 'Tables')
    await tables.getByTestId('agent-addon-resources').getByRole('checkbox', { name: 'Sales' }).check()
    const line = page.locator('[data-testid=agent-effective-line][data-code=agent_resources_read_only]')
    await expect(line).toContainText('may read 1 databases and change none')
  })

  test('a saved list is shown ticked with its mode, and a read that fails says so', async ({ page }) => {
    const agents = [{ ...AGENTS[1], id: 'a9', addons: ['tables'], resources: { tables: [{ id: 'db-sales', mode: 'write' }, { id: 'db-gone', mode: 'read' }] } }]
    await open(page, '/app/agents/a9/edit', { agents })
    const list = row(page, 'Tables').getByTestId('agent-addon-resources')
    await expect(list.getByRole('checkbox', { name: 'Sales' })).toBeChecked()
    await expect(list.getByTestId('agent-addon-resource').filter({ hasText: 'db-gone' })).toContainText('not available')
    await expect(now(row(page, 'Tables'))).toContainText('it may change 1')
  })

  test('a list that cannot be read is an error in the row, not an empty list', async ({ page }) => {
    await open(page, '/app/agents/a2/edit', { resourcesStatus: 500 })
    await expect(row(page, 'Tables').getByTestId('agent-addon-resources-error')).toContainText('could not be read')
  })
})

test.describe('MCP servers', () => {
  const connectors = Array.from({ length: 10 }, (_, i) => ({ id: `k${i}`, name: i === 3 ? 'Calendar' : `Source ${i}`, enabled: true }))

  test('with many connectors the picker is searchable, and a switch toggles the stored name', async ({ page }) => {
    const s = await installUses(page, { connectors, agents: [{ ...AGENTS[1], id: 'a7', mcp_servers: [] }] })
    await page.goto('/app/agents/a7/edit')
    const picker = page.getByTestId('agent-mcp-picker')
    await expect(picker.getByTestId('agent-mcp-row')).toHaveCount(10)
    await picker.getByTestId('agent-mcp-search').fill('calen')
    await expect(picker.getByTestId('agent-mcp-row')).toHaveCount(1)
    await picker.getByRole('switch', { name: 'Calendar' }).click()
    await page.getByRole('button', { name: 'Save Changes' }).first().click()
    await expect.poll(() => s.calls.find(c => c.method === 'PUT')?.body.mcp_servers).toEqual(['connector:k3'])
  })

  test('a stored name that nothing defined matches is amber, is not dropped, says it is ignored, and one click removes it', async ({ page }) => {
    const s = await installUses(page, { agents: [{ ...AGENTS[1], id: 'a7', model: 'qwen3-8b', mcp_servers: ['crm', 'typo', 'connector:zzz'] }] })
    await page.goto('/app/agents/a7/edit')
    const picker = page.getByTestId('agent-mcp-picker')
    const unknown = picker.getByTestId('agent-mcp-row-box').filter({ has: page.locator('[data-kind=unknown]') })
    await expect(unknown).toHaveCount(2)
    await expect(unknown.filter({ hasText: 'typo' })).toContainText('Not defined')
    await shot(page, 'mcp')
    const eff = page.locator('[data-testid=agent-effective-line][data-code=mcp_not_defined]')
    await expect(eff).toHaveCount(2)
    await expect(eff.first()).toContainText('is ignored')
    await unknown.filter({ hasText: 'typo' }).getByTestId('agent-mcp-remove').click()
    await expect(unknown).toHaveCount(1)
    await unknown.getByTestId('agent-mcp-remove').click()
    await expect(unknown).toHaveCount(0)
    await expect(eff).toHaveCount(0)
    await page.getByRole('button', { name: 'Save Changes' }).first().click()
    await expect.poll(() => s.calls.find(c => c.method === 'PUT')?.body.mcp_servers).toEqual(['crm'])
  })
})

test.describe('Memory skills', () => {
  // They used to be one line with no switch and no rows in the Skills list.
  // They are real rows now, in the Memory section and in the Skills list:
  // locked, a switch that shows the state and cannot be flipped, and the
  // provider as the source.
  test('are locked rows in Memory and in the Skills list, on while memory is on', async ({ page }) => {
    await open(page)
    const box = page.getByTestId('agent-memory-skills')
    await expect(box).toContainText('Memory skills')
    const rows = box.getByTestId('agent-memory-skill')
    await expect(rows).toHaveCount(2)
    await expect(rows.filter({ hasText: 'memory-check-own-files' })).toContainText('from Memory')
    await expect(rows.first()).toContainText('On while memory is on')
    for (const sw of await box.getByRole('switch').all()) {
      await expect(sw).toBeDisabled()
      await expect(sw).toBeChecked()
    }
    const list = page.getByTestId('agent-skills-panel').getByTestId('agent-skills-item')
    const inList = list.filter({ has: page.getByRole('switch', { name: 'memory-check-own-files', exact: true }) })
    await expect(inList).toHaveCount(1)
    await expect(inList).toHaveAttribute('data-locked', 'true')
    await expect(inList).toContainText('from Memory')
    await expect(inList.getByRole('switch')).toBeDisabled()
    await expect(inList.getByRole('switch')).toBeChecked()
    await shot(page, 'memory')
  })

  test('go off with the reason when the agent has no memory, and the bulk actions leave them alone', async ({ page }) => {
    const s = await open(page)
    const list = page.getByTestId('agent-skills-panel').getByTestId('agent-skills-item')
    const mem = list.filter({ has: page.getByRole('switch', { name: 'memory', exact: true }) })
    await page.getByTestId('agent-memory-level-off').click()
    await expect(mem.getByRole('switch')).not.toBeChecked()
    await expect(mem).toContainText('Off while this agent')
    await expect(page.getByTestId('agent-memory-skills')).toContainText('Off while this agent')
    // Select all does not reach a locked row: it stays off while memory is off.
    await page.getByTestId('agent-skills-all').click()
    await expect(mem.getByRole('switch')).not.toBeChecked()
    await page.getByTestId('agent-memory-level-reads').click()
    await expect(mem.getByRole('switch')).toBeChecked()
    await page.getByTestId('agent-skills-none').click()
    await expect(mem.getByRole('switch')).toBeChecked()
    expect(s.calls.some(c => c.method === 'PUT' || c.method === 'POST')).toBe(false)
  })
})

test.describe('Effective view', () => {
  test('names the account an add-on uses and blocks one that has none, with the fix on this form', async ({ page }) => {
    await open(page)
    await row(page, 'Tracker').getByRole('switch', { name: 'Tracker' }).click()
    const eff = page.getByTestId('agent-effective')
    const blocked = eff.locator('[data-code=addon_account_missing]')
    await expect(blocked).toContainText('Tracker is on but cannot work now: no account is chosen')
    await expect(blocked.getByTestId('agent-effective-fix')).toHaveText('Fix it here')
    await expect(page.getByTestId('agent-effective-summary')).toContainText('blocked')
    await row(page, 'Tracker').getByLabel('Work account').check()
    await expect(eff.locator('[data-line=addon-account-tracker]')).toContainText('Tracker uses Work account')
    await expect(eff.locator('[data-code=addon_account_missing]')).toHaveCount(0)
  })
})
