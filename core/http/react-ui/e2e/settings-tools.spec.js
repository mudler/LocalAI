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

  test('switching bash off saves off', async ({ page }) => {
    const state = await openTools(page, [bash()])
    await page.getByTestId('settings-tools-bash-switch').click()
    await expect(page.getByTestId('settings-tools-bash-mode')).toHaveCount(0)
    await page.getByRole('button', { name: 'Apply' }).click()
    await expect.poll(() => state.posts).toEqual([{ chats_host_exec: 'off' }])
  })

  test('with two add-ons that replace bash and no choice, it asks to choose one and saves the pin', async ({ page }) => {
    const state = await openTools(page, [bash({ provider: 'other', candidates: ['other', 'sandbox'] })])
    await expect(page.getByTestId('settings-tools-bash-unpinned')).toHaveText('2 add-ons replace bash: choose which one to use.')
    await expect(page.getByTestId('settings-tools-bash-provider')).toHaveText('Add-on: other')
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
    await expect(warning).toHaveText('This add-on replaces the built-in bash. Another add-on (Sandbox) already does. Only one can be active; you choose which in Settings > Tools.')
    await expect(page.getByTestId('install-dialog').getByRole('button', { name: 'Install and turn on' })).toBeEnabled()
    await shot(page, 'install-shadow-warning')
  })
})
