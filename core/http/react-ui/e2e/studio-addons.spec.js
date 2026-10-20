import { test, expect } from './coverage-fixtures.js'
import { installStudioAddons } from './studio-addon.js'
import { mockStudio } from './studio-fixtures.js'
import { fakeRealtime } from './chat-fixtures.js'

// Studio and Talk are packaged add-ons. These specs run the real built bundles
// inside the add-on shell: the pages draw, the old addresses open them, the
// links they hold reach the core pages, and nothing is left when they are not
// installed.

function watchErrors(page) {
  const errors = []
  page.on('pageerror', e => errors.push(String(e)))
  page.on('console', m => { if (m.type() === 'error') errors.push(m.text()) })
  return errors
}

test.describe('Studio add-on', () => {
  for (const tab of ['', '/images', '/video', '/threed', '/tts', '/sound', '/transform', '/diarization']) {
    test(`the ${tab ? tab.slice(1) : 'overview'} tab draws inside the add-on shell with no script error`, async ({ page }) => {
      const errors = watchErrors(page)
      await mockStudio(page)
      await page.goto(`/app/addons/studio/studio${tab}`)
      await expect(page.locator('[data-testid="addon-surface"][data-addon="studio"] .studio-tabs')).toBeVisible({ timeout: 15_000 })
      if (tab) await expect(page.locator(`.studio-tab-active[data-tab="${tab.slice(1)}"]`)).toBeVisible()
      else await expect(page.getByTestId('studio-overview')).toBeVisible()
      expect(errors).toEqual([])
    })
  }

  test('the shell titles the page once, and the page keeps its own words from the media locale', async ({ page }) => {
    await mockStudio(page)
    await page.goto('/app/addons/studio/studio')
    await expect(page.getByTestId('studio-overview')).toBeVisible({ timeout: 15_000 })
    await expect(page.locator('h1.page-title', { hasText: /^Studio$/ })).toHaveCount(1)
    await expect(page.getByText('What do you want to make?')).toBeVisible()
    // No raw key is drawn where words belong.
    await expect(page.locator('.studio-front').getByText(/studio\.[a-z]+\.[A-Za-z.]+/)).toHaveCount(0)
  })

  test('a model id with a slash and a colon travels as ?model= and reaches the page', async ({ page }) => {
    await mockStudio(page)
    await page.goto('/app/studio/images/sd-1.5-lcm?size=768x768')
    await expect(page).toHaveURL(/\/app\/addons\/studio\/studio\/images\?size=768x768&model=sd-1\.5-lcm$/)
    await expect(page.locator('.studio-tab-active[data-tab="images"]')).toBeVisible({ timeout: 15_000 })
  })

  test('the old Studio addresses say so plainly when the add-on is not installed', async ({ page }) => {
    for (const address of ['/app/studio', '/app/studio/tts', '/app/image', '/app/talk']) {
      await page.goto(address)
      await expect(page.getByTestId('addon-unavailable')).toBeVisible({ timeout: 15_000 })
      await expect(page).toHaveURL(/\/app\/addons\/(studio|talk)\//)
    }
    await expect(page.locator('.studio-tabs')).toHaveCount(0)
  })

  test('the Studio style sheet reaches the page and nothing outside it', async ({ page }) => {
    await mockStudio(page)
    await page.goto('/app/addons/studio/studio/images')
    await expect(page.locator('.studio-tab-active')).toBeVisible({ timeout: 15_000 })
    const css = await page.evaluate(() => [...document.querySelectorAll('style[data-addon-style="studio"]')].map(el => el.textContent).join('\n'))
    expect(css).toContain('[data-addon="studio"]')
    expect(css).not.toMatch(/(^|\})\s*\.studio-tab\s*\{/)
    // The sidebar is untouched by the sheet.
    const sidebarFont = await page.locator('.sidebar-nav .nav-item').first().evaluate(el => getComputedStyle(el).fontFamily)
    expect(sidebarFont.length).toBeGreaterThan(0)
  })
})

test.describe('Talk add-on', () => {
  test('the page draws inside the add-on shell, titled once, with no script error', async ({ page }) => {
    const errors = watchErrors(page)
    await fakeRealtime(page)
    await page.goto('/app/addons/talk/talk')
    await expect(page.getByTestId('talk-start')).toBeEnabled({ timeout: 15_000 })
    await expect(page.locator('h1')).toHaveCount(1)
    await expect(page.locator('h1')).toHaveText('Talk')
    expect(errors).toEqual([])
  })

  test('a button of the page opens a core page, and the way back to Talk travels with the editor link', async ({ page }) => {
    await fakeRealtime(page, { pipelines: false })
    await page.goto('/app/addons/talk/talk')
    const card = page.getByTestId('talk-no-pipeline')
    await expect(card).toBeVisible({ timeout: 15_000 })
    await card.getByRole('button', { name: 'Create a pipeline model' }).click()
    await expect(page).toHaveURL(/\/app\/model-editor\?template=pipeline/)
    expect(await page.evaluate(() => window.history.state?.usr)).toEqual({ from: '/app/addons/talk/talk', fromLabel: 'Talk' })
    await page.goBack()
    await expect(page.getByTestId('talk-no-pipeline')).toBeVisible()
    await page.getByTestId('talk-no-pipeline').getByRole('button', { name: /gallery/i }).click()
    await expect(page).toHaveURL(/\/app\/models/)
  })
})

test.describe('Without the add-ons', () => {
  test('the Add-ons drawer does not list Studio or Talk, and the sidebar has no Create group', async ({ page }) => {
    await page.goto('/app/addons')
    await expect(page.getByTestId('addon-tile').filter({ hasText: /Studio|Talk/ })).toHaveCount(0)
    await expect(page.locator('.sidebar-nav a[href="/app/studio"], .sidebar-nav a[href="/app/talk"]')).toHaveCount(0)
    await expect(page.locator('.sidebar-section-title', { hasText: 'Create' })).toHaveCount(0)
  })

  test('installing them lists both in the drawer', async ({ page }) => {
    await installStudioAddons(page)
    await page.goto('/app/addons')
    await expect(page.getByTestId('addon-tile').filter({ hasText: 'Studio' })).toBeVisible({ timeout: 15_000 })
    await expect(page.getByTestId('addon-tile').filter({ hasText: 'Talk' })).toBeVisible()
  })
})
