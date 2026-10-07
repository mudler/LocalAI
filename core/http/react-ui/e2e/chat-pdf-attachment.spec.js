import { test, expect } from './coverage-fixtures.js'

// Single-page PDF with a real text layer, built byte by byte so the xref
// offsets are valid and the spec needs no binary fixture.
function buildPdf(text) {
  const stream = `BT /F1 18 Tf 20 100 Td (${text}) Tj ET`
  const objs = [
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>',
    `<< /Length ${stream.length} >>\nstream\n${stream}\nendstream`,
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',
  ]
  let out = '%PDF-1.4\n'
  const offsets = []
  objs.forEach((body, i) => {
    offsets.push(out.length)
    out += `${i + 1} 0 obj\n${body}\nendobj\n`
  })
  const xref = out.length
  out += `xref\n0 ${objs.length + 1}\n0000000000 65535 f \n`
  for (const o of offsets) out += `${String(o).padStart(10, '0')} 00000 n \n`
  out += `trailer\n<< /Size ${objs.length + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`
  return Buffer.from(out, 'latin1')
}

async function openChat(page) {
  await page.route('**/api/models/capabilities', (route) => {
    route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({ data: [{ id: 'test-model', capabilities: ['FLAG_CHAT'] }] }),
    })
  })
  await page.goto('/app/chat')
  await expect(page.getByRole('button', { name: /test-model/ })).toBeVisible({ timeout: 10_000 })
}

test.describe('Chat - PDF attachments', () => {
  test('sends the extracted text layer, not the raw PDF bytes', async ({ page }) => {
    let requestBody = ''
    await page.route('**/v1/chat/completions', (route) => {
      requestBody = route.request().postData() || ''
      route.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ error: { message: 'stop' } }) })
    })
    await openChat(page)

    await page.locator('input[type=file][accept*="pdf"]').setInputFiles({
      name: 'report.pdf',
      mimeType: 'application/pdf',
      buffer: buildPdf('Quarterly revenue grew 42 percent'),
    })
    await expect(page.locator('.home-file-tag', { hasText: 'report.pdf' })).toBeVisible()

    await page.getByTestId('chat-input').fill('Summarize')
    await page.getByTestId('chat-send').click()

    await expect.poll(() => requestBody).toContain('Quarterly revenue grew 42 percent')
    expect(requestBody).toContain('File: report.pdf')
    expect(requestBody).not.toContain('%PDF')
  })

  test('rejects a PDF that cannot be parsed instead of attaching garbage', async ({ page }) => {
    await openChat(page)

    await page.locator('input[type=file][accept*="pdf"]').setInputFiles({
      name: 'broken.pdf',
      mimeType: 'application/pdf',
      buffer: Buffer.from('%PDF-1.4 this is not a real document'),
    })

    await expect(page.getByText('Could not read text from broken.pdf')).toBeVisible({ timeout: 10_000 })
    await expect(page.locator('.home-file-tag', { hasText: 'broken.pdf' })).toHaveCount(0)
  })
})

test.describe('Home - PDF attachments', () => {
  test('attaches a PDF that has a text layer', async ({ page }) => {
    await page.goto('/app')
    await page.locator('input[type=file][accept*="pdf"]').setInputFiles({
      name: 'notes.pdf',
      mimeType: 'application/pdf',
      buffer: buildPdf('Meeting notes for Tuesday'),
    })
    await expect(page.locator('.home-file-tag', { hasText: 'notes.pdf' })).toBeVisible({ timeout: 10_000 })
  })

  test('rejects a PDF that cannot be parsed', async ({ page }) => {
    await page.goto('/app')
    await page.locator('input[type=file][accept*="pdf"]').setInputFiles({
      name: 'broken.pdf',
      mimeType: 'application/pdf',
      buffer: Buffer.from('%PDF-1.4 this is not a real document'),
    })
    await expect(page.getByText('Could not read text from broken.pdf')).toBeVisible({ timeout: 10_000 })
    await expect(page.locator('.home-file-tag')).toHaveCount(0)
  })
})
