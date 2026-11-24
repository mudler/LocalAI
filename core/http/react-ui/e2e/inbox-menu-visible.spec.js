import { test, expect } from './coverage-fixtures.js'
import { makeInbox, openInbox, stubInbox } from './inbox-fixtures.js'

// The menu of the notification panel (the kebab in its header) opens in a
// portal on the page. It must sit above the panel, inside the viewport, and
// the first item must be what a click at its centre reaches. A panel with a
// scrolled list of notifications, a short window and a phone sheet are
// checked, in the light and the dark theme.

const panelOf = (page) => page.getByRole('dialog', { name: 'Notifications' })

async function openMenu(page) {
  await page.getByTestId('inbox-menu').getByRole('button').click()
  const menu = page.getByRole('menu', { name: 'More' })
  await expect(menu).toBeVisible()
  return menu
}

// The menu is inside the viewport and every item is the topmost element at its
// left, centre and right: the menu can overlap the panel only in part, so the
// centre alone is not enough.
async function expectUsable(page, menu) {
  const box = await menu.boundingBox()
  const vp = page.viewportSize()
  expect(box.x).toBeGreaterThanOrEqual(0)
  expect(box.y).toBeGreaterThanOrEqual(0)
  expect(box.x + box.width).toBeLessThanOrEqual(vp.width)
  expect(box.y + box.height).toBeLessThanOrEqual(vp.height)
  const items = menu.getByRole('menuitem')
  const count = await items.count()
  expect(count).toBeGreaterThan(0)
  for (let i = 0; i < count; i++) {
    const covered = await items.nth(i).evaluate((el) => {
      const r = el.getBoundingClientRect()
      const y = r.top + r.height / 2
      return [r.left + 6, r.left + r.width / 2, r.right - 6].some((x) => {
        const top = document.elementFromPoint(x, y)
        return !(top === el || el.contains(top))
      })
    })
    expect(covered, `menu item ${i} is covered`).toBe(false)
  }
}

for (const theme of ['light', 'dark']) {
  test.describe(`Inbox menu visible (${theme})`, () => {
    test.beforeEach(async ({ page }) => {
      await page.addInitScript((t) => { try { localStorage.setItem('localai-theme', t) } catch { /* private window */ } }, theme)
    })

    test('the menu is above the panel and the first item takes the click', async ({ page }) => {
      await stubInbox(page, makeInbox(3, [1]))
      await openInbox(page)
      await expectUsable(page, await openMenu(page))
    })

    test('with a long list scrolled to the last row the menu stays usable', async ({ page }) => {
      await stubInbox(page, makeInbox(40))
      await openInbox(page)
      const items = panelOf(page).getByTestId('inbox-item')
      await items.last().scrollIntoViewIfNeeded()
      await expectUsable(page, await openMenu(page))
    })

    test('in a short window the menu stays inside the viewport', async ({ page }) => {
      await page.setViewportSize({ width: 1280, height: 640 })
      await stubInbox(page, makeInbox(40))
      await openInbox(page)
      await expectUsable(page, await openMenu(page))
    })

    test('on a phone the sheet menu is above the sheet', async ({ page }) => {
      await page.setViewportSize({ width: 390, height: 780 })
      await stubInbox(page, makeInbox(10))
      await openInbox(page)
      await expectUsable(page, await openMenu(page))
    })

    test('Escape and a click outside close the menu', async ({ page }) => {
      await stubInbox(page, makeInbox(3))
      await openInbox(page)
      const menu = await openMenu(page)
      await page.keyboard.press('Escape')
      await expect(menu).toBeHidden()
      await expect(panelOf(page)).toBeVisible()
      await expect(page.getByTestId('inbox-menu').getByRole('button')).toBeFocused()
      await openMenu(page)
      await panelOf(page).getByRole('heading', { name: 'Notifications' }).click()
      await expect(page.getByRole('menu', { name: 'More' })).toBeHidden()
    })
  })
}
