import { test, expect } from './coverage-fixtures.js'

// Layers must read as layers in both themes: a card against the canvas, the
// current row against the list, and the send button against its disabled self.
const rgb = (css) => css.match(/[\d.]+/g).slice(0, 3).map(Number)
const distance = (a, b) => Math.hypot(...a.map((v, i) => v - b[i]))

for (const theme of ['light', 'dark']) {
  test.describe(`surfaces (${theme})`, () => {
    test.beforeEach(async ({ page }) => {
      await page.addInitScript((t) => localStorage.setItem('localai-theme', t), theme)
    })

    test('split panes are cards with their own edge and a shadow', async ({ page }) => {
      await page.goto('/app/models?view=installed')
      const pane = page.locator('.split-view__pane').first()
      await expect(pane).toBeVisible({ timeout: 15_000 })
      const style = await pane.evaluate((el) => {
        const cs = getComputedStyle(el)
        return {
          bg: cs.backgroundColor,
          border: cs.borderTopColor,
          shadow: cs.boxShadow,
          radius: cs.borderTopLeftRadius,
          canvas: getComputedStyle(document.body).backgroundColor,
        }
      })
      expect(style.radius).toBe('20px')
      expect(style.shadow).not.toBe('none')
      // The edge differs from the card, and the card differs from the canvas.
      expect(distance(rgb(style.border), rgb(style.bg))).toBeGreaterThan(8)
      expect(distance(rgb(style.bg), rgb(style.canvas))).toBeGreaterThan(8)
    })

    test('the selected row has an accent wash and an accent edge', async ({ page }) => {
      await page.goto('/app/models?view=installed')
      const row = page.locator('.entity-rail__item').first()
      await expect(row).toBeVisible({ timeout: 15_000 })
      const before = await row.evaluate((el) => getComputedStyle(el).backgroundColor)
      await row.click()
      const on = page.locator('.entity-rail__item--on').first()
      await expect(on).toBeVisible()
      const after = await on.evaluate((el) => {
        const cs = getComputedStyle(el)
        return { bg: cs.backgroundColor, shadow: cs.boxShadow }
      })
      expect(after.bg).not.toBe(before)
      expect(after.shadow).toContain('inset')
    })

    test('the home send button is quiet when empty and active with text', async ({ page }) => {
      await page.goto('/app')
      const textarea = page.locator('.home-textarea')
      await expect(textarea).toBeVisible({ timeout: 15_000 })
      const btn = page.locator('.home-send-btn')
      await expect(btn).toHaveAttribute('data-empty', 'true')
      await textarea.fill('hello')
      await expect(btn).not.toHaveAttribute('data-empty', 'true')
      // The test server lists no chat model, so the button stays disabled.
      // Compare the two looks directly: the active look must differ from the
      // quiet one whatever the disabled state.
      const colours = await btn.evaluate((el) => {
        el.disabled = false
        el.style.transition = 'none'
        const active = getComputedStyle(el).backgroundColor
        el.setAttribute('data-empty', 'true')
        const quiet = getComputedStyle(el).backgroundColor
        return { active, quiet }
      })
      expect(distance(rgb(colours.active), rgb(colours.quiet))).toBeGreaterThan(40)
    })

    test('the chat send button is quiet when empty and active with text', async ({ page }) => {
      await page.goto('/app/chat')
      const btn = page.locator('#chat-submit-btn')
      await expect(btn).toBeVisible({ timeout: 15_000 })
      await expect(btn).toBeDisabled()
      const empty = await btn.evaluate((el) => getComputedStyle(el).backgroundColor)
      await page.locator('.chat-input').fill('hello')
      await expect(btn).toBeEnabled()
      // The background transitions, so poll until it settles.
      await expect.poll(async () => {
        const filled = await btn.evaluate((el) => getComputedStyle(el).backgroundColor)
        return distance(rgb(filled), rgb(empty))
      }).toBeGreaterThan(40)
    })

    test('a popover sits on the float surface, not on the card', async ({ page }) => {
      await page.goto('/app')
      await expect(page.locator('.home-greeting')).toBeVisible({ timeout: 15_000 })
      await page.locator('.language-switcher-trigger').click()
      const menu = page.locator('.language-switcher-menu')
      await expect(menu).toBeVisible()
      const style = await menu.evaluate((el) => {
        const cs = getComputedStyle(el)
        return { bg: cs.backgroundColor, shadow: cs.boxShadow, edge: cs.borderTopColor }
      })
      expect(style.shadow).not.toBe('none')
      expect(distance(rgb(style.edge), rgb(style.bg))).toBeGreaterThan(8)
    })
  })
}
