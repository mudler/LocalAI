import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { app, installedAddon, mockAddons } from './addons-fixtures.js'

// The sandbox profile of a chat's commands: the row of the Tools menu next to
// the Network row, and the sandbox-profiles host control on the Settings tab of
// the add-on that runs commands. The API is stubbed.

const SHOTS = process.env.SBP_SHOT_DIR
const shot = async (page, name) => {
  if (!SHOTS) return
  for (const theme of ['light', 'dark']) {
    await page.evaluate(t => document.documentElement.setAttribute('data-theme', t), theme)
    await page.waitForTimeout(300)
    await page.screenshot({ path: `${SHOTS}/${name}-${theme}.png` })
  }
}

const RUNNER = app({
  id: 'sandbox', name: 'sandbox', title: 'Sandbox', description: 'Runs commands.', mainPage: undefined, shadows: ['bash'], tools: ['bash'], toolCount: 1,
  ui: { entry: `/api/addons/sandbox/assets/${'a'.repeat(64)}/ui.js`, sdk: { min: 2, max: 2 }, controls: ['network-policy', 'tool-providers', 'sandbox-profiles'] },
})
const DETAIL = {
  id: 'c1', title: 'Video', model: 'qwen3-8b', kind: 'chat', created: '2026-10-30T10:00:00Z', updated: '2026-10-30T10:00:00Z',
  running: false, last_turn: 'done', pending: [],
}
const sandboxAddons = { addons: [{ id: 'sandbox', tools: 2, off: false }], commands: 'available', commands_provider: 'sandbox', commands_isolation: 'isolated', execution_status: { readiness: 'available', isolation: 'isolated' } }
const egress = { preset: 'off', ceiling: 'registries', effective: 'off', applies: true, applies_reason: '', addon: 'sandbox', network: { title: 'Sandbox', presets: [] }, registries: [], allowed_hosts: [], rules: [], cards: [] }

const profile = (id, title, description, tools, extra = {}) => ({ id, title, description, tools, allowed: true, state: { phase: 'ready', percent: 0, error: '' }, ...extra })
const PROFILES = () => [
  profile('base', 'Base', 'Python, documents, PDF and the usual command-line tools.', ['python3 (pandas, matplotlib)', 'jq', 'git']),
  profile('media', 'Media', 'Base plus ffmpeg, sox and ImageMagick.', ['python3 (pandas, matplotlib)', 'jq', 'git', 'ffmpeg', 'sox', 'ImageMagick'], { state: { phase: 'idle', percent: 0, error: '' } }),
  profile('heavy', 'Heavy', 'Media plus a compiler.', ['ffmpeg', 'gcc'], { allowed: false }),
]
const spView = (over = {}) => ({ applies: true, addon: 'sandbox', profile: 'base', selected: '', source: 'default', default: 'base', fallback: false, fallback_reason: '', newer_image: false, profiles: PROFILES(), ...over })

async function stubChat(page, view) {
  await mockAddons(page, { apps: [RUNNER], installed: [installedAddon({ id: 'sandbox', title: 'Sandbox' })] })
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: [] } }))
  await page.route('**/api/chats/c1', route => route.fulfill({ json: DETAIL }))
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({ json: { conversations: [{ ...DETAIL, pending: undefined }], persistent: true, files: true, files_reason: '' } }))
  await page.route('**/api/chats/c1/addons', route => route.fulfill({ json: sandboxAddons }))
  await page.route('**/api/chats/c1/egress', route => route.fulfill({ json: egress }))
  await page.route('**/api/chats/c1/events', route => route.fulfill({ status: 200, headers: { 'Content-Type': 'text/event-stream' }, body: '' }).catch(() => {}))
  const state = { view, puts: [], gets: 0, putError: null }
  await page.route('**/api/chats/c1/sandbox-profile', route => {
    if (route.request().method() === 'PUT') {
      const { profile: id } = route.request().postDataJSON()
      state.puts.push(id)
      if (state.putError) return route.fulfill({ status: 400, json: { error: { type: state.putError, message: 'no' } } })
      state.view = { ...state.view, profile: id, selected: id, source: 'chat' }
      return route.fulfill({ json: state.view })
    }
    state.gets += 1
    return route.fulfill({ json: typeof state.view === 'function' ? state.view(state.gets) : state.view })
  })
  return state
}

async function openMenu(page) {
  await page.goto('/app/chats/c1')
  await page.getByTestId('chat-tools-button').click()
  return page.getByTestId('chat-sandbox-profile')
}

test.describe('Sandbox profile of the commands', () => {
  test.beforeEach(async ({ page }) => { await mockChat(page) })

  test('the Tools menu lists the profiles next to the network, and a profile the administrator withholds is disabled', async ({ page }) => {
    await stubChat(page, spView())
    const row = await openMenu(page)
    await expect(page.getByTestId('chat-network')).toBeVisible()
    await expect(row).toContainText('Sandbox profile')
    await expect(row.getByRole('radio', { name: 'Base' })).toHaveAttribute('aria-checked', 'true')
    await expect(row.getByTestId('chat-sandbox-profile-about')).toContainText('usual command-line tools')
    await expect(row.getByTestId('chat-sandbox-profile-tools')).toContainText('jq')
    await expect(row.getByTestId('chat-sandbox-profile-heavy')).toBeDisabled()
    await expect(row.getByTestId('chat-sandbox-profile-why-heavy')).toContainText('Your administrator has not given you this profile')
    await expect(row.getByTestId('chat-sandbox-profile-source')).toContainText('the default')
    await shot(page, 'sbp-picker')
  })

  test('a switch says first that it restarts the sandbox, then asks the server', async ({ page }) => {
    const state = await stubChat(page, spView())
    const row = await openMenu(page)
    await row.getByTestId('chat-sandbox-profile-media').click()
    await expect(row.getByTestId('chat-sandbox-profile-warning')).toHaveText('Switching restarts the sandbox. Your files stay. Running processes and installed packages are lost.')
    await expect(row.getByTestId('chat-sandbox-profile-tools')).toContainText('ffmpeg')
    expect(state.puts).toEqual([])
    await shot(page, 'sbp-switch-warning')
    await row.getByTestId('chat-sandbox-profile-confirm-yes').click()
    await expect.poll(() => state.puts).toEqual(['media'])
    await expect(row.getByRole('radio', { name: 'Media' })).toHaveAttribute('aria-checked', 'true')
    await expect(row.getByTestId('chat-sandbox-profile-confirm')).toHaveCount(0)
    await expect(row.getByTestId('chat-sandbox-profile-source')).toContainText('your choice')
  })

  test('cancelling a switch changes nothing', async ({ page }) => {
    const state = await stubChat(page, spView())
    const row = await openMenu(page)
    await row.getByTestId('chat-sandbox-profile-media').click()
    await row.getByTestId('chat-sandbox-profile-confirm-no').click()
    await expect(row.getByTestId('chat-sandbox-profile-confirm')).toHaveCount(0)
    expect(state.puts).toEqual([])
  })

  test('shows the progress of an image that is pulled and reads the view again every two seconds until it is ready', async ({ page }) => {
    const pulls = [10, 55, 90]
    const state = await stubChat(page, n => {
      const i = n - 1
      const ready = i >= pulls.length
      return spView({ profile: 'media', profiles: PROFILES().map(p => p.id === 'media'
        ? { ...p, state: ready ? { phase: 'ready', percent: 100, error: '' } : { phase: 'pulling', percent: pulls[i], error: '' } } : p) })
    })
    const row = await openMenu(page)
    const bar = row.getByTestId('chat-sandbox-profile-pulling-media')
    await expect(bar).toContainText('Preparing the Media image, 10%')
    await shot(page, 'sbp-pulling')
    await expect(bar).toContainText('Preparing the Media image, 55%', { timeout: 6_000 })
    await expect(bar).toHaveCount(0, { timeout: 10_000 })
    const after = state.gets
    await page.waitForTimeout(2600)
    expect(state.gets).toBeLessThanOrEqual(after + 1) // the timer stopped once nothing pulls
  })

  test('says in the chat that a profile fell back to Base, and why', async ({ page }) => {
    await stubChat(page, spView({
      profile: 'base', selected: 'media', source: 'chat', fallback: true, fallback_reason: 'The registry could not be reached.',
      profiles: PROFILES().map(p => p.id === 'media' ? { ...p, state: { phase: 'failed', percent: 0, error: 'registry unreachable' } } : p),
    }))
    const row = await openMenu(page)
    await expect(row.getByTestId('chat-sandbox-profile-fallback')).toContainText('Media could not be used, so commands run in Base. The registry could not be reached.')
    await expect(row.getByTestId('chat-sandbox-profile-failed-media')).toContainText('registry unreachable')
    await shot(page, 'sbp-fallback')
  })

  test('offers a restart onto a newer image in words', async ({ page }) => {
    await stubChat(page, spView({ newer_image: true }))
    const row = await openMenu(page)
    await expect(row.getByTestId('chat-sandbox-profile-newer')).toHaveText('A newer image is available. It applies when the sandbox restarts.')
  })

  test('reads a refusal of the server in words', async ({ page }) => {
    const state = await stubChat(page, spView())
    state.putError = 'sandbox_profile_not_allowed'
    const row = await openMenu(page)
    await row.getByTestId('chat-sandbox-profile-media').click()
    await row.getByTestId('chat-sandbox-profile-confirm-yes').click()
    await expect(row.getByTestId('chat-sandbox-profile-error')).toHaveText('You may not use this profile.')
  })

  test('is not drawn when the commands are not isolated by a provider with profiles', async ({ page }) => {
    await stubChat(page, spView({ applies: false }))
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    await expect(page.getByTestId('chat-sandbox-profile')).toHaveCount(0)
  })
})

// A new chat has no id yet. The profile is picked in the Tools menu, kept on
// the page and saved with the chat once it is created.
async function stubDraft(page, { applies = true } = {}) {
  await mockAddons(page, { apps: [RUNNER], installed: [installedAddon({ id: 'sandbox', title: 'Sandbox' })] })
  const state = { calls: [], created: 0 }
  await page.route('**/api/chats/sandbox-profiles', route => route.fulfill({ json: { addon: applies ? 'sandbox' : '', default: 'base', profiles: applies ? PROFILES() : [] } }))
  await page.route('**/api/chats/draft/addons*', route => route.fulfill({ json: sandboxAddons }))
  await page.route('**/api/chats/draft/egress*', route => route.fulfill({ json: { ...egress, applies: false, applies_reason: 'none' } }))
  await page.route('**/api/chats/c1/events', route => route.fulfill({ status: 200, headers: { 'Content-Type': 'text/event-stream' }, body: '' }).catch(() => {}))
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => {
    if (route.request().method() === 'POST') { state.calls.push('send'); return route.fulfill({ json: { started: true } }) }
    return route.fulfill({ json: { messages: [] } })
  })
  await page.route('**/api/chats/c1/addons', route => route.fulfill({ json: sandboxAddons }))
  await page.route('**/api/chats/c1/egress', route => route.fulfill({ json: egress }))
  await page.route('**/api/chats/c1/sandbox-profile', route => {
    if (route.request().method() === 'PUT') {
      const { profile: id } = route.request().postDataJSON()
      state.calls.push(`profile:${id}`)
      return route.fulfill({ json: spView({ profile: id, selected: id, source: 'chat' }) })
    }
    return route.fulfill({ json: spView() })
  })
  await page.route('**/api/chats/c1', route => route.fulfill({ json: DETAIL }))
  await page.route(/\/api\/chats(\?.*)?$/, route => {
    if (route.request().method() === 'POST') { state.calls.push('create'); return route.fulfill({ json: DETAIL }) }
    return route.fulfill({ json: { conversations: [], persistent: true, files: true, files_reason: '' } })
  })
  return state
}

test.describe('Sandbox profile of a new chat', () => {
  test.beforeEach(async ({ page }) => { await mockChat(page) })

  test('the profiles can be picked before the first message, with no restart warning', async ({ page }) => {
    const state = await stubDraft(page)
    await page.goto('/app')
    await page.getByTestId('chat-tools-button').click()
    const row = page.getByTestId('chat-sandbox-profile')
    await expect(row.getByRole('radio', { name: 'Base' })).toHaveAttribute('aria-checked', 'true')
    await expect(row.getByTestId('chat-sandbox-profile-media')).toBeEnabled()
    await expect(row.getByTestId('chat-sandbox-profile-heavy')).toBeDisabled()
    await expect(row.getByTestId('chat-sandbox-profile-draft')).toHaveText('The new chat starts in Base. Pick a profile here, or change it later in the chat.')
    await row.getByTestId('chat-sandbox-profile-media').click()
    await expect(row.getByRole('radio', { name: 'Media' })).toHaveAttribute('aria-checked', 'true')
    await expect(row.getByTestId('chat-sandbox-profile-tools')).toContainText('ffmpeg')
    await expect(row.getByTestId('chat-sandbox-profile-draft')).toHaveText('The new chat starts in Media. Pick a profile here, or change it later in the chat.')
    await expect(row.getByTestId('chat-sandbox-profile-confirm')).toHaveCount(0)
    expect(state.calls).toEqual([])
    await shot(page, 'sbp-draft')
  })

  test('the pick is saved right after the chat is created, before the message goes', async ({ page }) => {
    const state = await stubDraft(page)
    await page.goto('/app')
    await page.getByTestId('chat-tools-button').click()
    await page.getByTestId('chat-sandbox-profile-media').click()
    await page.getByTestId('home-input').fill('convert this video')
    await page.getByTestId('home-input').press('Enter')
    await expect.poll(() => state.calls).toEqual(['create', 'profile:media', 'send'])
  })

  test('the empty chat page saves the pick too', async ({ page }) => {
    const state = await stubDraft(page)
    await page.goto('/app')
    // The app opens the empty chat page with a navigation state (see ChatsRoute).
    await page.evaluate(() => {
      window.history.pushState({ usr: { agentId: '' }, key: 'draft', idx: 1 }, '', '/app/chats')
      window.dispatchEvent(new PopStateEvent('popstate', { state: window.history.state }))
    })
    await expect(page.getByTestId('chat-input')).toBeVisible()
    await page.getByTestId('chat-tools-button').click()
    await page.getByTestId('chat-sandbox-profile-media').click()
    await page.getByTestId('chat-input').fill('convert this video')
    await page.getByTestId('chat-input').press('Enter')
    await expect.poll(() => state.calls).toEqual(['create', 'profile:media', 'send'])
  })

  test('choosing the default again saves nothing', async ({ page }) => {
    const state = await stubDraft(page)
    await page.goto('/app')
    await page.getByTestId('chat-tools-button').click()
    await page.getByTestId('chat-sandbox-profile-media').click()
    await page.getByTestId('chat-sandbox-profile-base').click()
    await page.getByTestId('home-input').fill('hello')
    await page.getByTestId('home-input').press('Enter')
    await expect.poll(() => state.calls).toEqual(['create', 'send'])
  })

  test('is not drawn when no sandbox with profiles runs the commands', async ({ page }) => {
    await stubDraft(page, { applies: false })
    await page.goto('/app')
    await page.getByTestId('chat-tools-button').click()
    await expect(page.getByTestId('chat-sandbox-profile')).toHaveCount(0)
  })
})

test.describe('Sandbox profiles control', () => {
  const ADMIN = { addon: 'sandbox', roles: ['admin', 'user'], profiles: [
    { ...profile('base', 'Base', 'The usual tools.', ['jq', 'git']), cpus: 2, memory_mib: 2048, disk_mib: 10240, has_previous: false },
    { ...profile('media', 'Media', 'Base plus ffmpeg.', ['jq', 'ffmpeg']), cpus: 2, memory_mib: 4096, disk_mib: 20480, has_previous: true },
  ] }
  async function open(page) {
    const state = await mockAddons(page, { apps: [RUNNER], installed: [installedAddon({ id: 'sandbox', title: 'Sandbox' })] })
    await page.route('**/api/chats/admin/sandbox-profiles', route => route.fulfill({ json: ADMIN }))
    await page.route('**/api/chats/sandbox-profiles', route => route.fulfill({ json: { addon: 'sandbox', default: 'media', profiles: ADMIN.profiles } }))
    await page.goto('/app/addons?detail=sandbox&view=settings')
    await expect(page.getByTestId('sandbox-profiles-settings')).toBeVisible({ timeout: 15_000 })
    return state
  }

  test('everyone may use every profile until one is restricted, and one Save writes only sandbox_profiles', async ({ page }) => {
    const state = await open(page)
    await expect(page.getByTestId('sandbox-profile-who-media')).toHaveText('Everyone')
    await expect(page.getByTestId('sandbox-profiles-save')).toBeDisabled()
    await expect(page.getByTestId('sandbox-profile-pin-base')).toBeDisabled()
    await expect(page.getByTestId('sandbox-profiles-default')).toHaveValue('media')
    await page.getByTestId('sandbox-profiles-default').selectOption('base')
    await page.getByTestId('sandbox-profile-role-media-admin').check()
    await page.getByTestId('sandbox-profile-users-media').fill('alice, bob')
    await expect(page.getByTestId('sandbox-profile-who-media')).toHaveText('Only the roles and users below')
    await page.getByTestId('sandbox-profile-pin-media').click()
    await page.getByTestId('sandbox-profiles-memory').fill('4096')
    await shot(page, 'sbp-admin')
    await page.getByTestId('sandbox-profiles-save').click()
    await expect.poll(() => state.log.hostSettings).toEqual([{ sandbox_profiles: {
      default: 'base', access: { media: { roles: ['admin'], users: ['alice', 'bob'] } }, memory_mib: 4096, disk_mib: 0, pin_previous: ['media'],
    } }])
    await expect(page.getByTestId('sandbox-profiles-save')).toBeDisabled()
    await expect(page.getByTestId('sandbox-profiles-memory')).toHaveValue('4096')
  })

  test('refuses a quota that is not a whole number', async ({ page }) => {
    await open(page)
    await page.getByTestId('sandbox-profiles-disk').fill('-3')
    await expect(page.getByTestId('sandbox-profiles-bad')).toBeVisible()
    await expect(page.getByTestId('sandbox-profiles-save')).toBeDisabled()
  })
})
