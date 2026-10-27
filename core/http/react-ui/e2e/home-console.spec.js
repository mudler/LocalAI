import { test, expect } from './coverage-fixtures.js'
import { mockHome, mockUser, LOADED } from './home-fixtures.js'
import { installStudioAddons } from './studio-addon.js'

// The Home console: command bar, slash menu, memory strip, resume list, first
// run and the phone layout. Data is stubbed the way the other Home specs do it;
// the resume strip reads the newest chat the server keeps.

const GB = 1024 * 1024 * 1024

async function ready(page) {
  await page.goto('/app')
  await expect(page.locator('.home-greeting')).toBeVisible({ timeout: 15_000 })
}

test.describe('Home slash menu', () => {
  test.beforeEach(async ({ page }) => {
    await mockHome(page)
  })

  test('typing a slash opens the grouped action list', async ({ page }) => {
    await ready(page)
    await page.locator('.home-textarea').fill('/')
    const list = page.getByRole('listbox', { name: 'Actions' })
    await expect(list).toBeVisible()
    // Grouped, with a label above each group.
    await expect(list.locator('.dk-cmd-label').first()).toHaveText('Chat')
    await expect(list.getByRole('option').first()).toHaveAttribute('aria-selected', 'true')
  })

  test('only actions with a destination are offered', async ({ page }) => {
    await ready(page)
    await page.locator('.home-textarea').fill('/')
    const names = await page.locator('.dk-cmd-name').allTextContents()
    expect(names).toEqual([
      '/model', '/new', '/chat', '/assistant',
      '/gallery', '/installed', '/import', '/stop',
      '/agents', '/mcp', '/memory', '/settings', '/docs',
    ])
  })

  test('the /agents, /mcp and /memory commands open their pages', async ({ page }) => {
    for (const [cmd, url] of [['agents', /\/app\/agents$/], ['mcp', /\/app\/mcp-connectors$/], ['memory', /\/app\/collections$/]]) {
      await ready(page)
      await page.locator('.home-textarea').fill('/')
      await page.getByTestId(`home-slash-${cmd}`).click()
      await expect(page).toHaveURL(url)
    }
  })

  // Studio is an add-on: its command is offered only while it is installed.
  test('the /studio command appears once the Studio add-on is installed', async ({ page }) => {
    await installStudioAddons(page, { ids: ['studio'] })
    await ready(page)
    await page.locator('.home-textarea').fill('/')
    await expect(page.getByTestId('home-slash-studio')).toBeVisible()
    await page.getByTestId('home-slash-studio').click()
    await expect(page).toHaveURL(/\/app\/addons\/studio\/studio/)
  })

  test('the /studio command is not offered while the Studio add-on is not installed', async ({ page }) => {
    await ready(page)
    await page.locator('.home-textarea').fill('/stu')
    await expect(page.getByTestId('home-slash-studio')).toHaveCount(0)
  })

  // The LocalAI assistant is a chat started with the management tools.
  test('/assistant starts an assistant chat and opens it', async ({ page }) => {
    let body = null
    await page.route('**/api/chats', route => {
      if (route.request().method() !== 'POST') return route.fallback()
      body = route.request().postDataJSON()
      return route.fulfill({ status: 201, json: { id: 'as1', title: 'LocalAI Assistant', model: body.model, kind: 'chat', assistant: true } })
    })
    await ready(page)
    await page.locator('.home-textarea').fill('/')
    await page.getByTestId('home-slash-assistant').click()
    await expect(page).toHaveURL(/\/app\/chats\/as1/)
    expect(body).toMatchObject({ assistant: true, title: 'LocalAI Assistant' })
  })

  test('the assistant entry and command are hidden while the setting is off', async ({ page }) => {
    await mockHome(page, { features: { distributed: false, localai_assistant: false } })
    await ready(page)
    await expect(page.getByTestId('home-assistant')).toHaveCount(0)
    await page.locator('.home-textarea').fill('/ass')
    await expect(page.getByTestId('home-slash-assistant')).toHaveCount(0)
  })

  test('filters as you type and marks the typed part', async ({ page }) => {
    await ready(page)
    await page.locator('.home-textarea').fill('/ga')
    const options = page.getByRole('option')
    await expect(options).toHaveCount(1)
    await expect(options.first()).toContainText('/gallery')
    await expect(options.first().locator('mark')).toHaveText('/ga')
  })

  test('matches on the label too', async ({ page }) => {
    await ready(page)
    await page.locator('.home-textarea').fill('/documentation')
    await expect(page.getByRole('option')).toHaveCount(1)
    await expect(page.getByRole('option').first()).toContainText('/docs')
  })

  test('arrow keys move the choice and Enter runs it', async ({ page }) => {
    await ready(page)
    const box = page.locator('.home-textarea')
    await box.fill('/')
    await box.press('ArrowDown')
    await expect(page.getByRole('option').nth(1)).toHaveAttribute('aria-selected', 'true')
    await box.press('ArrowUp')
    await box.press('ArrowUp')
    // Wraps to the last action.
    await expect(page.getByRole('option').last()).toHaveAttribute('aria-selected', 'true')
    await box.fill('/gal')
    await box.press('Enter')
    await expect(page).toHaveURL(/\/app\/models$/)
  })

  test('Escape closes the menu and keeps the text', async ({ page }) => {
    await ready(page)
    const box = page.locator('.home-textarea')
    await box.fill('/ga')
    await expect(page.getByRole('listbox', { name: 'Actions' })).toBeVisible()
    await box.press('Escape')
    await expect(page.getByRole('listbox', { name: 'Actions' })).toHaveCount(0)
    await expect(box).toHaveValue('/ga')
  })

  test('a message with a space is a message, not a command', async ({ page }) => {
    await ready(page)
    await page.locator('.home-textarea').fill('/etc/hosts what is this file')
    await expect(page.getByRole('listbox', { name: 'Actions' })).toHaveCount(0)
  })

  test('a click on an action runs it and clears the box', async ({ page }) => {
    await ready(page)
    await page.locator('.home-textarea').fill('/')
    await page.getByTestId('home-slash-installed').click()
    await expect(page).toHaveURL(/\/app\/models\?view=installed/)
  })

  test('"/" outside a text field starts a command', async ({ page }) => {
    await ready(page)
    await page.locator('.home-greeting').click()
    await page.keyboard.press('/')
    await expect(page.locator('.home-textarea')).toBeFocused()
    await expect(page.locator('.home-textarea')).toHaveValue('/')
    await expect(page.getByRole('listbox', { name: 'Actions' })).toBeVisible()
  })

  test('/model opens the model list', async ({ page }) => {
    await ready(page)
    await page.locator('.home-textarea').fill('/model')
    await page.locator('.home-textarea').press('Enter')
    await expect(page.getByRole('listbox', { name: 'Model' })).toBeVisible()
  })

  test('/stop opens the memory strip', async ({ page }) => {
    await ready(page)
    await page.locator('.home-textarea').fill('/stop')
    await page.locator('.home-textarea').press('Enter')
    await expect(page.locator('.home-loaded')).toBeVisible()
  })

  test('/new opens the chats page on the chosen model', async ({ page }) => {
    await ready(page)
    await page.locator('.home-textarea').fill('/new')
    await page.locator('.home-textarea').press('Enter')
    await expect(page).toHaveURL(/\/app\/chats\?model=qwen3-8b-instruct$/)
  })

  test('a person who is not an admin sees only the actions they can use', async ({ page }) => {
    await mockUser(page)
    await ready(page)
    await page.locator('.home-textarea').fill('/')
    const names = await page.locator('.dk-cmd-name').allTextContents()
    expect(names).toEqual(['/model', '/new', '/chat', '/docs'])
  })

  test('and the library row keeps only the documentation link', async ({ page }) => {
    await mockUser(page)
    await ready(page)
    const row = page.getByTestId('home-library')
    await expect(row.getByText(/documentation/i)).toBeVisible()
    await expect(row.getByText(/browse gallery/i)).toHaveCount(0)
    await expect(page.getByTestId('home-assistant-tip')).toHaveCount(0)
  })
})

test.describe('Home model chip', () => {
  test.beforeEach(async ({ page }) => {
    await mockHome(page)
  })

  test('shows warm and cold models and picks with the keyboard', async ({ page }) => {
    await ready(page)
    const chip = page.getByTestId('home-model-chip')
    await expect(chip).toContainText('qwen3-8b-instruct')
    await chip.click()
    const list = page.getByRole('listbox', { name: 'Model' })
    await expect(list.getByRole('option')).toHaveCount(4)
    await expect(list.getByRole('option', { name: /qwen3-8b-instruct/ }).locator('.home-dot')).not.toHaveClass(/home-dot--cold/)
    await expect(list.getByRole('option', { name: /gemma-4-e4b/ }).locator('.home-dot')).toHaveClass(/home-dot--cold/)
    await expect(list.getByRole('option', { name: /gemma-4-e4b/ })).toContainText('not loaded')
    await page.keyboard.press('ArrowDown')
    await page.keyboard.press('Enter')
    await expect(chip).toContainText('gemma-4-e4b-it-qat-q4_0')
    await expect(list).toHaveCount(0)
    await expect(chip).toBeFocused()
  })

  test('keeps Home auto-selection and remembers an explicit model', async ({ page }) => {
    await ready(page)
    const chip = page.getByTestId('home-model-chip')
    await expect(chip).toContainText('qwen3-8b-instruct')
    await chip.click()
    await page.getByRole('option', { name: /gemma-4-e4b/ }).click()
    expect(await page.evaluate(() => localStorage.getItem('localai_last_model:FLAG_CHAT'))).toBe('gemma-4-e4b-it-qat-q4_0')
    await page.reload()
    await expect(chip).toContainText('gemma-4-e4b-it-qat-q4_0')
  })

  test('Escape closes the list and returns to the chip', async ({ page }) => {
    await ready(page)
    const chip = page.getByTestId('home-model-chip')
    await chip.click()
    await page.keyboard.press('Escape')
    await expect(page.getByRole('listbox', { name: 'Model' })).toHaveCount(0)
    await expect(chip).toBeFocused()
  })
})

test.describe('Home memory strip', () => {
  test('reads memory use in one line and opens into the loaded models', async ({ page }) => {
    await mockHome(page)
    await ready(page)
    const head = page.locator('.home-strip__head')
    await expect(head).toContainText('2')
    await expect(head).toContainText('models loaded')
    await expect(head).toContainText('8.8 / 24 GB')
    await expect(head).toContainText('RTX 4090')
    await expect(head).toHaveAttribute('aria-expanded', 'false')
    await head.click()
    await expect(head).toHaveAttribute('aria-expanded', 'true')
    await expect(page.locator('.home-loaded__row')).toHaveCount(2)
    // System RAM is listed under the models when the memory shown is GPU.
    await expect(page.locator('.home-ram')).toContainText('18.2 / 64 GB')
  })

  test('the bar is the memory in use, one solid fill', async ({ page }) => {
    await mockHome(page)
    await ready(page)
    const fill = page.locator('.home-strip__head .home-bar__fill')
    const width = await fill.evaluate(el => el.getBoundingClientRect().width / el.parentElement.getBoundingClientRect().width)
    expect(width).toBeGreaterThan(0.25)
    expect(width).toBeLessThan(0.45)
    const bg = await fill.evaluate(el => getComputedStyle(el).backgroundImage)
    expect(bg).toBe('none')
  })

  test('stopping one model asks first, then calls the backend', async ({ page }) => {
    await mockHome(page)
    const stops = []
    await page.route('**/backend/shutdown', route => {
      stops.push(route.request().postDataJSON())
      return route.fulfill({ json: { message: 'ok' } })
    })
    await ready(page)
    await page.locator('.home-strip__head').click()
    const row = page.locator('.home-loaded__row', { hasText: 'whisper-large-v3-turbo' })
    await row.hover()
    await row.getByRole('button', { name: /stop/i }).click()
    await expect(page.getByRole('alertdialog')).toContainText('whisper-large-v3-turbo')
    expect(stops).toEqual([])
    await page.getByRole('button', { name: /^Stop whisper-large-v3-turbo$/ }).click()
    await expect.poll(() => stops).toEqual([{ model: 'whisper-large-v3-turbo' }])
  })

  test('stop all stops every loaded model', async ({ page }) => {
    await mockHome(page)
    const stops = []
    await page.route('**/backend/shutdown', route => {
      stops.push(route.request().postDataJSON().model)
      return route.fulfill({ json: { message: 'ok' } })
    })
    await ready(page)
    await page.locator('.home-strip__head').click()
    await page.getByRole('button', { name: 'Stop all', exact: true }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Stop all' }).click()
    await expect.poll(() => stops.sort()).toEqual(LOADED.map(m => m.id).sort())
  })

  test('opens by itself while a model is being staged', async ({ page }) => {
    await mockHome(page, { operations: [{ jobID: 'j1', name: 'qwen3-32b-instruct', taskType: 'staging', nodeName: 'gpu-b', progress: 41, isBackend: false }] })
    await ready(page)
    const head = page.locator('.home-strip__head')
    await expect(head).toContainText('Staging qwen3-32b-instruct')
    await expect(head).toContainText('41%')
    await expect(head).toHaveAttribute('aria-expanded', 'true')
  })

  test('opens by itself after a failure and says what happened in plain words', async ({ page }) => {
    await mockHome(page, { operations: [{ jobID: 'j2', name: 'qwen3-32b-instruct', taskType: 'staging', nodeName: 'gpu-b', progress: 0, error: 'no node has enough free memory', isBackend: false }] })
    await ready(page)
    await expect(page.locator('.home-strip__head')).toHaveAttribute('aria-expanded', 'true')
    const notice = page.getByTestId('home-staging-error')
    await expect(notice).toContainText('qwen3-32b-instruct could not be staged')
    await expect(notice).toContainText('gpu-b')
    // One action; the raw error is behind a disclosure.
    await expect(notice.getByRole('button')).toHaveCount(1)
    await notice.getByText('Details').click()
    await expect(notice).toContainText('no node has enough free memory')
  })

  test('says it could not reach the server instead of showing a first run', async ({ page }) => {
    await page.route('**/system', route => route.abort())
    await page.route('**/v1/models', route => route.abort())
    await ready(page)
    const notice = page.getByTestId('home-load-error')
    await expect(notice).toContainText('could not load your models')
    await expect(page.getByTestId('home-first-run')).toHaveCount(0)
    // Retry works once the server answers.
    await page.unroute('**/system')
    await page.unroute('**/v1/models')
    await mockHome(page)
    await notice.getByRole('button', { name: 'Try again' }).click()
    await expect(notice).toHaveCount(0)
    await expect(page.locator('.home-strip__head')).toContainText('models loaded')
  })

  test('shows a skeleton while the first answer is on its way', async ({ page }) => {
    await mockHome(page)
    await page.route('**/v1/models', async route => {
      await new Promise(resolve => setTimeout(resolve, 800))
      await route.fulfill({ json: { data: [{ id: 'a' }] } })
    })
    await page.goto('/app')
    await expect(page.getByTestId('home-strip-skeleton')).toBeVisible()
    await expect(page.locator('.home-strip__head')).toBeVisible()
  })
})

test.describe('Home in a cluster', () => {
  const nodes = [
    { id: 'a', name: 'gpu-a', status: 'healthy', total_vram: 24 * GB, available_vram: 17 * GB },
    { id: 'b', name: 'gpu-b', status: 'healthy', total_vram: 48 * GB, available_vram: 21 * GB },
    { id: 'c', name: 'gpu-c', status: 'healthy', total_vram: 24 * GB, available_vram: 7 * GB },
  ]

  test('the strip says models, nodes and aggregate memory in one line', async ({ page }) => {
    await mockHome(page, { features: { distributed: true, localai_assistant: true }, nodes })
    await ready(page)
    const head = page.locator('.home-strip__head')
    await expect(head).toContainText('2')
    await expect(head).toContainText('models on')
    await expect(head).toContainText('3/3')
    await expect(head).toContainText('nodes')
    await expect(head).toContainText('51 / 96 GB')
    await head.click()
    await expect(page.locator('.home-nodes li')).toHaveCount(3)
    await expect(page.locator('.home-nodes')).toContainText('27 / 48 GB')
  })
})

test.describe('Home resume list', () => {
  const chat = { id: 'c1', title: 'Rewrite the release notes', model: 'qwen3-8b-instruct', kind: 'chat', created: '2026-10-09T10:00:00Z', updated: '2026-10-09T11:00:00Z' }
  test.beforeEach(async ({ page }) => {
    await page.clock.setFixedTime(new Date('2026-10-09T12:00:00Z').getTime())
    await mockHome(page)
  })

  test('it shows the newest chat the server keeps and opens it', async ({ page }) => {
    let asked = ''
    await page.route('**/api/chats?limit=1&kind=chat', route => { asked = route.request().url(); return route.fulfill({ json: { conversations: [chat], persistent: true, files: true } }) })
    await page.route('**/api/chats/c1', route => route.fulfill({ json: { conversation: chat } }))
    await ready(page)
    const row = page.getByTestId('home-conversation')
    await expect(row).toHaveCount(1)
    await expect(row).toContainText('Rewrite the release notes')
    await expect(row).toContainText('qwen3-8b-instruct')
    expect(asked).toContain('limit=1')
    await row.getByRole('button').first().click()
    await expect(page).toHaveURL(/\/app\/chats\/c1$/)
  })

  test('a row has no delete button: chats are deleted on the chats page', async ({ page }) => {
    await page.route('**/api/chats?limit=1&kind=chat', route => route.fulfill({ json: { conversations: [chat], persistent: true, files: true } }))
    await ready(page)
    await expect(page.getByTestId('home-conversation')).toHaveCount(1)
    await expect(page.locator('.home-row__delete')).toHaveCount(0)
  })

  test('with no chats it says where they will appear', async ({ page }) => {
    await page.route('**/api/chats?limit=1&kind=chat', route => route.fulfill({ json: { conversations: [], persistent: true, files: true } }))
    await ready(page)
    await expect(page.locator('.home-empty')).toContainText('Your conversations will wait here')
  })
})

test.describe('Home first run', () => {
  async function mockFirstRun(page) {
    await mockHome(page, { loaded: [], models: [], chatModels: [] })
    await page.route('**/api/models/estimate/*', route => route.fulfill({
      json: { sizeBytes: 2 * GB, sizeDisplay: '2 GB', estimates: { 4096: { vramBytes: 3 * GB, vramDisplay: '3 GB' } } },
    }))
    await page.route('**/api/models?*', route => route.fulfill({
      json: {
        models: [
          { name: 'tiny-chat', backend: 'llama-cpp', installed: false, tags: ['chat'] },
          { name: 'small-chat', backend: 'llama-cpp', installed: false, tags: ['chat'] },
        ],
        allBackends: [], allTags: [], availableModels: 2, installedModels: 0, totalPages: 1, currentPage: 1,
      },
    }))
  }

  test('teaches the three steps, with the recommended models inside the second', async ({ page }) => {
    await mockFirstRun(page)
    await ready(page)
    const guide = page.getByTestId('home-first-run')
    // The steps only: the recommended models are a list inside the second step.
    await expect(guide.locator('.home-step')).toHaveCount(3)
    await expect(guide.locator('.home-step[data-state="now"]')).toContainText('Browse the Model Gallery')
    await expect(guide.locator('.home-step').nth(1).locator('.home-starters')).toContainText('tiny-chat')
    await expect(guide.getByRole('button', { name: /browse model gallery/i })).toBeVisible()
    await expect(guide.getByRole('button', { name: /import model/i })).toBeVisible()
  })

  test('the command bar waits for a model and the strip says nothing runs', async ({ page }) => {
    await mockFirstRun(page)
    await ready(page)
    await expect(page.getByTestId('home-model-chip')).toContainText('No model selected')
    await expect(page.getByTestId('home-model-chip')).toBeDisabled()
    await expect(page.locator('.home-textarea')).toHaveAttribute('placeholder', 'Install a model to start chatting')
    await expect(page.locator('.home-send-btn')).toBeDisabled()
    await expect(page.getByTestId('home-strip-idle')).toContainText('No models running')
  })

  test('slash actions already work before a model is installed', async ({ page }) => {
    await mockFirstRun(page)
    await ready(page)
    await page.locator('.home-textarea').fill('/imp')
    await page.locator('.home-textarea').press('Enter')
    await expect(page).toHaveURL(/\/app\/import-model/)
  })

  test('install starts from a recommended row', async ({ page }) => {
    await mockFirstRun(page)
    const installs = []
    await page.route('**/api/models/install/*', route => {
      installs.push(new URL(route.request().url()).pathname)
      return route.fulfill({ json: { uuid: 'x', status: 'ok' } })
    })
    await ready(page)
    await page.locator('.home-starters-item', { hasText: 'tiny-chat' }).getByRole('button', { name: /install/i }).click()
    await expect.poll(() => installs.length).toBe(1)
    expect(installs[0]).toContain('tiny-chat')
  })

  test('a person who is not an admin gets a calm explanation, not install steps', async ({ page }) => {
    await mockFirstRun(page)
    await mockUser(page)
    await page.goto('/app')
    await expect(page.getByRole('heading', { name: 'No Models Available' })).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('home-first-run')).toHaveCount(0)
    await expect(page.locator('.home-textarea')).toHaveCount(0)
    await expect(page.locator('.home-wizard-actions').getByRole('link', { name: /documentation/i })).toBeVisible()
  })
})

test.describe('Home send hand-off', () => {
  test.beforeEach(async ({ page }) => {
    await mockHome(page)
  })

  // The chats page starts the chat on its own: it creates it with the model and
  // sends the typed text as the first message. The stubs answer in the shapes of
  // the server: the list and the messages are objects, and a chat is the flat
  // conversation with its waiting messages (core/schema/chats.go).
  async function serverChats(page) {
    const seen = { created: [], sent: [] }
    const conversation = { id: 'n1', title: 'New chat', model: 'qwen3-8b-instruct', kind: 'chat', created: '2026-10-09T10:00:00Z', updated: '2026-10-09T10:00:00Z', running: false }
    await page.route(/\/api\/chats(\?.*)?$/, async route => {
      if (route.request().method() === 'POST') {
        const body = route.request().postDataJSON()
        seen.created.push(body)
        return route.fulfill({ status: 201, json: { ...conversation, model: body.model } })
      }
      return route.fulfill({ json: { conversations: [], persistent: true, files: true } })
    })
    await page.route(/\/api\/chats\/n1\/messages(\?.*)?$/, route => {
      if (route.request().method() === 'POST') {
        seen.sent.push(route.request().postDataJSON())
        return route.fulfill({ json: { started: true } })
      }
      return route.fulfill({ json: { messages: [] } })
    })
    await page.route('**/api/chats/n1/events', route => route.abort())
    await page.route('**/api/chats/n1', route => route.fulfill({ json: { ...conversation, pending: [] } }))
    return seen
  }

  test('Send opens the chats page and starts the chat with the typed text and the model', async ({ page }) => {
    const seen = await serverChats(page)
    await ready(page)
    await page.locator('.home-textarea').fill('Summarise the release notes')
    await page.locator('.home-send-btn').click()
    await expect(page).toHaveURL(/\/app\/chats\/n1$/)
    await expect.poll(() => seen.sent.length).toBe(1)
    // Give a second, wrong send time to arrive before the counts are read.
    await page.waitForTimeout(1500)
    expect(seen.created).toHaveLength(1)
    expect(seen.created[0].model).toBe('qwen3-8b-instruct')
    expect(seen.sent).toEqual([{ text: 'Summarise the release notes' }])
  })

  test('Enter sends and Shift+Enter adds a line', async ({ page }) => {
    const seen = await serverChats(page)
    await ready(page)
    const box = page.locator('.home-textarea')
    await box.fill('line one')
    await box.press('Shift+Enter')
    await box.type('line two')
    await expect(box).toHaveValue('line one\nline two')
    await box.press('Enter')
    await expect(page).toHaveURL(/\/app\/chats\/n1$/)
    await expect.poll(() => seen.sent.length).toBe(1)
    await page.waitForTimeout(1500)
    expect(seen.created).toHaveLength(1)
    expect(seen.sent).toEqual([{ text: 'line one\nline two' }])
  })

  test('Back from the new chat sends nothing again', async ({ page }) => {
    const seen = await serverChats(page)
    await ready(page)
    await page.locator('.home-textarea').fill('Only once')
    await page.locator('.home-send-btn').click()
    await expect(page).toHaveURL(/\/app\/chats\/n1$/)
    await expect.poll(() => seen.sent.length).toBe(1)

    await page.goBack()
    await expect(page).toHaveURL(/\/app\/chats(\?model=[^/]*)?$/)
    await expect(page.getByTestId('chat-input')).toHaveValue('')
    await page.waitForTimeout(1500)
    expect(seen.created).toHaveLength(1)
    expect(seen.sent).toHaveLength(1)

    await page.goForward()
    await expect(page).toHaveURL(/\/app\/chats\/n1$/)
    await page.waitForTimeout(500)
    expect(seen.sent).toHaveLength(1)
  })

  test('Send stays off until there is text', async ({ page }) => {
    await ready(page)
    await expect(page.locator('.home-send-btn')).toBeDisabled()
    await page.locator('.home-textarea').fill('hi')
    await expect(page.locator('.home-send-btn')).toBeEnabled()
  })

  test('the old chat address opens the chats page and keeps the model as a hint', async ({ page }) => {
    await page.route('**/api/chats?limit=1&kind=chat', route => route.fulfill({ json: { conversations: [], persistent: true, files: true } }))
    await page.goto('/app/chat/qwen3-8b-instruct')
    await expect(page).toHaveURL(/\/app\/chats\?model=qwen3-8b-instruct$/)
  })
})

test.describe('Home on a phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test.beforeEach(async ({ page }) => {
    await mockHome(page)
  })

  test('the slash list stays inside the screen', async ({ page }) => {
    await ready(page)
    await page.locator('.home-textarea').fill('/')
    const box = await page.locator('.home-slash').boundingBox()
    expect(box.x).toBeGreaterThanOrEqual(0)
    expect(box.x + box.width).toBeLessThanOrEqual(390)
  })

  test('first run fits too', async ({ page }) => {
    await page.unroute('**/system')
    await page.unroute('**/v1/models')
    await page.route('**/system', route => route.fulfill({ json: { backends: [], loaded_models: [] } }))
    await page.route('**/v1/models', route => route.fulfill({ json: { data: [] } }))
    await ready(page)
    await expect(page.getByTestId('home-first-run')).toBeVisible()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(0)
  })
})

test.describe('Home reduced motion', () => {
  test('rows and menus do not animate', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await mockHome(page)
    await ready(page)
    await page.locator('.home-textarea').fill('/')
    const name = await page.locator('.home-slash').evaluate(el => getComputedStyle(el).animationName)
    expect(name).toBe('none')
  })
})
