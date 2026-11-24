import { test, expect } from './coverage-fixtures.js'
import { mockHome, setTheme } from './home-fixtures.js'
import { mockRecommended, recommendedAnswer } from './recommended-fixtures.js'

// The product name is drawn with a coloured "AI" and the first-run guide shows
// the llama in its circle, but only for the bundled brand. An operator who
// sets a name or a logo keeps exactly what they set: plain text, their image,
// no mascot.

const CUSTOM_LOGO = '/branding/asset/logo'
const SVG = '<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><rect width="64" height="64" fill="red"/></svg>'

async function brand(page, data) {
  await page.route('**/api/branding', route => route.fulfill({ json: data }))
  await page.route('**/branding/asset/**', route => route.fulfill({ contentType: 'image/svg+xml', body: SVG }))
}
const CUSTOM = { instance_name: 'Acme Brain', logo_url: CUSTOM_LOGO, logo_horizontal_url: '/branding/asset/logo_horizontal', favicon_url: '/branding/asset/favicon' }

async function firstRun(page) {
  await mockHome(page, { loaded: [], models: [], chatModels: [] })
  await mockRecommended(page, recommendedAnswer())
  await page.goto('/app')
  await expect(page.getByTestId('home-first-run')).toBeVisible({ timeout: 15_000 })
}

const mascot = page => page.getByTestId('home-first-run').locator('img[data-pose="circle"]')

test.describe('Branding: the bundled brand', () => {
  for (const theme of ['light', 'dark']) {
    test(`the sidebar name has a coloured AI in ${theme}`, async ({ page }) => {
      await setTheme(page, theme)
      await brand(page, {})
      await mockHome(page)
      await page.goto('/app')
      const wordmark = page.locator('.sidebar-wordmark')
      await expect(wordmark).toHaveText('LocalAI')
      const ai = wordmark.getByTestId('brand-ai')
      await expect(ai).toHaveText('AI')
      const [aiColour, textColour] = await Promise.all([
        ai.evaluate(el => getComputedStyle(el).color),
        wordmark.evaluate(el => getComputedStyle(el).color),
      ])
      expect(aiColour).not.toBe(textColour)
      expect(aiColour).toBe(theme === 'light' ? 'rgb(0, 91, 105)' : 'rgb(255, 201, 60)')
    })
  }

  test('the first-run guide shows the llama in step 1, decorative, once', async ({ page }) => {
    await brand(page, {})
    await firstRun(page)
    await expect(mascot(page)).toHaveCount(1)
    await expect(mascot(page)).toHaveAttribute('alt', '')
    await expect(mascot(page)).toHaveAttribute('aria-hidden', 'true')
    const step1 = page.locator('.home-step--lead')
    await expect(step1.locator('img[data-pose="circle"]')).toHaveCount(1)
    const box = await mascot(page).boundingBox()
    expect(box.width).toBeCloseTo(104, 0)
  })

  test('the llama stays clear of the step text at 390 px', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await brand(page, {})
    await firstRun(page)
    const img = await mascot(page).boundingBox()
    const title = await page.locator('.home-step--lead > .home-step__main > b').evaluate(el => {
      const r = document.createRange(); r.selectNodeContents(el); const b = r.getBoundingClientRect()
      return { right: b.right }
    })
    expect(img.width).toBeLessThanOrEqual(72)
    expect(title.right).toBeLessThanOrEqual(img.x)
  })

  test('the login panel has a coloured AI', async ({ page }) => {
    await brand(page, {})
    await page.route('**/api/auth/status', route => route.fulfill({ json: { authEnabled: true, staticApiKeyRequired: false, user: null, hasUsers: true, providers: [], registrationMode: 'open', permissions: {} } }))
    await page.goto('/login')
    await expect(page.locator('.lg-brand__name')).toHaveText('LocalAI')
    await expect(page.locator('.lg-brand__name').getByTestId('brand-ai')).toHaveText('AI')
  })
})

test.describe('Branding: an operator-set name and logo', () => {
  test('the sidebar shows the plain custom name and the custom logo', async ({ page }) => {
    await brand(page, CUSTOM)
    await mockHome(page)
    await page.goto('/app')
    await expect(page.locator('.sidebar-logo-img').first()).toHaveAttribute('src', /branding\/asset\/logo_horizontal$/)
    await expect(page.locator('.sidebar-logo-icon-img')).toHaveAttribute('src', /branding\/asset\/logo$/)
    await expect(page.locator('.sidebar-logo-icon')).toHaveAttribute('title', 'Acme Brain')
    await expect(page.getByTestId('brand-ai')).toHaveCount(0)
    await expect(page.locator('.sidebar')).not.toContainText('LocalAI')
  })

  test('a custom name with the bundled logo is plain text', async ({ page }) => {
    await brand(page, { instance_name: 'Acme Brain' })
    await mockHome(page)
    await page.goto('/app')
    await expect(page.locator('.sidebar-wordmark')).toHaveText('Acme Brain')
    await expect(page.getByTestId('brand-ai')).toHaveCount(0)
  })

  test('the favicon link follows the custom favicon', async ({ page }) => {
    await brand(page, CUSTOM)
    await mockHome(page)
    await page.goto('/app')
    await expect(page.locator("link[rel='icon']")).toHaveAttribute('href', /branding\/asset\/favicon/)
    await expect(page).toHaveTitle('Acme Brain')
  })

  test('the first-run guide has no llama for a custom name', async ({ page }) => {
    await brand(page, { instance_name: 'Acme Brain' })
    await firstRun(page)
    await expect(mascot(page)).toHaveCount(0)
    await expect(page.locator('.home-step--lead')).toHaveCount(0)
  })

  test('the first-run guide has no llama for a custom logo with the bundled name', async ({ page }) => {
    await brand(page, { logo_url: CUSTOM_LOGO })
    await firstRun(page)
    await expect(mascot(page)).toHaveCount(0)
  })

  test('the login panel shows the custom name and logo, without the span', async ({ page }) => {
    await brand(page, CUSTOM)
    await page.route('**/api/auth/status', route => route.fulfill({ json: { authEnabled: true, staticApiKeyRequired: false, user: null, hasUsers: true, providers: [], registrationMode: 'open', permissions: {} } }))
    await page.goto('/login')
    await expect(page.locator('.lg-brand__name')).toHaveText('Acme Brain')
    await expect(page.getByTestId('brand-ai')).toHaveCount(0)
    await expect(page.locator('.lg-brand__logo')).toHaveAttribute('src', /branding\/asset\/logo$/)
  })
})
