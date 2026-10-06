import { test, expect } from './coverage-fixtures.js'

// The collapsed sidebar is a single icon column. The signed-in avatar sat about
// ten pixels right of it: its link had a zero flex basis, so the icon spilled out
// of the link from the link's left edge instead of being centred in it.

const signedIn = (page) =>
  page.route('**/api/auth/status', (route) =>
    route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({
        authEnabled: true,
        staticApiKeyRequired: false,
        providers: ['local'],
        user: { id: 'u1', name: 'Admin', role: 'admin', provider: 'local' },
      }),
    })
  )

// Centre x of every icon in the sidebar, keyed by what it is.
const centres = (page) =>
  page.evaluate(() => {
    const cx = (el) => {
      const b = el.getBoundingClientRect()
      return b.x + b.width / 2
    }
    const one = (sel) => {
      const el = document.querySelector(sel)
      return el ? cx(el) : null
    }
    return {
      nav: [...document.querySelectorAll('.sidebar-nav .nav-icon')].map(cx),
      mark: one('.sidebar-logo-icon-img'),
      avatar: one('.sidebar-user-avatar, .sidebar-user-avatar-icon'),
      language: one('.language-switcher-trigger i'),
      theme: one('.theme-toggle__icon'),
      collapse: one('.sidebar-collapse-btn i'),
    }
  })

for (const theme of ['light', 'dark']) {
  test.describe(`sidebar icon column (${theme})`, () => {
    test.beforeEach(async ({ page }) => {
      await page.addInitScript((t) => localStorage.setItem('localai-theme', t), theme)
      await signedIn(page)
    })

    test('collapsed: nav icons, mark, avatar, language, theme and collapse share one centre', async ({ page }) => {
      await page.addInitScript(() => localStorage.setItem('localai_sidebar_collapsed', 'true'))
      await page.goto('/app')
      await expect(page.locator('.sidebar.collapsed')).toBeVisible({ timeout: 15_000 })
      await expect(page.locator('.sidebar-user-avatar, .sidebar-user-avatar-icon')).toBeVisible()
      // Let the width transition and the icon fonts settle before measuring.
      await page.waitForTimeout(600)
      const c = await centres(page)
      const column = c.nav[0]
      expect(c.nav.length).toBeGreaterThan(3)
      for (const x of c.nav) expect(Math.abs(x - column)).toBeLessThanOrEqual(1)
      for (const key of ['mark', 'avatar', 'language', 'theme', 'collapse']) {
        expect(c[key], `${key} is rendered`).not.toBeNull()
        expect(Math.abs(c[key] - column), `${key} centre ${c[key]} vs column ${column}`).toBeLessThanOrEqual(1)
      }
    })

    test('tablet rail: the same column holds at 800px wide', async ({ page }) => {
      await page.setViewportSize({ width: 800, height: 900 })
      await page.goto('/app')
      await expect(page.locator('.sidebar-user-avatar, .sidebar-user-avatar-icon')).toBeVisible({ timeout: 15_000 })
      await page.waitForTimeout(600)
      const c = await centres(page)
      const column = c.nav[0]
      for (const key of ['avatar', 'language', 'theme']) {
        expect(Math.abs(c[key] - column), `${key} centre ${c[key]} vs column ${column}`).toBeLessThanOrEqual(1)
      }
    })

    test('expanded: avatar and language icon sit on the nav icon column', async ({ page }) => {
      await page.addInitScript(() => localStorage.setItem('localai_sidebar_collapsed', 'false'))
      await page.goto('/app')
      await expect(page.locator('.sidebar-user-avatar, .sidebar-user-avatar-icon')).toBeVisible({ timeout: 15_000 })
      await page.waitForTimeout(600)
      const c = await centres(page)
      const column = c.nav[0]
      for (const x of c.nav) expect(Math.abs(x - column)).toBeLessThanOrEqual(1)
      for (const key of ['avatar', 'language']) {
        expect(Math.abs(c[key] - column), `${key} centre ${c[key]} vs column ${column}`).toBeLessThanOrEqual(1)
      }
    })
  })
}
