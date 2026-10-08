import { test, expect } from './coverage-fixtures.js'
import { LOG_LINES, mockOperate } from './operate-fixtures.js'

// Runtime, Logs: what a backend process printed, read live over the log
// stream. The viewer filters by stream and by text, follows the end, shows or
// hides times, exports, and clears (with an undo window).

async function openLogs(page, { lines = LOG_LINES, path = '/app/backend-logs/qwen3-8b-instruct' } = {}) {
  await mockOperate(page)
  await page.routeWebSocket('**/ws/backend-logs/**', ws => {
    ws.send(JSON.stringify({ type: 'initial', lines }))
  })
  await page.goto(path)
}

const lineRows = page => page.locator('[data-log-line]')

test.describe('Backend Logs', () => {
  test('model detail page shows title', async ({ page }) => {
    await openLogs(page)
    await expect(page.locator('.page-title')).toContainText('qwen3-8b-instruct')
  })

  test('the way out is the Backends list, which is where a process comes from', async ({ page }) => {
    await openLogs(page)
    await expect(page.locator('.lg-back')).toHaveAttribute('href', '/app/backends?view=installed')
  })

  test('reads the lines the process printed, with the error line marked', async ({ page }) => {
    await openLogs(page)
    await expect(lineRows(page)).toHaveCount(5)
    await expect(lineRows(page).nth(3)).toContainText('out of memory')
    await expect(lineRows(page).nth(3)).toHaveAttribute('data-stream', 'stderr')
    await expect(lineRows(page).nth(3)).toContainText('err')
  })

  test('stream buttons narrow the lines and say which is on', async ({ page }) => {
    await openLogs(page)
    const all = page.getByRole('button', { name: 'All', exact: true })
    const stderr = page.getByRole('button', { name: 'stderr', exact: true })
    await expect(all).toHaveAttribute('aria-pressed', 'true')
    await stderr.click()
    await expect(stderr).toHaveAttribute('aria-pressed', 'true')
    await expect(all).toHaveAttribute('aria-pressed', 'false')
    await expect(lineRows(page)).toHaveCount(1)
    await page.getByRole('button', { name: 'stdout', exact: true }).click()
    await expect(lineRows(page)).toHaveCount(4)
  })

  test('the filter keeps the lines that contain the text', async ({ page }) => {
    await openLogs(page)
    await page.getByRole('textbox', { name: 'Filter lines' }).fill('backend')
    await expect(lineRows(page)).toHaveCount(2)
    await page.getByRole('textbox', { name: 'Filter lines' }).fill('nothing like this')
    await expect(page.getByText('No line contains that text.')).toBeVisible()
  })

  test('Times hides the time and the stream label for easier copying', async ({ page }) => {
    await openLogs(page)
    const times = page.getByRole('switch', { name: 'Times' })
    await expect(times).toHaveAttribute('aria-checked', 'true')
    await expect(page.locator('.lg-line__time').first()).toBeVisible()
    await times.click()
    await expect(times).toHaveAttribute('aria-checked', 'false')
    await expect(page.locator('.lg-line__time')).toHaveCount(0)
    await expect(page.locator('.lg-line__stream')).toHaveCount(0)
  })

  test('Follow is on by default and can be turned off', async ({ page }) => {
    await openLogs(page)
    const follow = page.getByRole('switch', { name: 'Follow' })
    await expect(follow).toHaveAttribute('aria-checked', 'true')
    await follow.click()
    await expect(follow).toHaveAttribute('aria-checked', 'false')
  })

  test('Export offers the visible lines as a file', async ({ page }) => {
    await openLogs(page)
    const download = page.waitForEvent('download')
    await page.getByRole('button', { name: 'Export' }).click()
    expect((await download).suggestedFilename()).toMatch(/^backend-logs-qwen3-8b-instruct-\d{4}-\d{2}-\d{2}\.json$/)
  })

  test('Clear hides the lines at once and wipes them when the window ends', async ({ page }) => {
    await openLogs(page)
    const calls = []
    await page.route('**/api/backend-logs/qwen3-8b-instruct/clear', route => { calls.push('clear'); return route.fulfill({ json: {} }) })
    await page.getByRole('button', { name: 'Clear' }).click()
    await expect(lineRows(page)).toHaveCount(0)
    await expect(page.getByTestId('logs-undo-toast')).toContainText('Clearing the log')
    expect(calls).toEqual([])
    await expect.poll(() => calls, { timeout: 15_000 }).toEqual(['clear'])
  })

  test('Undo brings the lines back and never calls the server', async ({ page }) => {
    await openLogs(page)
    const calls = []
    await page.route('**/api/backend-logs/qwen3-8b-instruct/clear', route => { calls.push('clear'); return route.fulfill({ json: {} }) })
    await page.getByRole('button', { name: 'Clear' }).click()
    await page.getByTestId('logs-undo-toast').getByRole('button', { name: 'Undo' }).click()
    await expect(lineRows(page)).toHaveCount(5)
    await page.waitForTimeout(7_000)
    expect(calls).toEqual([])
  })

  test('says so when the process has printed nothing', async ({ page }) => {
    await openLogs(page, { lines: [] })
    await expect(page.getByText('No log lines')).toBeVisible()
    await expect(page.getByText('Log output will appear here as the backend process runs.')).toBeVisible()
  })

  test('a picker switches to another process', async ({ page }) => {
    await openLogs(page)
    await page.getByRole('combobox', { name: 'Process' }).selectOption('whisper-large-v3')
    await expect(page).toHaveURL(/\/app\/backend-logs\/whisper-large-v3$/)
  })

  test('the Logs tab lists the processes that have output', async ({ page }) => {
    await mockOperate(page)
    await page.goto('/app/backends')
    await page.locator('.hub-subnav').getByRole('link', { name: 'Logs' }).click()
    await expect(page).toHaveURL(/\/app\/backend-logs$/)
    const list = page.getByTestId('logs-processes')
    await expect(list.getByRole('link', { name: /qwen3-8b-instruct/ })).toHaveAttribute('href', '/app/backend-logs/qwen3-8b-instruct')
    await expect(page.locator('.dk-hubtabs [data-hub-tab="runtime"]')).toHaveAttribute('aria-current', 'page')
  })

  test('says so when no process has printed anything', async ({ page }) => {
    await mockOperate(page)
    await page.route('**/api/backend-logs', route => route.fulfill({ json: [] }))
    await page.goto('/app/backend-logs')
    await expect(page.getByText('No process has printed anything')).toBeVisible()
  })

  test('fits a phone', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 800 })
    await openLogs(page)
    await expect(lineRows(page)).toHaveCount(5)
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(1)
  })
})
