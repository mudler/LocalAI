import { test, expect } from './coverage-fixtures.js'

// ListPicker, driven on the catalog (/app/ui-catalog): the keyboard, the
// filter box, groups, the none row, the empty state and a multiple choice.
// Set UI_SHOTS_DIR to also save screenshots of the open menus.

const CATALOG = '/app/ui-catalog'
const SHOTS = process.env.UI_SHOTS_DIR

async function openCatalog(page, { theme = 'light', width = 1440, height = 900 } = {}) {
  await page.setViewportSize({ width, height })
  await page.addInitScript((t) => localStorage.setItem('localai-theme', t), theme)
  await page.goto(CATALOG)
  await expect(page.getByTestId('cat-section-picker')).toBeVisible({ timeout: 15_000 })
  // The stored theme is kept per account; set the shown one directly so a
  // second call in the same test switches it.
  await page.evaluate((t) => document.documentElement.setAttribute('data-theme', t), theme)
  await page.getByTestId('cat-section-picker').scrollIntoViewIfNeeded()
}

const backend = (page) => page.getByTestId('cat-pick-backend')
const backendMenu = (page) => page.getByTestId('cat-pick-backend-menu')

test.describe('ListPicker', () => {
  test('the keyboard opens, moves, picks and closes with focus back on the trigger', async ({ page }) => {
    await openCatalog(page)
    const trigger = backend(page)
    await expect(trigger).toHaveAttribute('aria-haspopup', 'listbox')
    await expect(trigger).toHaveAttribute('aria-expanded', 'false')
    await expect(trigger).toContainText('Automatic')

    await trigger.focus()
    await page.keyboard.press('ArrowDown')
    await expect(trigger).toHaveAttribute('aria-expanded', 'true')
    const menu = backendMenu(page)
    // Eight backends: the filter box shows and takes the focus.
    const filter = menu.getByRole('combobox', { name: 'Filter backends' })
    await expect(filter).toBeFocused()
    const options = menu.getByRole('option')
    await expect(options).toHaveCount(9)
    // The none row is chosen and active when the menu opens.
    await expect(options.first()).toHaveAttribute('aria-selected', 'true')
    await expect(options.first()).toHaveAttribute('data-active', 'true')
    // Groups have their heading.
    await expect(menu.locator('.home-menu__head')).toHaveText(['Text', 'Audio', 'Image'])

    await page.keyboard.press('ArrowDown')
    await expect(options.nth(1)).toHaveAttribute('data-active', 'true')
    await expect(filter).toHaveAttribute('aria-activedescendant', await options.nth(1).getAttribute('id'))
    await page.keyboard.press('End')
    await expect(options.last()).toHaveAttribute('data-active', 'true')
    await page.keyboard.press('ArrowDown')
    await expect(options.first()).toHaveAttribute('data-active', 'true')
    await page.keyboard.press('ArrowUp')
    await expect(options.last()).toHaveAttribute('data-active', 'true')
    await page.keyboard.press('Home')
    await expect(options.first()).toHaveAttribute('data-active', 'true')

    await page.keyboard.press('ArrowDown')
    await page.keyboard.press('Enter')
    await expect(menu).toHaveCount(0)
    await expect(trigger).toBeFocused()
    await expect(trigger).toContainText('llama-cpp')

    // Reopened, the chosen row is the active one; Escape closes and returns focus.
    await page.keyboard.press('ArrowDown')
    await expect(menu.getByRole('option', { name: /llama-cpp/ })).toHaveAttribute('data-active', 'true')
    await expect(menu.getByRole('option', { name: /llama-cpp/ })).toHaveAttribute('aria-selected', 'true')
    await page.keyboard.press('Escape')
    await expect(menu).toHaveCount(0)
    await expect(trigger).toBeFocused()
  })

  test('the filter narrows the rows, keeps the none row and says when nothing matches', async ({ page }) => {
    await openCatalog(page)
    await backend(page).click()
    const menu = backendMenu(page)
    const filter = menu.getByRole('combobox', { name: 'Filter backends' })
    await filter.fill('speech')
    // The none row plus the speech backends (matched by their description).
    await expect(menu.getByRole('option')).toHaveText([/Automatic/, /whisper/, /piper/, /kokoro/])
    await expect(menu.getByRole('option').first()).toHaveAttribute('data-active', 'true')
    // Home and End move the caret in the box while it has text, not the row.
    await page.keyboard.press('End')
    await expect(menu.getByRole('option').first()).toHaveAttribute('data-active', 'true')
    await page.keyboard.press('ArrowDown')
    await page.keyboard.press('ArrowDown')
    await page.keyboard.press('Enter')
    await expect(backend(page)).toContainText('piper')

    await backend(page).click()
    await menu.getByRole('combobox').fill('zzz')
    await expect(menu.getByText('No backend matches.')).toBeVisible()
    await expect(menu.getByRole('option')).toHaveText([/Automatic/])
    // The none row empties the value.
    await page.keyboard.press('Enter')
    await expect(backend(page)).toContainText('Automatic')
  })

  test('a disabled row cannot be chosen and a click outside closes the menu', async ({ page }) => {
    await openCatalog(page)
    await backend(page).click()
    const menu = backendMenu(page)
    const mlx = menu.getByRole('option', { name: /mlx/ })
    await expect(mlx).toHaveAttribute('aria-disabled', 'true')
    // aria-disabled: Playwright waits for an enabled element, so force the click.
    await mlx.click({ force: true })
    await expect(menu).toBeVisible()
    await page.mouse.click(5, 5)
    await expect(menu).toHaveCount(0)
    await expect(backend(page)).toContainText('Automatic')
  })

  test('a multiple choice stays open, checks the rows and counts them on the trigger', async ({ page }) => {
    await openCatalog(page)
    const trigger = page.getByTestId('cat-pick-tools')
    await expect(trigger).toHaveAccessibleName('Tools')
    await expect(page.getByTestId('cat-pick-tools-count')).toHaveText('1')
    await trigger.click()
    const menu = page.getByTestId('cat-pick-tools-menu')
    // Three rows: no filter box, the focus is on the list.
    await expect(menu.getByRole('combobox')).toHaveCount(0)
    await expect(menu.getByRole('listbox')).toBeFocused()
    await expect(menu.getByRole('listbox')).toHaveAttribute('aria-multiselectable', 'true')
    await expect(menu.getByRole('option', { name: 'Search' })).toHaveAttribute('aria-selected', 'true')
    await expect(menu.getByRole('option', { name: 'Search' }).locator('.home-menu__check')).toHaveCount(1)

    await page.keyboard.press('ArrowDown')
    await page.keyboard.press('Enter')
    await expect(menu).toBeVisible()
    await expect(menu.getByRole('option', { name: 'Files' })).toHaveAttribute('aria-selected', 'true')
    await expect(page.getByTestId('cat-pick-tools-count')).toHaveText('2')
    await expect(trigger).toContainText('Search, Files')

    await menu.getByRole('option', { name: 'Search' }).click()
    await expect(page.getByTestId('cat-pick-tools-count')).toHaveText('1')
    await expect(menu.getByRole('option', { name: 'Search' })).toHaveAttribute('aria-selected', 'false')
    if (SHOTS) await page.waitForTimeout(400)
    if (SHOTS) await page.getByTestId('cat-section-picker').screenshot({ path: `${SHOTS}/list-picker-multi.png` })
    await page.keyboard.press('Escape')
    await expect(menu).toHaveCount(0)
    await expect(trigger).toBeFocused()
  })

  test('an empty list says so, and a disabled trigger does not open', async ({ page }) => {
    await openCatalog(page)
    await page.getByTestId('cat-pick-empty').click()
    await expect(page.getByTestId('cat-pick-empty-menu').getByText('No backends are installed.')).toBeVisible()
    await expect(page.getByTestId('cat-pick-empty-menu').getByRole('option')).toHaveCount(0)
    await page.keyboard.press('Escape')

    await expect(page.getByTestId('cat-pick-disabled')).toBeDisabled()
  })

  test('the field trigger has the control height and the compact select the sm height', async ({ page }) => {
    await openCatalog(page)
    const field = await backend(page).boundingBox()
    const chip = await page.getByTestId('cat-pick-tools').boundingBox()
    const sm = await page.getByTestId('cat-select-sm').boundingBox()
    const ctl = await page.evaluate(() => {
      const s = getComputedStyle(document.documentElement)
      return { md: parseFloat(s.getPropertyValue('--ctl-h')), sm: parseFloat(s.getPropertyValue('--ctl-h-sm')) }
    })
    expect(Math.round(field.height)).toBe(ctl.md)
    expect(Math.round(chip.height)).toBe(ctl.sm)
    expect(Math.round(sm.height)).toBe(ctl.sm)
  })

  test('screenshots of the open menus in both themes', async ({ page }) => {
    test.skip(!SHOTS, 'set UI_SHOTS_DIR to save screenshots')
    for (const theme of ['light', 'dark']) {
      for (const [w, h, tag] of [[1440, 900, 'desktop'], [390, 844, 'phone']]) {
        await openCatalog(page, { theme, width: w, height: h })
        await backend(page).click()
        await backendMenu(page).getByRole('combobox').fill('')
        await page.keyboard.press('ArrowDown')
        await page.keyboard.press('ArrowDown')
        // Let the menu finish its rise before the picture.
        await page.waitForTimeout(400)
        await page.screenshot({ path: `${SHOTS}/list-picker-${theme}-${tag}.png`, fullPage: false })
        await page.keyboard.press('Escape')
      }
    }
  })
})
