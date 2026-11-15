import { test, expect } from './coverage-fixtures.js'
import { mockHome } from './home-fixtures.js'

// Setups on Home: a named model, tool switches and effort that a new chat
// starts from, kept in LocalAI/setups.json. The API is stubbed in the shapes
// of the server (core/schema/chats.go and the well-known files); the saved
// document is kept in a variable so a PUT shows in the next GET.

const CONV = { id: 'n1', title: 'New chat', model: 'qwen3-8b-instruct', kind: 'chat', created: '2026-10-30T10:00:00Z', updated: '2026-10-30T10:00:00Z', running: false }

const CODING = { id: 'coding', name: 'Coding', model: 'gemma-4-e4b-it-qat-q4_0', effort: 'high', tools: { builtins_off: ['web'] } }
const WRITING = { id: 'writing', name: 'Writing', model: 'qwen3-32b-instruct', effort: 'low', tools: { addons_off: ['office'] } }

const wk = (data, version) => ({ name: 'setups', path: '/LocalAI/setups.json', exists: !!version, version, data, valid: true, errors: [] })

// The chat and tools stubs of home-composer.spec.js, and the setups file.
async function server(page, { hold = {}, ctl = {}, setups = null, agents = [], missingApi = false, servers = [], failSend = false } = {}) {
  const seen = { created: [], puts: [], updates: [], conflicts: 0 }
  let doc = setups ? wk(setups, 3) : wk({}, 0)
  await page.route('**/api/chats/wellknown/setups', async route => {
    if (missingApi) return route.fulfill({ status: missingApi === true ? 404 : missingApi, json: { error: { message: 'unknown well-known file' } } })
    if (route.request().method() === 'PUT') {
      if (ctl.failPut) return route.fulfill({ status: 500, json: { error: { message: 'the file is read only' } } })
      if (ctl.race) {
        // Another tab wrote first: the newer file has a setup already.
        seen.conflicts++
        const current = wk({ default: 'other', setups: [{ id: 'other', name: 'Other', model: 'qwen3-8b-instruct', effort: '' }] }, 5)
        ctl.race = false
        doc = current
        return route.fulfill({ status: 409, json: { error: { message: 'the file changed' }, current } })
      }
      const body = route.request().postDataJSON()
      seen.puts.push(body.values)
      doc = wk(body.values, doc.version + 1)
      return route.fulfill({ json: doc })
    }
    return route.fulfill({ json: doc })
  })
  await page.route(/\/api\/chats(\?.*)?$/, async route => {
    if (route.request().method() === 'POST') {
      const body = route.request().postDataJSON()
      seen.created.push(body)
      return route.fulfill({ status: 201, json: { ...CONV, model: body.model || CONV.model } })
    }
    return route.fulfill({ json: { conversations: [], persistent: true, files: true } })
  })
  await page.route(/\/api\/chats\/n1\/messages(\?.*)?$/, route => {
    if (route.request().method() !== 'POST') return route.fulfill({ json: { messages: [] } })
    return failSend
      ? route.fulfill({ status: 413, json: { error: { message: 'the file is too large' } } })
      : route.fulfill({ json: { started: true } })
  })
  await page.route('**/api/chats/n1/events', route => route.abort())
  await page.route('**/api/chats/n1', route => {
    if (route.request().method() !== 'GET') seen.updates.push(route.request().postDataJSON())
    return route.fulfill({ json: { ...CONV, pending: [] } })
  })
  await page.route(/\/api\/chats\/draft\/tools(\?.*)?$/, async route => { if (hold.p) await hold.p; return route.fulfill({ json: {
    builtins: [
      { group: 'web', tools: ['web_search'], off: false, usable: true, provider: 'builtin', providers: {} },
      { group: 'files', tools: ['read_file'], off: false, usable: true, provider: 'builtin', providers: {} },
    ],
    addons: [{ id: 'office', tools: 4, off: false }], servers, connectors: [], off: [],
  } }) })
  await page.route(/\/api\/chats\/draft\/addons(\?.*)?$/, async route => { if (hold.p) await hold.p; return route.fulfill({ json: { addons: [{ id: 'office', tools: 4, off: false }], commands: 'none' } }) })
  await page.route(/\/api\/chats\/skills(\?.*)?$/, async route => { if (hold.p) await hold.p; return route.fulfill({ json: { skills: [] } }) })
  await page.route('**/api/chats/agents', route => route.fulfill({ json: { agents } }))
  return seen
}

async function open(page) {
  await page.goto('/app')
  await expect(page.locator('.home-greeting')).toBeVisible({ timeout: 15_000 })
  await expect(page.getByTestId('home-model-chip')).toBeVisible()
}

const chipName = (page) => page.getByTestId('setup-chip-name')
const line = (page) => page.getByTestId('setup-line')

async function pick(page, id) {
  await page.getByTestId('setup-chip-button').click()
  await page.getByTestId(`setup-option-${id}`).click()
}

async function toolSwitch(page, name) {
  await page.getByTestId('chat-tools-button').click()
  return page.getByTestId('chat-tools-menu').getByRole('switch', { name, exact: true })
}

test.describe('Home setups', () => {
  let problems
  test.beforeEach(async ({ page }) => {
    // React says so in the console when markup is nested wrongly.
    problems = []
    page.on('console', (m) => {
      if (['error', 'warning'].includes(m.type()) && /validateDOMNesting|cannot be a descendant|cannot contain|hydrat|unique "key"/i.test(m.text())) problems.push(m.text())
    })
    await mockHome(page)
  })
  test.afterEach(() => {
    expect(problems).toEqual([])
  })

  test('first message creates My default and offers Rename', async ({ page }) => {
    const seen = await server(page)
    await open(page)
    await expect(chipName(page)).toHaveText('Setup')
    await expect(page.getByTestId('chat-ready')).toBeVisible()
    await page.getByTestId('home-input').fill('hello')
    await page.getByTestId('home-send').click()
    await expect(page).toHaveURL(/\/app\/chats\/n1$/)
    await expect.poll(() => seen.puts.length).toBe(1)
    expect(seen.puts[0].setups).toHaveLength(1)
    expect(seen.puts[0].setups[0]).toMatchObject({ id: 'my-default', name: 'My default', model: 'qwen3-8b-instruct' })
    expect(seen.puts[0].default).toBe('my-default')
    const rename = page.getByRole('button', { name: 'Rename' })
    await expect(rename).toBeVisible()
    await rename.click()
    await expect(page.getByTestId('setup-manage-sheet')).toBeVisible()
    await expect(page.getByTestId('setup-manage-name-my-default')).toHaveValue('My default')
  })

  test('the default setup is applied on open', async ({ page }) => {
    await server(page, { setups: { default: 'coding', setups: [CODING, WRITING] } })
    await open(page)
    await expect(chipName(page)).toHaveText('Coding')
    await expect(page.getByTestId('home-model-chip')).toContainText('gemma-4-e4b-it-qat-q4_0')
    await expect(line(page)).toContainText('Coding · gemma-4-e4b-it-qat-q4_0 · Your files, office · Effort High')
    await expect(page.getByTestId('setup-chip-edited')).toHaveCount(0)
    await expect(await toolSwitch(page, 'Look things up online')).toBeVisible()
  })

  test('picking a setup sets model, tools and effort', async ({ page }) => {
    await server(page, { setups: { default: 'coding', setups: [CODING, WRITING] } })
    await open(page)
    await expect(chipName(page)).toHaveText('Coding')
    await pick(page, 'writing')
    await expect(chipName(page)).toHaveText('Writing')
    await expect(page.getByTestId('home-model-chip')).toContainText('qwen3-32b-instruct')
    await expect(page.getByTestId('chat-effort-button')).toContainText('Low')
    await expect(line(page)).toContainText('Effort Low')
    await expect(page.getByTestId('setup-chip-edited')).toHaveCount(0)
    const web = await toolSwitch(page, 'Look things up online')
    await expect(web).toHaveAttribute('aria-checked', 'true')
  })

  test('a changed switch shows edited, Update saves it, Reset restores it', async ({ page }) => {
    const seen = await server(page, { setups: { default: 'coding', setups: [CODING, WRITING] } })
    await open(page)
    await expect(chipName(page)).toHaveText('Coding')
    let files = await toolSwitch(page, 'Your files')
    await expect(files).toHaveAttribute('aria-checked', 'true')
    await files.click()
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('setup-chip-edited')).toBeVisible()
    await expect(page.getByTestId('setup-update')).toHaveText('Update “Coding”')
    // Reset puts the switch back.
    await page.getByTestId('setup-reset').click()
    await expect(page.getByTestId('setup-chip-edited')).toHaveCount(0)
    files = await toolSwitch(page, 'Your files')
    await expect(files).toHaveAttribute('aria-checked', 'true')
    await files.click()
    await page.keyboard.press('Escape')
    await page.getByTestId('setup-update').click()
    await expect(page.getByTestId('setup-chip-edited')).toHaveCount(0)
    expect(seen.puts).toHaveLength(1)
    expect(seen.puts[0].setups[0]).toMatchObject({ id: 'coding', model: 'gemma-4-e4b-it-qat-q4_0', effort: 'high', tools: { builtins_off: ['files', 'web'] } })
    expect(seen.puts[0].setups[1]).toEqual(WRITING)
  })

  test('Save as new asks for a name and adds a chip entry', async ({ page }) => {
    const seen = await server(page, { setups: { default: 'coding', setups: [CODING] } })
    await open(page)
    await page.getByTestId('chat-effort-button').click()
    await page.getByTestId('chat-effort-low').click()
    await expect(page.getByTestId('setup-chip-edited')).toBeVisible()
    await page.getByTestId('setup-save-new').click()
    await page.getByTestId('setup-name-input').fill('  Quick   draft ')
    await page.getByTestId('setup-name-save').click()
    await expect(chipName(page)).toHaveText('Quick draft')
    await expect(page.getByTestId('setup-chip-edited')).toHaveCount(0)
    expect(seen.puts[0].setups.map(s => s.name)).toEqual(['Coding', 'Quick draft'])
    expect(seen.puts[0].setups[1]).toMatchObject({ model: 'gemma-4-e4b-it-qat-q4_0', effort: 'low' })
    await page.getByTestId('setup-chip-button').click()
    await expect(page.getByTestId('setup-option-quick-draft')).toBeVisible()
    await expect(page.getByTestId('setup-option-coding')).toBeVisible()
  })

  test('the menu saves the current state under a name when no setup is in use', async ({ page }) => {
    const seen = await server(page)
    await open(page)
    await page.getByTestId('setup-chip-button').click()
    await expect(page.getByTestId('setup-chip-menu')).toContainText('No setups yet')
    await expect(page.getByTestId('setup-manage')).toHaveCount(0)
    await page.getByTestId('setup-save-as').click()
    await page.getByTestId('setup-name-input').fill('Plain')
    await page.keyboard.press('Enter')
    await expect(chipName(page)).toHaveText('Plain')
    expect(seen.puts[0].setups[0]).toMatchObject({ id: 'plain', name: 'Plain', model: 'qwen3-8b-instruct' })
  })

  test('a setup whose model is not installed keeps the current model and shows a warning', async ({ page }) => {
    const seen = await server(page, { setups: { default: 'ghost', setups: [{ id: 'ghost', name: 'Ghost', model: 'gone-model-7b', effort: 'medium' }] } })
    await open(page)
    await expect(chipName(page)).toHaveText('Ghost')
    await expect(page.getByTestId('home-model-chip')).toContainText('qwen3-8b-instruct')
    await expect(page.getByTestId('setup-warning')).toContainText('gone-model-7b')
    // The kept model does not make the setup edited, and nothing is saved.
    await expect(page.getByTestId('setup-chip-edited')).toHaveCount(0)
    expect(seen.puts).toHaveLength(0)
  })

  test('a picked saved agent makes the chip inactive and the agent model wins', async ({ page }) => {
    const seen = await server(page, {
      setups: { default: 'coding', setups: [CODING] },
      agents: [{ id: 'a1', name: 'Reviewer', model: 'qwen3-32b-instruct' }],
    })
    await open(page)
    await expect(chipName(page)).toHaveText('Coding')
    await page.getByTestId('chat-agent-picker').getByRole('button').click()
    await page.getByRole('radio', { name: 'Reviewer' }).click()
    await expect(page.getByTestId('home-model-chip')).toContainText('qwen3-32b-instruct')
    await page.getByTestId('home-input').fill('Review this')
    await page.getByTestId('home-send').click()
    await expect(page).toHaveURL(/\/app\/chats\/n1$/)
    // The setup's tool switches and effort do not go with the agent chat.
    expect(seen.created[0].agent_id).toBe('a1')
    expect(seen.created[0]).not.toHaveProperty('switches')
    expect(seen.updates).toEqual([])
  })

  test('a picked saved agent shows the chip off; dropping the agent brings the setup back', async ({ page }) => {
    await server(page, {
      setups: { default: 'coding', setups: [CODING] },
      agents: [{ id: 'a1', name: 'Reviewer', model: 'qwen3-32b-instruct' }],
    })
    await open(page)
    await expect(chipName(page)).toHaveText('Coding')
    await page.getByTestId('chat-agent-picker').getByRole('button').click()
    await page.getByRole('radio', { name: 'Reviewer' }).click()
    const chip = page.getByTestId('setup-chip-button')
    await expect(chip).toBeDisabled()
    await expect(page.getByTestId('setup-reason')).toBeVisible()
    await expect(page.getByTestId('setup-reason')).toContainText('A saved agent decides')
    await expect(chip).toHaveAttribute('aria-describedby', 'setup-chip-reason')
    // The status line falls back to the ready line.
    await expect(line(page)).toHaveCount(0)
    await expect(page.getByTestId('chat-ready')).toBeVisible()
    // The agent comes off: the setup's effort and switches are back.
    await page.getByTestId('chat-agent-picker').getByRole('button').click()
    await page.getByRole('radio', { name: 'No agent' }).click()
    await expect(chip).toBeEnabled()
    await expect(chipName(page)).toHaveText('Coding')
    await expect(page.getByTestId('chat-effort-button')).toContainText('High')
    await expect(await toolSwitch(page, 'Look things up online')).toHaveAttribute('aria-checked', 'false')
    await expect(page.getByTestId('setup-chip-edited')).toHaveCount(0)
  })

  test('a missing well-known API hides the chip', async ({ page }) => {
    await server(page, { missingApi: true })
    await open(page)
    await expect(page.getByTestId('chat-ready')).toContainText('qwen3-8b-instruct')
    await expect(page.getByTestId('setup-chip')).toHaveCount(0)
    await expect(page.getByTestId('setup-line')).toHaveCount(0)
    // The row is as it was: the model chip first, then Tools.
    const top = page.locator('.home-cmd__top')
    await expect(top.locator(':scope > *').first()).toHaveClass(/home-pick/)
    await expect(page.getByTestId('chat-tools-button')).toBeVisible()
  })

  for (const status of [403, 500]) {
    test(`a ${status} from the setups API hides the chip as well`, async ({ page }) => {
      await server(page, { missingApi: status })
      await open(page)
      await expect(page.getByTestId('chat-ready')).toContainText('qwen3-8b-instruct')
      await expect(page.getByTestId('setup-chip')).toHaveCount(0)
    })
  }

  test('Manage renames, reorders, deletes and sets the default', async ({ page }) => {
    const seen = await server(page, { setups: { default: 'coding', setups: [CODING, WRITING] } })
    await open(page)
    await page.getByTestId('setup-chip-button').click()
    await page.getByTestId('setup-manage').click()
    const sheet = page.getByTestId('setup-manage-sheet')
    await expect(sheet).toBeVisible()
    await expect(page.getByTestId('setup-manage-default-coding')).toBeVisible()

    const name = page.getByTestId('setup-manage-name-writing')
    await name.fill('Essays')
    await name.press('Enter')
    await expect.poll(() => seen.puts.length).toBe(1)
    expect(seen.puts[0].setups[1].name).toBe('Essays')

    await page.getByTestId('setup-manage-up-writing').click()
    await expect.poll(() => seen.puts.length).toBe(2)
    expect(seen.puts[1].setups.map(s => s.id)).toEqual(['writing', 'coding'])

    await page.getByTestId('setup-manage-make-default-writing').click()
    await expect.poll(() => seen.puts.length).toBe(3)
    expect(seen.puts[2].default).toBe('writing')
    await expect(page.getByTestId('setup-manage-default-writing')).toBeVisible()

    await page.getByTestId('setup-manage-remove-coding').click()
    await page.getByTestId('setup-manage-remove-yes-coding').click()
    await expect.poll(() => seen.puts.length).toBe(4)
    expect(seen.puts[3].setups.map(s => s.id)).toEqual(['writing'])
    await expect(page.getByTestId('setup-manage-row-coding')).toHaveCount(0)

    await page.getByTestId('setup-manage-done').click()
    await expect(sheet).toHaveCount(0)
    // The setup in use was deleted: the chip goes back to Setup.
    await expect(chipName(page)).toHaveText('Setup')
  })

  test('a setup naming an unknown tool is applied without it', async ({ page }) => {
    await server(page, { setups: { default: 'odd', setups: [{ id: 'odd', name: 'Odd', model: 'qwen3-8b-instruct', effort: '', tools: { builtins_off: ['nonesuch', 'web'], mcp_off: ['gone'], addons_off: ['missing'] } }] } })
    await open(page)
    await expect(chipName(page)).toHaveText('Odd')
    await expect(page.locator('.toast-error')).toHaveCount(0)
    await expect(await toolSwitch(page, 'Look things up online')).toHaveAttribute('aria-checked', 'false')
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('setup-chip-edited')).toHaveCount(0)
  })

  test('the tools summary of the line agrees with the Tools badge', async ({ page }) => {
    await server(page, {
      servers: [{ name: 'crm', off: false, usable: true }],
      setups: { default: 'addon', setups: [
        { id: 'addon', name: 'Addon only', model: 'qwen3-8b-instruct', effort: '', tools: { builtins_off: ['web', 'files'], mcp_off: ['crm'] } },
        { id: 'mcp', name: 'Server only', model: 'qwen3-8b-instruct', effort: '', tools: { builtins_off: ['web', 'files'], addons_off: ['office'] } },
        { id: 'all', name: 'Everything', model: 'qwen3-8b-instruct', effort: '' },
      ] },
    })
    await open(page)
    const text = page.getByTestId('setup-line-text')
    await expect(chipName(page)).toHaveText('Addon only')
    await expect(text).toContainText('· office ·')
    await expect(page.getByTestId('chat-tools-count')).toHaveText('1')
    await pick(page, 'mcp')
    await expect(text).toContainText('· crm ·')
    await expect(page.getByTestId('chat-tools-count')).toHaveText('1')
    await pick(page, 'all')
    await expect(text).toContainText('· 4 tools ·')
    await expect(page.getByTestId('chat-tools-count')).toHaveText('4')
    await expect(text).not.toContainText('No tools')
  })

  test('a switch changed right after a setup is applied still shows edited', async ({ page }) => {
    await server(page, { setups: { default: 'coding', setups: [CODING, WRITING] } })
    await open(page)
    await pick(page, 'writing')
    // No wait for the lists: the change goes in as soon as the menu allows.
    const files = await toolSwitch(page, 'Your files')
    await files.click()
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('setup-chip-edited')).toBeVisible()
    await page.waitForTimeout(500)
    await expect(page.getByTestId('setup-chip-edited')).toBeVisible()
  })

  test('Manage: an empty name goes back, the end buttons are disabled and focus is handed on', async ({ page }) => {
    const seen = await server(page, { setups: { default: 'coding', setups: [CODING, WRITING] } })
    await open(page)
    await page.getByTestId('setup-chip-button').click()
    await page.getByTestId('setup-manage').click()
    const name = page.getByTestId('setup-manage-name-coding')
    await name.fill('   ')
    await name.press('Enter')
    await expect(name).toHaveValue('Coding')
    expect(seen.puts).toHaveLength(0)

    await expect(page.getByTestId('setup-manage-up-coding')).toBeDisabled()
    await expect(page.getByTestId('setup-manage-down-writing')).toBeDisabled()
    // Moving to the end disables the pressed button: the focus goes to the name.
    await page.getByTestId('setup-manage-down-coding').click()
    await expect(page.getByTestId('setup-manage-down-coding')).toBeDisabled()
    await expect(page.getByTestId('setup-manage-name-coding')).toBeFocused()
    // Deleting a row hands the focus to its neighbour.
    await page.getByTestId('setup-manage-remove-coding').click()
    await page.getByTestId('setup-manage-remove-yes-coding').click()
    await expect(page.getByTestId('setup-manage-row-coding')).toHaveCount(0)
    await expect(page.getByTestId('setup-manage-name-writing')).toBeFocused()
  })

  test('the 51st setup is refused with a plain message and nothing is saved', async ({ page }) => {
    const many = Array.from({ length: 50 }, (_, i) => ({ id: `s${i}`, name: `Setup ${i}`, model: 'qwen3-8b-instruct', effort: '' }))
    const seen = await server(page, { setups: { default: 's0', setups: many } })
    await open(page)
    await page.getByTestId('chat-effort-button').click()
    await page.getByTestId('chat-effort-low').click()
    await page.getByTestId('setup-save-new').click()
    await page.getByTestId('setup-name-input').fill('One too many')
    await page.getByTestId('setup-name-save').click()
    await expect(page.getByText('You can have up to 50 setups.')).toBeVisible()
    expect(seen.puts).toHaveLength(0)
  })

  test('a failed send creates no setup', async ({ page }) => {
    const seen = await server(page, { failSend: true })
    await open(page)
    await page.getByTestId('home-input').fill('hello')
    await page.getByTestId('home-send').click()
    await expect(page.getByText('the file is too large')).toBeVisible()
    await page.waitForTimeout(500)
    expect(seen.puts).toHaveLength(0)
    await expect(page).not.toHaveURL(/\/app\/chats\/n1$/)
  })

  test('sending with another setup in use makes it the default', async ({ page }) => {
    const seen = await server(page, { setups: { default: 'coding', setups: [CODING, WRITING] } })
    await open(page)
    await pick(page, 'writing')
    await page.getByTestId('home-input').fill('hello')
    await page.getByTestId('home-send').click()
    await expect(page).toHaveURL(/\/app\/chats\/n1$/)
    await expect.poll(() => seen.puts.length).toBe(1)
    expect(seen.puts[0]).toEqual({ default: 'writing', setups: [CODING, WRITING] })
  })

  test('the missing-model warning goes once another model is picked', async ({ page }) => {
    await server(page, { setups: { default: 'ghost', setups: [{ id: 'ghost', name: 'Ghost', model: 'gone-model-7b', effort: '' }] } })
    await open(page)
    await expect(page.getByTestId('setup-warning')).toBeVisible()
    await page.getByTestId('home-model-chip').click()
    await page.getByRole('option', { name: /gemma-4-e4b/ }).click()
    await expect(page.getByTestId('home-model-chip')).toContainText('gemma-4-e4b')
    await expect(page.getByTestId('setup-warning')).toHaveCount(0)
    await expect(page.getByTestId('setup-chip-edited')).toBeVisible()
  })

  test('a switch clicked while the lists of a setup are still loading stays an edit', async ({ page }) => {
    const hold = {}
    await server(page, { hold, setups: { default: 'coding', setups: [CODING, WRITING] } })
    await open(page)
    await expect(chipName(page)).toHaveText('Coding')
    // The lists of the first setup are shown; from here they answer only when released.
    await expect(page.getByTestId('chat-tools-count')).toBeVisible()
    let release
    hold.p = new Promise((r) => { release = r })
    await pick(page, 'writing')
    // The old lists are still shown: switch one while the new ones load.
    const files = await toolSwitch(page, 'Your files')
    await expect(files).toHaveAttribute('aria-checked', 'true')
    await files.click()
    await page.keyboard.press('Escape')
    release()
    await expect(chipName(page)).toHaveText('Writing')
    await expect(page.getByTestId('setup-chip-edited')).toBeVisible()
    await page.waitForTimeout(500)
    await expect(page.getByTestId('setup-chip-edited')).toBeVisible()
    await expect(await toolSwitch(page, 'Your files')).toHaveAttribute('aria-checked', 'false')
    // The setup's own switch (the add-on is off) was loaded as well.
    await expect(page.getByTestId('chat-tools-menu').getByRole('switch', { name: 'office', exact: true })).toHaveAttribute('aria-checked', 'false')
  })

  test('deleting the only setup in Manage moves the focus to Done', async ({ page }) => {
    await server(page, { setups: { default: 'coding', setups: [CODING] } })
    await open(page)
    await page.getByTestId('setup-chip-button').click()
    await page.getByTestId('setup-manage').click()
    await expect(page.getByTestId('setup-manage-up-coding')).toBeDisabled()
    await page.getByTestId('setup-manage-remove-coding').click()
    await page.getByTestId('setup-manage-remove-yes-coding').click()
    await expect(page.getByTestId('setup-manage-row-coding')).toHaveCount(0)
    await expect(page.getByTestId('setup-manage-done')).toBeFocused()
  })

  test('deleting every setup and then sending creates nothing', async ({ page }) => {
    const seen = await server(page, { setups: { default: 'coding', setups: [CODING] } })
    await open(page)
    await page.getByTestId('setup-chip-button').click()
    await page.getByTestId('setup-manage').click()
    await page.getByTestId('setup-manage-remove-coding').click()
    await page.getByTestId('setup-manage-remove-yes-coding').click()
    await expect(page.getByTestId('setup-manage-row-coding')).toHaveCount(0)
    await page.getByTestId('setup-manage-done').click()
    await page.getByTestId('home-input').fill('hello')
    await page.getByTestId('home-send').click()
    await expect(page).toHaveURL(/\/app\/chats\/n1$/)
    await page.waitForTimeout(500)
    expect(seen.puts).toHaveLength(1)
    expect(seen.puts[0].setups).toEqual([])
    await expect(page.getByRole('button', { name: 'Rename' })).toHaveCount(0)
  })

  test('a second tab that already saved a setup is not given a second one', async ({ page }) => {
    const ctl = { race: true }
    const seen = await server(page, { ctl })
    await open(page)
    await page.getByTestId('home-input').fill('hello')
    await page.getByTestId('home-send').click()
    await expect(page).toHaveURL(/\/app\/chats\/n1$/)
    await expect.poll(() => seen.conflicts).toBe(1)
    await page.waitForTimeout(500)
    // The retry saw the newer file and stood down: nothing was written.
    expect(seen.puts).toHaveLength(0)
    await expect(page.getByRole('button', { name: 'Rename' })).toHaveCount(0)
  })

  test('an invalid file with no history is not replaced by My default', async ({ page }) => {
    const seen = await server(page, { setups: null })
    // The file exists and is invalid: the server reads it as empty data.
    await page.route('**/api/chats/wellknown/setups', route => {
      if (route.request().method() === 'PUT') { seen.puts.push('invalid-overwrite'); return route.fulfill({ status: 500, json: {} }) }
      return route.fulfill({ json: { name: 'setups', path: '/LocalAI/setups.json', exists: true, version: 4, data: {}, valid: false, errors: ['setups: wrong type'] } })
    })
    await open(page)
    await page.getByTestId('home-input').fill('hello')
    await page.getByTestId('home-send').click()
    await expect(page).toHaveURL(/\/app\/chats\/n1$/)
    await page.waitForTimeout(500)
    expect(seen.puts).toEqual([])
  })

  test('a refused save keeps the typed name', async ({ page }) => {
    const ctl = {}
    await server(page, { ctl, setups: { default: 'coding', setups: [CODING] } })
    await open(page)
    await page.getByTestId('chat-effort-button').click()
    await page.getByTestId('chat-effort-low').click()
    await page.getByTestId('setup-save-new').click()
    await page.getByTestId('setup-name-input').fill('Keep me')
    ctl.failPut = true
    await page.getByTestId('setup-name-save').click()
    await expect(page.getByText('the file is read only')).toBeVisible()
    await expect(page.getByTestId('setup-name-input')).toHaveValue('Keep me')
    // Tried again once the file takes writes.
    ctl.failPut = false
    await page.getByTestId('setup-name-save').click()
    await expect(chipName(page)).toHaveText('Keep me')
  })

  test('a refused rename in Manage keeps the typed name for another try', async ({ page }) => {
    const ctl = {}
    await server(page, { ctl, setups: { default: 'coding', setups: [CODING] } })
    await open(page)
    await page.getByTestId('setup-chip-button').click()
    await page.getByTestId('setup-manage').click()
    ctl.failPut = true
    const name = page.getByTestId('setup-manage-name-coding')
    await name.fill('Renamed')
    await name.press('Enter')
    await expect(page.getByText('the file is read only')).toBeVisible()
    await expect(name).toHaveValue('Renamed')
  })
})
