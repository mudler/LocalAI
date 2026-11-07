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
async function server(page, { setups = null, agents = [], missingApi = false } = {}) {
  const seen = { created: [], puts: [] }
  let doc = setups ? wk(setups, 3) : wk({}, 0)
  await page.route('**/api/chats/wellknown/setups', async route => {
    if (missingApi) return route.fulfill({ status: 404, json: { error: { message: 'unknown well-known file' } } })
    if (route.request().method() === 'PUT') {
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
  await page.route(/\/api\/chats\/n1\/messages(\?.*)?$/, route => (route.request().method() === 'POST'
    ? route.fulfill({ json: { started: true } })
    : route.fulfill({ json: { messages: [] } })))
  await page.route('**/api/chats/n1/events', route => route.abort())
  await page.route('**/api/chats/n1', route => route.fulfill({ json: { ...CONV, pending: [] } }))
  await page.route(/\/api\/chats\/draft\/tools(\?.*)?$/, route => route.fulfill({ json: {
    builtins: [
      { group: 'web', tools: ['web_search'], off: false, usable: true, provider: 'builtin', providers: {} },
      { group: 'files', tools: ['read_file'], off: false, usable: true, provider: 'builtin', providers: {} },
    ],
    addons: [{ id: 'office', tools: 4, off: false }], servers: [], connectors: [], off: [],
  } }))
  await page.route(/\/api\/chats\/draft\/addons(\?.*)?$/, route => route.fulfill({ json: { addons: [{ id: 'office', tools: 4, off: false }], commands: 'none' } }))
  await page.route(/\/api\/chats\/skills(\?.*)?$/, route => route.fulfill({ json: { skills: [] } }))
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
  test.beforeEach(async ({ page }) => {
    await mockHome(page)
  })

  test('first message creates My default and offers Rename', async ({ page }) => {
    const seen = await server(page)
    await open(page)
    await expect(chipName(page)).toHaveText('Setup')
    await expect(page.getByTestId('chat-ready')).toBeVisible()
    await page.getByTestId('home-input').fill('hello')
    await page.getByTestId('home-send').click()
    await expect(page).toHaveURL(/\/app\/chats\/n1$/)
    await expect.poll(() => seen.puts.length).toBe(2)
    expect(seen.puts[0].setups).toHaveLength(1)
    expect(seen.puts[0].setups[0]).toMatchObject({ id: 'my-default', name: 'My default', model: 'qwen3-8b-instruct' })
    expect(seen.puts[1].default).toBe('my-default')
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
    await expect(line(page)).toContainText('Coding · gemma-4-e4b-it-qat-q4_0 · Your files · Effort High')
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
    await server(page, {
      setups: { default: 'coding', setups: [CODING] },
      agents: [{ id: 'a1', name: 'Reviewer', model: 'qwen3-32b-instruct' }],
    })
    await open(page)
    await expect(chipName(page)).toHaveText('Coding')
    await page.getByTestId('chat-agent-picker').getByRole('button').click()
    await page.getByRole('radio', { name: 'Reviewer' }).click()
    await expect(page.getByTestId('home-model-chip')).toContainText('qwen3-32b-instruct')
    const chip = page.getByTestId('setup-chip-button')
    await expect(chip).toBeDisabled()
    await expect(chip).toHaveAttribute('title', /saved agent/)
    // The status line falls back to the ready line.
    await expect(line(page)).toHaveCount(0)
    await expect(page.getByTestId('chat-ready')).toBeVisible()
  })

  test('a missing well-known API hides the chip', async ({ page }) => {
    await server(page, { missingApi: true })
    await open(page)
    await expect(page.getByTestId('chat-ready')).toBeVisible()
    await expect(page.getByTestId('setup-chip')).toHaveCount(0)
    await expect(page.getByTestId('chat-tools-button')).toBeVisible()
  })

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
})
