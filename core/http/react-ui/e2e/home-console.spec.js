import { test, expect } from './coverage-fixtures.js'
import { mockHome, mockUser, seedChats, sampleChats, CHATS_KEY, LOADED } from './home-fixtures.js'

// The Home console: command bar, slash menu, memory strip, resume list, first
// run and the phone layout. Data is stubbed the way the other Home specs do it;
// conversations are written to the browser storage the Chat page reads.

const GB = 1024 * 1024 * 1024

async function ready(page) {
  await page.goto('/app')
  await expect(page.locator('.home-greeting')).toBeVisible({ timeout: 15_000 })
}

async function storedChats(page) {
  return page.evaluate((key) => JSON.parse(localStorage.getItem(key) || '{}'), CHATS_KEY)
}

test.describe('Home slash menu', () => {
  test.beforeEach(async ({ page }) => {
    await mockHome(page)
    await seedChats(page)
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
      '/studio', '/settings', '/docs',
    ])
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
    await page.getByTestId('home-slash-studio').click()
    await expect(page).toHaveURL(/\/app\/studio/)
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

  test('/new opens an empty chat on the chosen model', async ({ page }) => {
    await ready(page)
    const before = (await storedChats(page)).chats.length
    await page.locator('.home-textarea').fill('/new')
    await page.locator('.home-textarea').press('Enter')
    await expect(page).toHaveURL(/\/app\/chat/)
    await expect.poll(async () => {
      const data = await storedChats(page)
      const active = data.chats.find(c => c.id === data.activeChatId)
      return data.chats.length === before + 1 && active?.history.length === 0 && active?.model
    }).toBe('qwen3-8b-instruct')
  })

  test('a person who is not an admin sees only the actions they can use', async ({ page }) => {
    await mockUser(page)
    await ready(page)
    await page.locator('.home-textarea').fill('/')
    const names = await page.locator('.dk-cmd-name').allTextContents()
    expect(names).toEqual(['/model', '/new', '/chat', '/studio', '/docs'])
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
    await seedChats(page)
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
  const now = new Date('2026-10-09T12:00:00Z').getTime()
  test.beforeEach(async ({ page }) => {
    await page.clock.setFixedTime(now)
    await mockHome(page)
    await seedChats(page, sampleChats(now))
  })

  test('groups conversations by day, one card per day', async ({ page }) => {
    await ready(page)
    await expect(page.getByTestId('home-day-today').locator('.home-day__label')).toContainText('Today')
    await expect(page.getByTestId('home-day-yesterday').locator('.home-day__label')).toContainText('Yesterday')
    await expect(page.getByTestId('home-day-week').locator('.home-day__label')).toContainText('Earlier this week')
    await expect(page.getByTestId('home-day-today').getByTestId('home-conversation')).toHaveCount(2)
    await expect(page.getByTestId('home-day-yesterday').getByTestId('home-conversation')).toHaveCount(2)
    await expect(page.getByTestId('home-day-week').getByTestId('home-conversation')).toHaveCount(1)
  })

  test('a row shows time, title, reply preview, model and message count', async ({ page }) => {
    await ready(page)
    const row = page.getByTestId('home-conversation').first()
    await expect(row.locator('.home-row__time')).toHaveText(/^\d\d:\d\d$/)
    await expect(row.locator('.home-row__title')).toHaveText('Rewrite the 3.4 release notes for clarity')
    await expect(row.locator('.home-row__reply')).toContainText('Anytime.')
    await expect(row.locator('.home-row__model')).toHaveText('qwen3-8b-instruct')
    await expect(row.locator('.home-row__count')).toHaveText('6 messages')
  })

  test('a conversation with an image carries an eye', async ({ page }) => {
    await ready(page)
    const row = page.getByTestId('home-conversation').filter({ hasText: 'architecture diagram' })
    await expect(row.locator('.home-row__model svg')).toBeVisible()
  })

  test('hover swaps the count for Resume and offers delete', async ({ page }) => {
    await ready(page)
    const row = page.getByTestId('home-conversation').nth(1)
    await expect(row.locator('.home-row__count')).toBeVisible()
    await expect(row.locator('.home-row__resume')).toBeHidden()
    await row.hover()
    await expect(row.locator('.home-row__count')).toBeHidden()
    await expect(row.locator('.home-row__resume')).toBeVisible()
    await expect(row.getByRole('button', { name: /delete conversation/i })).toBeVisible()
  })

  test('j and k move between rows; Enter resumes the focused one', async ({ page }) => {
    await ready(page)
    await page.locator('.home-greeting').click()
    await page.keyboard.press('j')
    await expect(page.locator('.home-row__open').nth(0)).toBeFocused()
    await page.keyboard.press('j')
    await expect(page.locator('.home-row__open').nth(1)).toBeFocused()
    await page.keyboard.press('k')
    await expect(page.locator('.home-row__open').nth(0)).toBeFocused()
    await page.keyboard.press('j')
    await page.keyboard.press('j')
    await page.keyboard.press('Enter')
    await expect(page).toHaveURL(/\/app\/chat/)
    const data = await storedChats(page)
    expect(data.activeChatId).toBe('c-yday-a')
  })

  test('j and k are ignored while typing', async ({ page }) => {
    await ready(page)
    await page.locator('.home-textarea').fill('jk')
    await expect(page.locator('.home-textarea')).toHaveValue('jk')
    await expect(page.locator('.home-row__open').first()).not.toBeFocused()
  })

  test('clicking a row resumes that conversation in Chat', async ({ page }) => {
    await ready(page)
    await page.getByTestId('home-conversation').filter({ hasText: 'invoice' }).locator('.home-row__open').click()
    await expect(page).toHaveURL(/\/app\/chat/)
    expect((await storedChats(page)).activeChatId).toBe('c-yday-b')
  })

  test('delete hides the row and offers undo; undo brings it back', async ({ page }) => {
    await ready(page)
    const title = 'Why does llama-cpp stall at a 4096 context'
    const row = page.getByTestId('home-conversation').filter({ hasText: title })
    await row.hover()
    await row.getByRole('button', { name: /delete conversation/i }).click()
    await expect(page.getByTestId('home-conversation').filter({ hasText: title })).toHaveCount(0)
    const toast = page.getByTestId('home-undo-toast')
    await expect(toast).toContainText(title)
    // The chat is kept until the undo time ends.
    expect((await storedChats(page)).chats.map(c => c.id)).toContain('c-today-b')
    await toast.getByRole('button', { name: 'Undo' }).click()
    await expect(page.getByTestId('home-conversation').filter({ hasText: title })).toHaveCount(1)
    await expect(toast).toHaveCount(0)
    expect((await storedChats(page)).chats.map(c => c.id)).toContain('c-today-b')
  })

  test('without undo the delete becomes final when the time ends', async ({ page }) => {
    await page.clock.install({ time: now })
    await ready(page)
    const row = page.getByTestId('home-conversation').filter({ hasText: 'invoice' })
    await row.hover()
    await row.getByRole('button', { name: /delete conversation/i }).click()
    await expect(page.getByTestId('home-undo-toast')).toBeVisible()
    await page.clock.runFor(6500)
    await expect(page.getByTestId('home-undo-toast')).toHaveCount(0)
    expect((await storedChats(page)).chats.map(c => c.id)).not.toContain('c-yday-b')
  })

  test('the Delete key removes the focused row and moves focus on', async ({ page }) => {
    await ready(page)
    await page.locator('.home-greeting').click()
    await page.keyboard.press('j')
    await page.keyboard.press('Delete')
    await expect(page.getByTestId('home-conversation')).toHaveCount(4)
    await expect(page.locator('.home-row__open').first()).toBeFocused()
    await expect(page.getByTestId('home-undo-toast')).toBeVisible()
  })

  test('old conversations wait behind one button', async ({ page }) => {
    const chats = sampleChats(now)
    const old = Array.from({ length: 8 }, (_, i) => ({
      ...chats[0],
      id: `old-${i}`,
      name: `Old chat ${i}`,
      updatedAt: now - (10 + i) * 24 * 60 * 60 * 1000,
    }))
    await page.addInitScript(([key, data]) => localStorage.setItem(key, JSON.stringify(data)),
      [CHATS_KEY, { chats: [...chats, ...old], activeChatId: chats[0].id }])
    await ready(page)
    await expect(page.getByTestId('home-conversation')).toHaveCount(8)
    await page.getByRole('button', { name: /show 5 older conversations/i }).click()
    await expect(page.getByTestId('home-conversation')).toHaveCount(13)
    await expect(page.getByTestId('home-day-older').locator('.home-day__label')).toContainText('Older')
  })

  test('with no conversations it says where they will appear', async ({ page }) => {
    await page.addInitScript((key) => localStorage.removeItem(key), CHATS_KEY)
    await page.unroute('**/api/features')
    await mockHome(page)
    await page.addInitScript((key) => localStorage.setItem(key, JSON.stringify({ chats: [], activeChatId: null })), CHATS_KEY)
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
    await expect(guide.getByRole('listitem')).toHaveCount(3)
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
  async function record(page) {
    await page.addInitScript(() => {
      window.__handoff = null
      const set = Storage.prototype.setItem
      Storage.prototype.setItem = function (key, value) {
        if (key === 'localai_index_chat_data') window.__handoff = JSON.parse(value)
        return set.call(this, key, value)
      }
    })
  }

  test.beforeEach(async ({ page }) => {
    await mockHome(page)
    await seedChats(page)
    await record(page)
  })

  test('Send hands the message and model to Chat and opens it', async ({ page }) => {
    await ready(page)
    const send = page.locator('.home-send-btn')
    await expect(send).toBeDisabled()
    await page.locator('.home-textarea').fill('Compare the release plans')
    await expect(send).toBeEnabled()
    await send.click()
    await expect(page).toHaveURL(/\/app\/chat\/qwen3-8b-instruct/)
    const handoff = await page.evaluate(() => window.__handoff)
    expect(handoff).toMatchObject({
      message: 'Compare the release plans',
      model: 'qwen3-8b-instruct',
      newChat: true,
      mcpMode: false,
      mcpServers: [],
      clientMCPServers: [],
      files: [],
    })
  })

  test('Enter sends and Shift+Enter adds a line', async ({ page }) => {
    await ready(page)
    const box = page.locator('.home-textarea')
    await box.fill('first')
    await box.press('Shift+Enter')
    await box.type('second')
    await expect(box).toHaveValue('first\nsecond')
    await box.press('Enter')
    await expect(page).toHaveURL(/\/app\/chat\//)
    expect((await page.evaluate(() => window.__handoff)).message).toBe('first\nsecond')
  })

  test('the picked model travels with it', async ({ page }) => {
    await ready(page)
    await page.getByTestId('home-model-chip').click()
    await page.getByRole('option', { name: /gemma-4-e4b/ }).click()
    await page.locator('.home-textarea').fill('hello')
    await page.locator('.home-send-btn').click()
    await expect(page).toHaveURL(/\/app\/chat\/gemma-4-e4b-it-qat-q4_0/)
    expect((await page.evaluate(() => window.__handoff)).model).toBe('gemma-4-e4b-it-qat-q4_0')
  })

  test('attachments travel with it', async ({ page }) => {
    await ready(page)
    await page.locator('input[type=file][accept*="pdf"]').setInputFiles({
      name: 'notes.txt', mimeType: 'text/plain', buffer: Buffer.from('Meeting notes'),
    })
    const tag = page.locator('.home-file-tag', { hasText: 'notes.txt' })
    await expect(tag).toBeVisible()
    // An attachment alone is enough to send.
    await expect(page.locator('.home-send-btn')).toBeEnabled()
    await page.locator('.home-textarea').fill('Summarise')
    await page.locator('.home-send-btn').click()
    await expect(page).toHaveURL(/\/app\/chat\//)
    const handoff = await page.evaluate(() => window.__handoff)
    expect(handoff.files).toHaveLength(1)
    expect(handoff.files[0]).toMatchObject({ name: 'notes.txt', textContent: 'Meeting notes' })
  })

  test('an attachment can be removed before sending', async ({ page }) => {
    await ready(page)
    await page.locator('input[type=file][accept*="pdf"]').setInputFiles({
      name: 'notes.txt', mimeType: 'text/plain', buffer: Buffer.from('x'),
    })
    await page.getByRole('button', { name: 'Remove notes.txt' }).click()
    await expect(page.locator('.home-file-tag')).toHaveCount(0)
    await expect(page.locator('.home-send-btn')).toBeDisabled()
  })

  test('the MCP selection travels with it', async ({ page }) => {
    await page.route('**/api/models/config-json/qwen3-8b-instruct', route => route.fulfill({
      json: { name: 'qwen3-8b-instruct', mcp: { remote: 'mcpServers:\n  ordino:\n    url: http://ordino:8080/mcp' } },
    }))
    await page.route('**/v1/mcp/servers/qwen3-8b-instruct', route => route.fulfill({
      json: { model: 'qwen3-8b-instruct', servers: [{ name: 'ordino', type: 'remote', tools: [{ name: 't' }] }] },
    }))
    await ready(page)
    await page.locator('.chat-mcp-dropdown > button').click()
    await page.getByRole('button', { name: 'Servers', exact: true }).click()
    await page.locator('.chat-mcp-server-item', { hasText: 'ordino' }).getByRole('checkbox').check()
    await page.locator('.home-textarea').fill('use the tool')
    await page.locator('.home-send-btn').click()
    await expect(page).toHaveURL(/\/app\/chat\//)
    expect((await page.evaluate(() => window.__handoff)).mcpServers).toEqual(['ordino'])
  })

  test('the assistant line opens a chat in assistant mode and then steps aside', async ({ page }) => {
    await ready(page)
    const tip = page.getByTestId('home-assistant-tip')
    await expect(tip).toContainText('Manage LocalAI by chatting')
    await tip.getByRole('button', { name: 'Open assistant' }).click()
    await expect(page).toHaveURL(/\/app\/chat$/)
    const handoff = await page.evaluate(() => window.__handoff)
    expect(handoff).toMatchObject({ localaiAssistant: true, newChat: true })
  })

  test('the assistant line can be dismissed and stays dismissed', async ({ page }) => {
    await ready(page)
    await page.getByTestId('home-assistant-tip').getByRole('button', { name: 'Dismiss' }).click()
    await expect(page.getByTestId('home-assistant-tip')).toHaveCount(0)
    await page.reload()
    await expect(page.locator('.home-greeting')).toBeVisible()
    await expect(page.getByTestId('home-assistant-tip')).toHaveCount(0)
    // It moves to the library row.
    await expect(page.getByTestId('home-library')).toContainText('Manage by chat')
  })
})

test.describe('Home on a phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test.beforeEach(async ({ page }) => {
    await mockHome(page)
    await seedChats(page)
  })

  test('nothing scrolls sideways', async ({ page }) => {
    await ready(page)
    await expect(page.getByTestId('home-conversation').first()).toBeVisible()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(0)
  })

  test('the command bar, strip and resume list fit the width', async ({ page }) => {
    await ready(page)
    for (const sel of ['.home-cmd', '.home-strip', '.home-day__card']) {
      const box = await page.locator(sel).first().boundingBox()
      expect(box.x).toBeGreaterThanOrEqual(0)
      expect(box.x + box.width).toBeLessThanOrEqual(390)
    }
    // The hints that need a keyboard are hidden.
    await expect(page.locator('.home-slash-hint')).toBeHidden()
    await expect(page.locator('.home-resume__keys')).toBeHidden()
  })

  test('Send stays on the right and the three attach buttons stay', async ({ page }) => {
    await ready(page)
    await expect(page.locator('.home-attach-btn')).toHaveCount(3)
    const send = await page.locator('.home-send-btn').boundingBox()
    const cmd = await page.locator('.home-cmd').boundingBox()
    expect(send.x + send.width).toBeGreaterThan(cmd.x + cmd.width - 16)
  })

  test('a conversation can be deleted without a hover', async ({ page }) => {
    await ready(page)
    const row = page.getByTestId('home-conversation').first()
    const del = row.getByRole('button', { name: /delete conversation/i })
    await expect(del).toBeVisible()
    await del.click()
    await expect(page.getByTestId('home-undo-toast')).toBeVisible()
    await expect(page.getByTestId('home-conversation')).toHaveCount(4)
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
    await seedChats(page)
    await ready(page)
    await page.locator('.home-textarea').fill('/')
    const name = await page.locator('.home-slash').evaluate(el => getComputedStyle(el).animationName)
    expect(name).toBe('none')
  })
})
