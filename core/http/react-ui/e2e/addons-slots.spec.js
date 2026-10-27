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

})
