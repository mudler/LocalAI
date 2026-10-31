import { test, expect, request as playwrightRequest } from '@playwright/test'
import fs from 'node:fs'
import { mockChat } from './chat-fixtures.js'

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

test.describe.configure({ mode: 'serial' })

let admin
test.beforeAll(async () => { admin = await apiAs('admin') })
test.afterAll(async () => { await admin.dispose() })

test.describe('Host UI slots', () => {
  test('slots: an add-on\'s settings sections are drawn on the Settings tab of its own details and nowhere else', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'admin')
    await mockChat(page)
    await page.goto('/app/addons?detail=slots&view=settings')
    await expect(page.getByTestId('addon-detail-tab-settings')).toHaveAttribute('aria-selected', 'true', { timeout: 25_000 })
    const section = page.getByTestId('addon-settings-section')
    // Two contributions: a working card and one that throws.
    await expect(section).toHaveCount(2, { timeout: 25_000 })
    // Only this add-on's sections, and no heading that repeats its name.
    await expect(page.locator('[data-testid="addon-settings-section"]:not([data-addon-section="slots"])')).toHaveCount(0)
    await expect(page.locator('[data-testid="addon-detail"] .ad-section__head')).toHaveCount(0)
    await expect(page.getByTestId('fx-settings')).toContainText('Settings contributed by the slots fixture.')
    // A section is a block root, so its form keeps its own layout.
    expect(await page.locator('[data-testid="addon-settings-section"] > .ad-slot').first().evaluate(n => getComputedStyle(n).display)).toBe('block')
    // The failing section is a marker in its own section; the pane stays.
    await expect(section.nth(1).getByTestId('addon-slot-unavailable')).toBeVisible()
    await expect(page.getByTestId('addon-detail')).toBeVisible()
    // The Overview tab is the details as before.
    await page.getByTestId('addon-detail-tab-overview').click()
    await expect(page).not.toHaveURL(/view=settings/)
    await expect(page.getByTestId('preview-facts')).toBeVisible()
    await expect(page.getByTestId('addon-settings-section')).toHaveCount(0)
    // The Settings page draws no add-on section any more.
    await page.goto('/app/settings')
    await expect(page.getByTestId('settings-page')).toBeVisible()
    await expect(page.locator('[data-group="addons"]')).toBeVisible({ timeout: 25_000 })
    await expect(page.getByTestId('addon-settings-sections')).toHaveCount(0)
    await shoot(page, 'slots-settings-tab')
    await context.close()
  })

  test('slots: the Settings page lists the add-ons that have settings, each a link to its Settings tab', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'admin')
    await mockChat(page)
    await page.goto('/app/settings')
    const group = page.locator('.st-group[data-group="addons"]')
    await expect(group).toBeVisible({ timeout: 25_000 })
    await group.click()
    await expect(group).toHaveAttribute('aria-current', 'true')
    const row = page.locator('[data-testid="settings-addon"][data-app="slots"]')
    await expect(row).toBeVisible()
    await expect(row).toContainText('Slots fixture')
    await row.getByTestId('settings-addon-link').click()
    await expect(page).toHaveURL(/\/app\/addons\?detail=slots&view=settings$/)
    await expect(page.getByTestId('addon-settings-section')).toHaveCount(2, { timeout: 25_000 })
    // ?group= opens a group directly.
    await page.goto('/app/settings?group=addons')
    await expect(page.getByTestId('settings-addons')).toBeVisible({ timeout: 25_000 })
    await context.close()
  })

  test('slots: the old anchor of a section on the Settings page opens the add-on\'s Settings tab', async ({ browser }) => {
    const { context, page } = await browserAs(browser, 'admin')
    await mockChat(page)
    await page.goto('/app/settings#ad-section-slots-settings-card')
    await expect(page).toHaveURL(/\/app\/addons\?detail=slots&view=settings$/, { timeout: 25_000 })
    await expect(page.getByTestId('addon-settings-section')).toHaveCount(2, { timeout: 25_000 })
    await context.close()
  })

  test('slots: the add-on page and its details offer the settings to an administrator only', async ({ browser }) => {
    const admin = await browserAs(browser, 'admin')
    await mockChat(admin.page)
    await admin.page.goto('/app/addons/slots/home')
    const link = admin.page.getByTestId('addon-page-settings')
    await expect(link).toBeVisible({ timeout: 25_000 })
    await link.click()
    await expect(admin.page).toHaveURL(/\/app\/addons\?detail=slots&view=settings$/)
    await admin.context.close()
    const member = await browserAs(browser, 'member')
    await mockChat(member.page)
    await member.page.goto('/app/addons/slots/home')
    await expect(member.page.getByTestId('addon-surface')).toBeVisible({ timeout: 25_000 })
    await expect(member.page.getByTestId('addon-page-settings')).toHaveCount(0)
    await member.page.goto('/app/addons?detail=slots&view=settings')
    await expect(member.page.getByTestId('addon-detail')).toBeVisible({ timeout: 25_000 })
    await expect(member.page.getByTestId('addon-detail-tab-settings')).toHaveCount(0)
    await expect(member.page.getByTestId('addon-settings-section')).toHaveCount(0)
    await member.context.close()
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

})
