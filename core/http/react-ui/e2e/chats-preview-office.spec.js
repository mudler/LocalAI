import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
import { test, expect } from './coverage-fixtures.js'

// Word, Excel and PowerPoint files in the preview of the Files page, against
// the real test server. The pane draws them from their own bytes (a document
// in an isolated frame, a workbook as a table, a presentation on a canvas);
// when that fails or the file is too large it shows the server's text view.
// The fixtures in fixtures/office were written by python-docx, openpyxl and
// python-pptx.

test.describe.configure({ mode: 'serial', timeout: 90_000 })

const dir = path.join(path.dirname(fileURLToPath(import.meta.url)), 'fixtures', 'office')
const stamp = Date.now().toString(36)
const folder = `/OfficePreview-${stamp}`

test.beforeAll(async ({ request }) => {
  for (const name of ['report.docx', 'budget.xlsx', 'deck.pptx', 'long.xlsx']) {
    const res = await request.put(`/api/chats/files/content?path=${encodeURIComponent(`${folder}/${name}`)}`, {
      data: readFileSync(path.join(dir, name)), headers: { 'Content-Type': 'application/octet-stream' },
    })
    expect(res.ok(), `upload ${name}`).toBe(true)
  }
})

async function openFile(page, name) {
  await page.goto(`/app/files?path=${encodeURIComponent(folder)}`)
  await page.getByRole('button', { name, exact: true }).or(page.getByText(name, { exact: true })).first().click()
  return page.getByTestId('file-preview')
}

test('a Word document is drawn with its formatting in a frame that runs no script', async ({ page }) => {
  const outside = []
  page.on('request', (r) => { if (!r.url().startsWith('http://127.0.0.1:8089') && !r.url().startsWith('data:') && !r.url().startsWith('blob:')) outside.push(r.url()) })
  const pane = await openFile(page, 'report.docx')
  const frame = pane.getByTestId('preview-docx')
  await expect(frame).toBeVisible()
  // Nothing in the frame can run: no allow-scripts, and a policy that loads nothing.
  expect(await frame.getAttribute('sandbox')).toBe('allow-same-origin')
  const inner = page.frameLocator('[data-testid="preview-docx"]')
  await expect(inner.getByText('Quarterly report')).toBeVisible()
  await expect(inner.locator('strong, b, span[style*="bold"]').filter({ hasText: '12% over the quarter' }).first()).toBeVisible()
  await expect(inner.getByRole('cell', { name: 'North' })).toBeVisible()
  await expect(inner.locator('img').first()).toBeVisible()
  expect(await frame.evaluate(el => el.contentDocument.querySelector('meta[http-equiv="Content-Security-Policy"]')?.content)).toContain("default-src 'none'")
  expect(await frame.evaluate(el => el.contentDocument.querySelectorAll('script').length)).toBe(0)
  // The text view is not shown, and Download is in the bar.
  await expect(pane.getByTestId('preview-document')).toHaveCount(0)
  await expect(pane.getByTestId('preview-office-download')).toHaveAttribute('download', 'report.docx')
  // Full size, and back to fit.
  const zoom = () => frame.evaluate(el => el.contentDocument.querySelector('.docx-wrapper').style.zoom)
  const fit = await zoom()
  expect(Number(fit)).toBeLessThan(1)
  await pane.getByTestId('preview-docx-zoom').click()
  await expect.poll(zoom).toBe('1')
  await pane.getByTestId('preview-docx-zoom').click()
  await expect.poll(zoom).toBe(fit)
  expect(outside).toEqual([])
})

test('a workbook shows its sheets with formatted numbers and dates, and the formula of a result that was never stored', async ({ page }) => {
  const pane = await openFile(page, 'budget.xlsx')
  const table = pane.getByTestId('preview-table')
  await expect(table).toBeVisible()
  await expect(pane.getByTestId('preview-sheet-tab')).toHaveText(['Budget', 'Wide', 'Notes'])
  await expect(table.getByRole('cell', { name: '$12,000.00' })).toBeVisible()
  await expect(table.getByRole('cell', { name: '2026-01-15' })).toBeVisible()
  // The file was written by a script, so its formulas have no stored result.
  await expect(table.getByRole('cell', { name: '=C2+D2' })).toBeVisible()
  await expect(table.locator('td.pv-grid__num').first()).toBeVisible()
  await pane.getByRole('tab', { name: 'Wide' }).click()
  await expect(table.getByRole('columnheader', { name: 'Metric 40' })).toBeVisible()
  await expect(pane.getByTestId('preview-table-more')).toHaveCount(0)
})

test('a long sheet shows its first rows and says how many there are (the header row counts)', async ({ page }) => {
  const pane = await openFile(page, 'long.xlsx')
  await expect(pane.getByTestId('preview-table-more')).toContainText('Showing 1000 of 1201 rows')
  await expect(pane.getByTestId('preview-table').locator('tbody tr')).toHaveCount(999)
})

test('a presentation is drawn on a canvas, one slide at a time', async ({ page }) => {
  const pane = await openFile(page, 'deck.pptx')
  const count = pane.getByTestId('preview-slide-count')
  await expect(count).toHaveText('Slide 1 of 3')
  const prev = pane.getByTestId('preview-slide-prev')
  const next = pane.getByTestId('preview-slide-next')
  await expect(prev).toBeDisabled()
  const painted = () => pane.locator('canvas').evaluate((c) => {
    const d = c.getContext('2d').getImageData(0, 0, c.width, c.height).data
    let n = 0
    for (let i = 0; i < d.length; i += 4) if (d[i + 3] && (d[i] < 200 || d[i + 1] < 200 || d[i + 2] < 200)) n++
    return n
  })
  await expect.poll(painted).toBeGreaterThan(100)
  await next.click()
  await expect(count).toHaveText('Slide 2 of 3')
  await pane.getByTestId('preview-deck').locator('[role="group"]').focus()
  await page.keyboard.press('ArrowRight')
  await expect(count).toHaveText('Slide 3 of 3')
  await expect(next).toBeDisabled()
  await expect.poll(painted).toBeGreaterThan(100)
  await page.keyboard.press('ArrowLeft')
  await expect(count).toHaveText('Slide 2 of 3')
})

test('when the drawing fails, the text view takes over and says so', async ({ page }) => {
  // The bytes of the file are not a document; the server still reads the real one.
  await page.route(/\/api\/chats\/files\/content\?.*report\.docx/, route => route.fulfill({ status: 200, contentType: 'application/octet-stream', body: 'this is not a zip archive' }))
  const pane = await openFile(page, 'report.docx')
  await expect(pane.getByTestId('preview-office-note')).toContainText('could not be drawn')
  await expect(pane.getByTestId('preview-document')).toContainText('Revenue grew')
  await expect(pane.getByTestId('preview-docx')).toHaveCount(0)
})

test('a file over the size cap is not read at all, and the text view says why', async ({ page }) => {
  let reads = 0
  await page.route(/\/api\/chats\/files\/content\?.*budget\.xlsx/, (route) => { reads++; return route.continue() })
  // The listing reports the workbook as 30 MB.
  await page.route(/\/api\/chats\/files\?/, async (route) => {
    const res = await route.fetch()
    const body = await res.json()
    for (const e of body.entries || []) if (e.name === 'budget.xlsx') e.size = 30 * 1024 * 1024
    await route.fulfill({ response: res, json: body })
  })
  const pane = await openFile(page, 'budget.xlsx')
  await expect(pane.getByTestId('preview-office-note')).toContainText('over 25.0 MB')
  await expect(pane.getByTestId('preview-table')).toBeVisible()
  expect(reads).toBe(0)
})
