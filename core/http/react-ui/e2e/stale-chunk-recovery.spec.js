import { test, expect } from './coverage-fixtures.js'

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

test.describe('Stale chunk recovery', () => {
  test('a 404 on a page chunk recovers on its own', async ({ page }) => {
    let served404 = 0
    await page.route(HOME_CHUNK, route => {
      // Only the first request 404s: that is the deploy window closing behind
      // the tab. The reload then finds the chunk where it should be.
      if (served404 === 0) {
        served404++
        return route.fulfill({ status: 404, contentType: 'text/plain', body: 'Not Found' })
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
      return route.fulfill({ status: 404, contentType: 'text/plain', body: 'Not Found' })
    })

    await page.goto('/app')

    // The initial load plus exactly one reload. Anything more is a reload loop,
    // which is worse than the error screen: it never settles and never says why.
    await expect.poll(() => attempts, { timeout: 10_000 }).toBe(2)
    await page.waitForTimeout(3_000)
    expect(attempts).toBe(2)
    await expect(page.locator('.home-page')).toHaveCount(0)
  })
})
