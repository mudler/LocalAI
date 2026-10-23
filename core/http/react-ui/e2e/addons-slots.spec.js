import { test, expect, request as playwrightRequest } from '@playwright/test'
import fs from 'node:fs'
import { randomUUID } from 'node:crypto'
import { chatOf, pair, mockChat } from './chat-fixtures.js'
import { seedChats, setTheme } from './home-fixtures.js'

// Host UI slots. The UI test server runs with real authentication and a real
// add-on owner; the "slots" fixture package (tests/addons/fixture/ui/slots) is
// installed after the host build from a loopback catalog. It contributes to
// every host slot, with a contribution that throws and a style sheet that
// collides with host classes. Disabling and enabling are the real lifecycle
// routes; every contribution the page draws comes from the owner's apps list
// and the module the owner serves. Only the chat models, the system probes and
// the stored proposal of the approval card are stubbed.
//
// The specs change the owner's state and run in order on one worker.

const creds = JSON.parse(fs.readFileSync(process.env.ADDONS_FIXTURE, 'utf8'))
const BASE = process.env.ADDONS_UI_URL
const shots = process.env.ADDONS_SHOTS
const SECRET = 'SECRET-MESSAGE-TEXT-7731'

async function shoot(page, name) {
  if (!shots) return
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.waitForTimeout(400)
  await page.screenshot({ path: `${shots}/${name}.png`, fullPage: false })
}

async function browserAs(browser, who, extra = {}) {
  const context = await browser.newContext({ baseURL: BASE, ...extra })
  await context.addCookies([{ name: 'session', value: creds.users[who].session, url: BASE }])
  const page = await context.newPage()
  return { context, page }
}

async function apiAs(who) {
  const user = creds.users[who]
  return playwrightRequest.newContext({ baseURL: BASE, extraHTTPHeaders: { Authorization: `Bearer ${user.token}` } })
}

async function appOf(api, id) {
  const response = await api.get('/api/addons/apps')
  expect(response.status()).toBe(200)
  return (await response.json()).apps.find(a => a.id === id)
}

async function lifecycle(api, id, verb) {
  const app = await appOf(api, id)
  const response = await api.post(`/api/addons/apps/${id}/${verb}`, { data: { operationId: randomUUID(), generation: app.generation } })
  expect(response.status(), `${verb} ${id}`).toBe(200)
}

const history = pair(SECRET, 'An answer that must stay in the page.')
async function openChat(page) {
  await mockChat(page)
  await seedChats(page, [chatOf('c1', 'Slots', 'qwen3-8b', history)], 'c1')
  await page.goto('/app/chat')
  await page.getByTestId('chat-composer').waitFor()
}

const menuButton = (page) => page.getByTestId('addon-composer-menu')
const actionOf = (page, id) => page.locator(`[data-addon-action="${id}"]`)
const noOverflow = (page) => page.evaluate(() => document.scrollingElement.scrollWidth <= window.innerWidth)

test.describe.configure({ mode: 'serial' })

let admin
test.beforeAll(async () => { admin = await apiAs('admin') })
test.afterAll(async () => { await admin.dispose() })

test.describe('Host UI slots', () => {
  test('slots: the composer overflow menu holds the declared composer actions and components only', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'member')
    await openChat(page)
    const button = menuButton(page)
    await expect(button).toBeVisible({ timeout: 25_000 })
    await expect(button).toHaveAttribute('aria-haspopup', 'menu')
    await expect(button).toHaveAttribute('aria-expanded', 'false')
    // Closed, no contribution of the add-on is mounted.
    await expect(page.getByTestId('fx-composer-chip')).toHaveCount(0)
    await button.click()
    await expect(button).toHaveAttribute('aria-expanded', 'true')
    const menu = page.getByRole('menu')
    await expect(menu.getByRole('menuitem')).toHaveText(['Summarize chat', 'Review a request'])
    // The component contribution is drawn in its own root, with the slot props only.
    const chip = menu.getByTestId('fx-composer-chip')
    await expect(chip).toBeVisible()
    expect(JSON.parse(await chip.getAttribute('data-props'))).toEqual({ conversationId: 'c1', disabled: false })
    // Message-placement actions and components never appear in the composer menu.
    await expect(menu.locator('[data-addon-action="inspect"], [data-testid="fx-message-chip"]')).toHaveCount(0)
    // The throwing contribution is a marker; the page and the composer keep working.
    await expect(menu.getByTestId('addon-slot-unavailable')).toHaveText('Unavailable')
    await expect(page.getByTestId('chat-input')).toBeEditable()
    await page.getByTestId('chat-input').fill('still typing')
    await expect(page.getByTestId('chat-input')).toHaveValue('still typing')
    // A module export the manifest does not declare is never drawn.
    await expect(page.getByTestId('fx-rogue')).toHaveCount(0)
    await shoot(page, 'slots-composer-menu-dark')
    await context.close()
  })

  test('slots: a tool action calls only its declared own tool and draws the declared result renderer', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'member')
    const calls = []
    page.on('request', (r) => {
      if (r.url().endsWith('/api/addons/dispatch') && r.method() === 'POST') calls.push(JSON.parse(r.postData()))
    })
    await openChat(page)
    // The fixture owner has no resource integration, so the real gateway
    // denies every read: the host says so in a dialog and draws no result.
    await menuButton(page).click()
    await actionOf(page, 'summarize').click()
    await expect(page.getByTestId('addon-action-failed')).toHaveText('Summarize chat could not be completed.', { timeout: 25_000 })
    await page.keyboard.press('Escape')
    await expect(page.getByRole('dialog')).toHaveCount(0)
    // The success path: only the answer of the dispatch route is stubbed. The
    // request is the real one the host built.
    await page.route('**/api/addons/dispatch', async (route) => {
      const body = route.request().postDataJSON()
      await route.fulfill({ json: { payload: { actor: 'member', arguments: body.arguments } } })
    })
    calls.length = 0
    await menuButton(page).click()
    await actionOf(page, 'summarize').click()
    const card = page.getByTestId('fx-result-card')
    await expect(card).toBeVisible({ timeout: 25_000 })
    await expect(card).toContainText('Summary of c1')
    await expect(card).toContainText('tool summarize')
    const reads = calls.filter(c => c.action === 'read')
    expect(reads).toHaveLength(1)
    expect(reads[0].addonId).toBe('slots')
    expect(reads[0].arguments).toEqual({ tool: 'summarize', context: { conversationId: 'c1' } })
    // No conversation text left the page with the call.
    expect(JSON.stringify(calls)).not.toContain(SECRET)
    await shoot(page, 'slots-tool-result')
    await page.keyboard.press('Escape')
    await expect(card).toHaveCount(0)
    await context.close()
  })

  test('slots: message actions sit in the message action row and carry no message text', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'member')
    await openChat(page)
    const assistant = page.locator('[data-testid="chat-message"][data-role="assistant"]')
    const row = assistant.getByTestId('message-actions')
    await expect(actionOf(page, 'inspect')).toHaveCount(2, { timeout: 25_000 })
    await expect(row.locator('[data-addon-action="inspect"]')).toHaveAttribute('aria-label', 'Inspect message')
    await expect(row.locator('[data-addon-action="explode"]')).toHaveCount(1)
    // Composer-placement actions are not drawn on messages.
    await expect(page.locator('[data-testid="chat-message"] [data-addon-action="summarize"]')).toHaveCount(0)
    const props = await row.getByTestId('fx-message-chip').getAttribute('data-props')
    expect(JSON.parse(props)).toEqual({ conversationId: 'c1', messageId: '1', role: 'assistant' })
    // Nothing the contributions were given or drew holds the text of any message.
    const attributes = await page.evaluate(() => Array.from(document.querySelectorAll('[data-props]')).map(n => n.getAttribute('data-props')))
    expect(attributes.join(' ')).not.toContain(SECRET)
    expect(attributes.join(' ')).not.toContain('An answer that must stay')
    await shoot(page, 'slots-message-actions')
    await context.close()
  })

  test('slots: a component action opens the add-on component in a host dialog and a throwing one is contained', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'member')
    await openChat(page)
    const assistant = page.locator('[data-testid="chat-message"][data-role="assistant"]')
    await assistant.locator('[data-addon-action="inspect"]').click()
    const dialog = page.getByRole('dialog')
    await expect(dialog).toBeVisible()
    await expect(dialog).toContainText('Inspect message')
    expect(JSON.parse(await dialog.getByTestId('fx-inspect').getAttribute('data-props'))).toEqual({ conversationId: 'c1', messageId: '1', role: 'assistant' })
    await page.keyboard.press('Escape')
    await expect(dialog).toHaveCount(0)
    // A component that throws leaves the contained error card and the chat page.
    await assistant.locator('[data-addon-action="explode"]').click()
    await expect(page.getByRole('dialog').getByTestId('addon-error')).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(page.getByRole('dialog')).toHaveCount(0)
    await expect(page.getByTestId('chat-composer')).toBeVisible()
    await expect(page.locator('[data-testid="chat-message"]')).toHaveCount(2)
    await context.close()
  })

  test('slots: the host approval frame owns Confirm and Cancel and the card only draws details', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'member')
    const posts = []
    const proposal = {
      id: 'op-fixture-1', operationId: 'op-fixture-1', addonId: 'slots', resourceId: 'r1', generation: 1, action: 'write',
      arguments: { tool: 'ping', rows: 2 }, revision: { schema: 1, data: 1 }, digest: 'sha256:' + 'c'.repeat(64),
      effects: { count: 2, sample: [{ row: 1 }, { row: 2 }] }, state: 'pending',
      expiresAt: new Date(Date.now() + 5 * 60_000).toISOString(), createdAt: new Date().toISOString(),
    }
    await page.route('**/api/addons/proposals/op-fixture-1', route => route.fulfill({ json: proposal }))
    await page.route('**/api/addons/proposals/op-fixture-1/confirm', (route) => {
      posts.push({ headers: route.request().headers(), body: route.request().postDataJSON() })
      route.fulfill({ json: { ...proposal, state: 'applied' } })
    })
    await openChat(page)
    await menuButton(page).click()
    await actionOf(page, 'approve').click()
    await page.getByRole('button', { name: 'Show the request' }).click()
    const frame = page.getByTestId('approval-frame')
    await expect(frame).toBeVisible()
    await expect(frame.getByTestId('approval-count')).toHaveText('2')
    const card = frame.getByTestId('fx-card')
    await expect(card).toContainText('Card for ping: 2 changes')
    // The card's own button does nothing to the proposal.
    await card.getByRole('button', { name: 'Card button' }).click()
    expect(posts).toHaveLength(0)
    // Confirm and Cancel are the host's, outside the card's root.
    const confirm = frame.getByTestId('approval-confirm')
    expect(await confirm.evaluate(n => !n.closest('[data-addon]'))).toBe(true)
    expect(await frame.getByTestId('approval-cancel').evaluate(n => !n.closest('[data-addon]'))).toBe(true)
    await shoot(page, 'slots-approval-frame')
    await confirm.click()
    await expect(frame.getByTestId('approval-state')).toHaveText('Applied')
    expect(posts).toHaveLength(1)
    expect(posts[0].headers['x-localai-confirm']).toBe('op-fixture-1')
    expect(posts[0].body).toEqual({ digest: proposal.digest })
    await expect(confirm).toBeDisabled()
    await context.close()
  })

  test('slots: the settings page ends with a host-owned section for each contributing add-on', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'admin')
    await mockChat(page)
    await page.goto('/app/settings')
    await expect(page.getByTestId('settings-page')).toBeVisible()
    const section = page.getByTestId('addon-settings-section')
    // Two contributions: a working card and one that throws.
    await expect(section).toHaveCount(2, { timeout: 25_000 })
    await expect(page.locator('.ad-section__title')).toHaveText(['Slots fixture', 'Slots fixture'])
    await expect(page.getByTestId('fx-settings')).toContainText('Settings contributed by the slots fixture.')
    // The failing section is a marker under its host heading; the settings page stays.
    await expect(page.getByTestId('addon-slot-unavailable')).toHaveCount(1)
    await expect(section.nth(1).getByTestId('addon-slot-unavailable')).toBeVisible()
    // The section is last in the page, after the host's own content.
    const order = await page.evaluate(() => {
      const page = document.querySelector('[data-testid="settings-page"]')
      const host = page.querySelector('.st-bar')
      const sections = page.querySelector('[data-testid="addon-settings-sections"]')
      return Boolean(host.compareDocumentPosition(sections) & Node.DOCUMENT_POSITION_FOLLOWING)
    })
    expect(order).toBe(true)
    await section.first().scrollIntoViewIfNeeded()
    await shoot(page, 'slots-settings-section')
    await context.close()
  })

  test('slots: the navigation slot adds its entry below the tile grid of the Add-ons drawer', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'member')
    await mockChat(page)
    await page.goto('/app/addons')
    const nav = page.getByTestId('addon-nav-slot')
    await expect(nav).toBeVisible({ timeout: 25_000 })
    const link = nav.getByTestId('fx-nav')
    await expect(link).toHaveText('Slots home')
    // It is in the slot container of the drawer page, after the host tiles.
    const inside = await link.evaluate(n => Boolean(n.closest('.ad-nav-slot')))
    expect(inside).toBe(true)
    const below = await page.evaluate(() => {
      const tiles = document.querySelector('.ad-tiles')
      const slot = document.querySelector('.ad-nav-slot')
      return Boolean(tiles.compareDocumentPosition(slot) & Node.DOCUMENT_POSITION_FOLLOWING)
    })
    expect(below).toBe(true)
    await link.click()
    await expect(page).toHaveURL(/\/app\/addons\/slots\/home$/)
    await shoot(page, 'slots-nav-entry')
    await context.close()
  })

  test('slots: add-on style sheets that collide with host class names change no host node', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'member')
    await openChat(page)
    await expect(menuButton(page)).toBeVisible({ timeout: 25_000 })
    const probe = await page.evaluate(() => {
      const s = (el) => getComputedStyle(el)
      const acts = document.querySelector('[data-testid="message-actions"]')
      const attach = document.querySelector('.home-attach-btn')
      return {
        acts: s(acts).display,
        attachWidth: attach.getBoundingClientRect().width,
        attachBackground: s(attach).backgroundColor,
        body: s(document.body).backgroundColor,
        sheet: document.querySelector('style[data-addon-style="slots"]')?.textContent || '',
      }
    })
    expect(probe.sheet).toContain('[data-addon="slots"]')
    expect(probe.acts).not.toBe('none')
    expect(probe.attachWidth).toBeGreaterThan(8)
    expect(probe.attachBackground).not.toBe('rgb(255, 0, 0)')
    expect(probe.body).not.toBe('rgb(255, 0, 0)')
    await context.close()
  })

  test('slots: keyboard users open the menu, move through it, close it with Escape and run an action', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'member')
    await openChat(page)
    const button = menuButton(page)
    await expect(button).toBeVisible({ timeout: 25_000 })
    await button.focus()
    await page.keyboard.press('Enter')
    const items = page.getByRole('menuitem')
    await expect(items.first()).toBeFocused()
    await page.keyboard.press('ArrowDown')
    await expect(items.nth(1)).toBeFocused()
    await page.keyboard.press('ArrowDown')
    await expect(items.first()).toBeFocused()
    await page.keyboard.press('Escape')
    await expect(page.getByRole('menu')).toHaveCount(0)
    await expect(button).toBeFocused()
    await page.keyboard.press('Enter')
    await page.keyboard.press('ArrowDown')
    await page.keyboard.press('Enter')
    await expect(page.getByRole('dialog')).toContainText('Review a request')
    await page.keyboard.press('Escape')
    await expect(page.getByRole('dialog')).toHaveCount(0)
    await context.close()
  })

  test('slots: the layout keeps inside a 400 px screen in the light theme', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'admin', { viewport: { width: 400, height: 800 } })
    await setTheme(page, 'light')
    await openChat(page)
    await expect(menuButton(page)).toBeVisible({ timeout: 25_000 })
    expect(await noOverflow(page)).toBe(true)
    await menuButton(page).click()
    const box = await page.getByRole('menu').boundingBox()
    expect(box.x).toBeGreaterThanOrEqual(0)
    expect(box.x + box.width).toBeLessThanOrEqual(400)
    await shoot(page, 'slots-narrow-menu-light')
    await page.keyboard.press('Escape')
    await expect(actionOf(page, 'inspect').last()).toBeVisible()
    expect(await noOverflow(page)).toBe(true)
    await page.goto('/app/settings')
    await expect(page.getByTestId('addon-settings-section').first()).toBeVisible({ timeout: 25_000 })
    expect(await noOverflow(page)).toBe(true)
    await page.getByTestId('addon-settings-section').first().scrollIntoViewIfNeeded()
    await shoot(page, 'slots-narrow-settings-light')
    await context.close()
  })

  test('slots: a caller without the add-ons feature sees no contribution and the server denies the tool call', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'outsider')
    const apps = []
    page.on('response', (r) => { if (/\/api\/addons\/apps$/.test(r.url())) apps.push(r.status()) })
    await openChat(page)
    await expect(page.getByTestId('chat-composer')).toBeVisible()
    await expect(page.locator('[data-testid="chat-message"]')).toHaveCount(2)
    await expect(menuButton(page)).toHaveCount(0)
    await expect(page.locator('[data-addon-action]')).toHaveCount(0)
    await expect(page.getByTestId('addon-composer')).toHaveCount(0)
    await page.goto('/app/settings')
    await expect(page.getByTestId('addon-settings-section')).toHaveCount(0)
    // Hiding is never the control: the same call through the real gateway is denied.
    const app = await appOf(admin, 'slots')
    for (const who of ['outsider']) {
      const api = await apiAs(who)
      const response = await api.post('/api/addons/dispatch', {
        data: { operationId: randomUUID(), addonId: 'slots', generation: app.generation, action: 'read', arguments: { tool: 'summarize', context: { conversationId: 'c1' } } },
      })
      expect(response.status()).toBe(403)
      expect((await api.get('/api/addons/apps')).status()).toBe(403)
      await api.dispose()
    }
    // The member holds the feature and sees the contributions; the owner has
    // no resource integration for this fixture, so a read is denied for them too.
    const member = await apiAs('member')
    expect((await member.get('/api/addons/apps')).status()).toBe(200)
    const denied = await member.post('/api/addons/dispatch', {
      data: { operationId: randomUUID(), addonId: 'slots', generation: app.generation, action: 'read', arguments: { tool: 'summarize', context: { conversationId: 'c1' } } },
    })
    expect(denied.status()).toBe(403)
    await member.dispose()
    expect(apps.every(s => s === 403 || s === 401)).toBe(true)
    await context.close()
  })

  test('slots: disabling the add-on through the real owner removes every contribution and enabling brings them back', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'member')
    await openChat(page)
    await expect(menuButton(page)).toBeVisible({ timeout: 25_000 })
    await expect(actionOf(page, 'inspect')).toHaveCount(2)
    await menuButton(page).click()
    await expect(page.getByTestId('fx-composer-chip')).toBeVisible()
    await lifecycle(admin, 'slots', 'stop')
    // The owner hides a stopped app from readers; the next list unmounts it all.
    await expect(menuButton(page)).toHaveCount(0, { timeout: 30_000 })
    await expect(page.getByTestId('fx-composer-chip')).toHaveCount(0)
    await expect(actionOf(page, 'inspect')).toHaveCount(0)
    await expect(page.locator('[data-addon="slots"]')).toHaveCount(0)
    await expect(page.locator('style[data-addon-style="slots"]')).toHaveCount(0)
    const log = await page.evaluate(() => window.__slotsFixture.events)
    expect(log).toContain('abort:1')
    expect(log).toContain('dispose:1')
    // The chat itself is untouched.
    await expect(page.getByTestId('chat-composer')).toBeVisible()
    await expect(page.locator('[data-testid="chat-message"]')).toHaveCount(2)
    await shoot(page, 'slots-after-disable')
    await lifecycle(admin, 'slots', 'start')
    await expect.poll(async () => (await appOf(admin, 'slots')).state, { timeout: 30_000 }).toBe('running')
    // A list that holds no contribution is not polled again, so the add-on
    // returns with the next page load.
    await page.reload()
    await expect(menuButton(page)).toBeVisible({ timeout: 30_000 })
    await expect(actionOf(page, 'inspect')).toHaveCount(2)
    await context.close()
  })
})
