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
      // The Explore inspector is always on the page, whatever the gallery holds.
      await page.goto('/app/models')
      const pane = page.locator('.ledger__pane').first()
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

    test('the selected row sits on a surface step and shows a check, with no edge rail', async ({ page }) => {
      await page.goto('/app/models?view=installed')
      const row = page.locator('tr[data-row]').first()
      await expect(row).toBeVisible({ timeout: 15_000 })
      const cell = row.locator('td').nth(1)
      const before = await cell.evaluate((el) => getComputedStyle(el).backgroundColor)
      await row.click()
      await expect(row).toHaveAttribute('data-selected', 'true')
      const after = await cell.evaluate((el) => {
        const cs = getComputedStyle(el)
        return { bg: cs.backgroundColor, shadow: cs.boxShadow, border: cs.borderLeftWidth }
      })
      // A stronger surface, not a stripe on the edge.
      expect(after.bg).not.toBe(before)
      expect(after.shadow).not.toContain('inset')
      expect(after.border).toBe('0px')
      // The check mark is the second signal.
      const mark = await row.locator('.ledger-mark').evaluate((el) => getComputedStyle(el).backgroundColor)
      expect(mark).not.toBe('rgba(0, 0, 0, 0)')
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
      await expect(btn).toHaveAttribute('data-empty', 'true')
      await page.getByTestId('chat-input').fill('hello')
      await expect(btn).not.toHaveAttribute('data-empty', 'true')
      // The test server lists no chat model, so the button stays disabled. Compare
      // the two looks directly: the active look must differ from the quiet one
      // whatever the disabled state.
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
