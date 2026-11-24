import { test, expect } from './coverage-fixtures.js'
import { mockHome, mockUser } from './home-fixtures.js'
import { mockRecommended, recommendedAnswer, REC_MODELS, CPU_REC_MODELS } from './recommended-fixtures.js'

// First run: nothing is installed, and the page recommends one model for this
// machine. The server decides the model, the window it installs with and the
// fit (GET /api/models/recommended); these specs stub that answer and check what
// a person sees and what an install sends, for an administrator and for a
// member who cannot install.

async function firstRun(page, answer, { member = false } = {}) {
  await mockHome(page, { loaded: [], models: [], chatModels: [] })
  const seen = await mockRecommended(page, answer)
  if (member) await mockUser(page)
  await page.goto('/app')
  return seen
}

const rec = (page) => page.getByTestId('starter-models')
const primary = (page) => page.getByTestId('starter-primary')

test.describe('First run recommendation, as an administrator', () => {
  test('shows one primary choice with its size and the window it will get', async ({ page }) => {
    await firstRun(page, recommendedAnswer())
    await expect(rec(page)).toBeVisible({ timeout: 15_000 })
    await expect(rec(page)).toContainText('Recommended for your machine')
    await expect(page.getByTestId('starter-tier')).toContainText('Small GPU')
    await expect(primary(page)).toHaveCount(1)
    await expect(primary(page)).toContainText('Qwen3.8 4B')
    await expect(primary(page)).toContainText('2.8 GB download')
    await expect(primary(page)).toContainText('32K context')
    await expect(primary(page)).toContainText('The most capable model that fits a small card')
    await expect(primary(page).getByRole('button', { name: /install/i })).toBeVisible()
    // The alternatives wait behind the disclosure.
    await expect(page.getByTestId('starter-alt')).toHaveCount(0)
  })

  test('"More models" opens the two alternatives', async ({ page }) => {
    await firstRun(page, recommendedAnswer())
    const more = page.getByTestId('starter-more-toggle')
    await expect(more).toHaveText(/More models/, { timeout: 15_000 })
    await expect(more).toHaveAttribute('aria-expanded', 'false')
    await more.click()
    await expect(more).toHaveAttribute('aria-expanded', 'true')
    await expect(more).toHaveText(/Fewer models/)
    await expect(page.getByTestId('starter-alt')).toHaveCount(2)
    await expect(page.getByTestId('starter-alt').nth(0)).toContainText('Gemma 4 E4B')
    await expect(page.getByTestId('starter-alt').nth(0)).toContainText('24K context')
    await expect(page.getByTestId('starter-alt').nth(1)).toContainText('Qwen3.8 2B')
    await more.click()
    await expect(page.getByTestId('starter-alt')).toHaveCount(0)
  })

  test('install names the entry, pins it and sends the fitted window', async ({ page }) => {
    const installs = []
    await page.route('**/api/models/install/*', (route) => {
      const url = new URL(route.request().url())
      installs.push({ id: decodeURIComponent(url.pathname.split('/').pop()), query: Object.fromEntries(url.searchParams) })
      return route.fulfill({ json: { jobID: 'job-1', message: 'Installation started' } })
    })
    await firstRun(page, recommendedAnswer())
    await primary(page).getByRole('button', { name: /install/i }).click()
    await expect.poll(() => installs.length).toBe(1)
    expect(installs[0]).toEqual({ id: 'qwen3.8-4b-q4', query: { variant: 'qwen3.8-4b-q4', context_size: '32768' } })
  })

  test('an alternative installs with its own window', async ({ page }) => {
    const installs = []
    await page.route('**/api/models/install/*', (route) => {
      const url = new URL(route.request().url())
      installs.push({ id: decodeURIComponent(url.pathname.split('/').pop()), query: Object.fromEntries(url.searchParams) })
      return route.fulfill({ json: { jobID: 'job-1' } })
    })
    await firstRun(page, recommendedAnswer())
    await page.getByTestId('starter-more-toggle').click()
    await page.getByTestId('starter-alt').nth(0).getByRole('button', { name: /install/i }).click()
    await expect.poll(() => installs.length).toBe(1)
    expect(installs[0]).toEqual({ id: 'gemma-4-e4b-it-qat-q4_0', query: { variant: 'gemma-4-e4b-it-qat-q4_0', context_size: '24576' } })
  })

  test('an install the server refuses is reported, and the button comes back', async ({ page }) => {
    await page.route('**/api/models/install/*', (route) => route.fulfill({ status: 403, json: { error: 'admin only' } }))
    await firstRun(page, recommendedAnswer())
    const button = primary(page).getByRole('button', { name: /install/i })
    await button.click()
    await expect(page.getByText(/Install failed/)).toBeVisible()
    await expect(primary(page).getByRole('button', { name: /install/i })).toBeEnabled()
  })

  test('with no hardware detected it says so and shows the CPU tier', async ({ page }) => {
    await firstRun(page, recommendedAnswer({
      tier: 'cpu', hardware: { detected: false, kind: 'unknown', memory_bytes: 0, scope: 'local' },
      models: CPU_REC_MODELS.map(m => ({ ...m, fit: 'unknown', fit_reasons: ['hardware_unknown'], context_size: 0, memory_bytes: 0 })),
    }))
    await expect(page.getByTestId('starter-undetected')).toContainText('could not detect your hardware', { timeout: 15_000 })
    await expect(page.getByTestId('starter-tier')).toContainText('CPU only')
    await expect(primary(page)).toContainText('Gemma 4 E2B')
    // No window is promised when nothing was checked, and the notice is not repeated on the row.
    await expect(primary(page)).not.toContainText('context')
    await expect(primary(page)).not.toContainText('could not be read')
  })

  test('when nothing fits it says so and still names the smallest model', async ({ page }) => {
    await firstRun(page, recommendedAnswer({
      tier: 'cpu', nothing_fits: true,
      models: [{ ...CPU_REC_MODELS[1], fit: 'does_not_fit', fit_reasons: ['exceeds_memory'], context_size: 4096 }],
    }))
    await expect(page.getByTestId('starter-nothing-fits')).toContainText('None of the recommended models fits', { timeout: 15_000 })
    await expect(primary(page)).toContainText('Qwen3.8 4B')
    await expect(primary(page)).toContainText('Does not fit')
    await expect(primary(page)).toContainText('more memory than this machine has')
    await expect(page.getByTestId('starter-more-toggle')).toHaveCount(0)
  })

  test('a tight fit is flagged on the model', async ({ page }) => {
    await firstRun(page, recommendedAnswer({
      models: [{ ...REC_MODELS[0], fit: 'tight', fit_reasons: ['little_headroom', 'window_reduced'], context_size: 16384 }],
    }))
    await expect(primary(page)).toContainText('Tight fit', { timeout: 15_000 })
    await expect(primary(page)).toContainText('16K context')
    await expect(primary(page)).toContainText('leaves little memory free')
  })

  test('a cluster says whose hardware it was sized for', async ({ page }) => {
    await firstRun(page, recommendedAnswer({ hardware: { detected: true, kind: 'gpu', memory_bytes: 80 * 1024 ** 3, scope: 'cluster', node_name: 'node-1' } }))
    await expect(rec(page)).toContainText('best machine in your cluster', { timeout: 15_000 })
  })

  test('draws nothing when the server cannot answer', async ({ page }) => {
    await mockHome(page, { loaded: [], models: [], chatModels: [] })
    await page.route('**/api/models/recommended', (route) => route.fulfill({ status: 500, json: { error: 'boom' } }))
    await page.goto('/app')
    await expect(page.getByTestId('home-first-run')).toBeVisible({ timeout: 15_000 })
    await expect(rec(page)).toHaveCount(0)
  })
})

test.describe('First run recommendation, as a member who cannot install', () => {
  test('sees the same names read-only, with a note to ask an administrator', async ({ page }) => {
    await firstRun(page, recommendedAnswer(), { member: true })
    await expect(rec(page)).toBeVisible({ timeout: 15_000 })
    await expect(primary(page)).toContainText('Qwen3.8 4B')
    await expect(primary(page)).toContainText('32K context')
    await expect(rec(page).getByRole('button', { name: /^install/i })).toHaveCount(0)
    await expect(page.getByTestId('starter-ask-admin')).toContainText('Only an administrator can install models')
    // The alternatives are readable too, and still have no button.
    await page.getByTestId('starter-more-toggle').click()
    await expect(page.getByTestId('starter-alt')).toHaveCount(2)
    await expect(rec(page).getByRole('button', { name: /^install/i })).toHaveCount(0)
  })

  test('never sends an install', async ({ page }) => {
    const installs = []
    await page.route('**/api/models/install/*', (route) => { installs.push(route.request().url()); return route.fulfill({ status: 403, json: {} }) })
    await firstRun(page, recommendedAnswer(), { member: true })
    await expect(rec(page)).toBeVisible({ timeout: 15_000 })
    await page.waitForTimeout(300)
    expect(installs).toEqual([])
  })
})

test.describe('First run recommendation on a phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  for (const member of [false, true]) {
    test(`fits the screen without sideways scrolling (${member ? 'member' : 'administrator'})`, async ({ page }) => {
      await firstRun(page, recommendedAnswer(), { member })
      await expect(rec(page)).toBeVisible({ timeout: 15_000 })
      await page.getByTestId('starter-more-toggle').click()
      await expect(page.getByTestId('starter-alt')).toHaveCount(2)
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
      expect(overflow).toBeLessThanOrEqual(0)
      const box = await rec(page).boundingBox()
      expect(box.x + box.width).toBeLessThanOrEqual(390)
    })
  }
})
