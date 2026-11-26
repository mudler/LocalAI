import { test, expect } from './coverage-fixtures.js'
import { inboxButton, stubInbox, makeInbox } from './inbox-fixtures.js'

// A deploy replaces the whole content-hashed asset set at once. A tab that is
// holding an older index.html (or one whose request lands on a replica that
// has not been swapped yet, which is the normal state during a rolling update)
// asks for a page chunk that the server no longer has and gets a 404. The
// dynamic import rejects and, without handling, React Router's default error
// boundary replaces the app with "Unexpected Application Error!".
//
// These specs pin the recovery: reload once to pick up the current index.html,
// and no more than once, so a chunk that is genuinely missing surfaces the
// error instead of reloading forever.
const HOME_CHUNK = /\/assets\/Home-[^/]*\.js(\?.*)?$/
const HOME_CSS = /\/assets\/Home-[^/]*\.css(\?.*)?$/
// A lazy component, not a route page: the notification panel loads on the
// first opening, which can be long after the tab loaded the shell.
const INBOX_CHUNK = /\/assets\/InboxPanel-[^/]*\.js(\?.*)?$/
const notFound = route => route.fulfill({ status: 404, contentType: 'text/plain', body: 'Not Found' })

test.describe('@production-chunks Stale chunk recovery', () => {
  test('a 404 on a page chunk recovers on its own', async ({ page }) => {
    let served404 = 0
    await page.route(HOME_CHUNK, route => {
      // Only the first request 404s: that is the deploy window closing behind
      // the tab. The reload then finds the chunk where it should be.
      if (served404 === 0) {
        served404++
        return notFound(route)
      }
      return route.continue()
    })

    await page.goto('/app')

    await expect(page.locator('.home-page')).toBeVisible()
    expect(served404).toBe(1)
  })

  test('a chunk that stays missing does not reload in a loop', async ({ page }) => {
    let attempts = 0
    await page.route(HOME_CHUNK, route => {
      attempts++
      return notFound(route)
    })

    await page.goto('/app')

    // The initial load plus exactly one reload. Anything more is a reload loop,
    // which is worse than the error screen: it never settles and never says why.
    await expect.poll(() => attempts, { timeout: 10_000 }).toBe(2)
    await page.waitForTimeout(3_000)
    expect(attempts).toBe(2)
    await expect(page.locator('.home-page')).toHaveCount(0)
    // The error screen of the app, inside the layout, not React Router's
    // developer screen.
    const screen = page.getByTestId('route-error')
    await expect(screen).toBeVisible()
    await expect(screen).toHaveAttribute('data-kind', 'updated')
    await expect(screen.getByRole('heading', { name: 'LocalAI was updated' })).toBeVisible()
    await expect(page.locator('.sidebar')).toBeVisible()
    await expect(page.getByText('Unexpected Application Error')).toHaveCount(0)

    // Reload picks up the build once the server has it.
    await page.unroute(HOME_CHUNK)
    await screen.getByRole('button', { name: 'Reload' }).click()
    await expect(page.locator('.home-page')).toBeVisible()
  })

  test('a 404 on the CSS of a page chunk recovers on its own', async ({ page }) => {
    let served404 = 0
    await page.route(HOME_CSS, route => {
      if (served404 === 0) {
        served404++
        return notFound(route)
      }
      return route.continue()
    })

    await page.goto('/app')

    await expect(page.locator('.home-page')).toBeVisible()
    expect(served404).toBe(1)
    await expect(page.getByTestId('route-error')).toHaveCount(0)
  })

  test('a 404 on the notification panel chunk reloads, and the panel opens after', async ({ page }) => {
    await stubInbox(page, makeInbox(2))
    let served404 = 0
    await page.route(INBOX_CHUNK, route => {
      if (served404 === 0) {
        served404++
        return notFound(route)
      }
      return route.continue()
    })

    await page.goto('/app/chats/c1')
    const reloaded = page.waitForEvent('load')
    await inboxButton(page).click()
    await reloaded
    expect(served404).toBe(1)
    await expect(page.getByText('Unexpected Application Error')).toHaveCount(0)

    await inboxButton(page).click()
    await expect(page.getByRole('dialog', { name: 'Notifications' })).toBeVisible()
  })

  test('a notification panel chunk that stays missing shows the update screen', async ({ page }) => {
    await stubInbox(page, makeInbox(2))
    let attempts = 0
    await page.route(INBOX_CHUNK, route => { attempts++; return notFound(route) })

    await page.goto('/app/chats/c1')
    const reloaded = page.waitForEvent('load')
    await inboxButton(page).click()
    await reloaded
    // The reload spent the one try: the next failure is shown, not reloaded.
    await inboxButton(page).click()

    const screen = page.getByTestId('route-error')
    await expect(screen).toBeVisible()
    await expect(screen).toHaveAttribute('data-kind', 'updated')
    await expect(page.getByText('Unexpected Application Error')).toHaveCount(0)
    expect(attempts).toBe(2)

    await page.unroute(INBOX_CHUNK)
    await screen.getByRole('button', { name: 'Reload' }).click()
    await expect(page.locator('.sidebar')).toBeVisible()
    await expect(page.getByTestId('route-error')).toHaveCount(0)
    await inboxButton(page).click()
    await expect(page.getByRole('dialog', { name: 'Notifications' })).toBeVisible()
  })
})
