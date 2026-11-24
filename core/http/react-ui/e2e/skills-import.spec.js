import { test, expect } from './coverage-fixtures.js'
import { installAgents } from './agents-fixtures.js'

// Importing skills from a zip file and exporting one, with the skills API
// stubbed. The real checks of the archive are Go specs on the server.

const ZIP = { name: 'pdf-tools.zip', mimeType: 'application/zip', buffer: Buffer.from('PK-not-read-by-the-stub') }

async function stubImport(page, { status = 201, body } = {}) {
  const calls = { imports: 0, exports: [] }
  await page.route('**/api/chats/skills/import', route => {
    calls.imports += 1
    return route.fulfill({ status, json: body })
  })
  await page.route(/\/api\/chats\/skills\/personal\/[^/?]+\/export$/, route => {
    calls.exports.push(new URL(route.request().url()).pathname)
    return route.fulfill({ status: 200, headers: { 'content-disposition': 'attachment; filename="citations.zip"' }, contentType: 'application/zip', body: Buffer.from('PK-zip') })
  })
  return calls
}

test.describe('Skills import and export', () => {
  test('imports a zip file, says scripts are stored not run, and lists the new skill', async ({ page }) => {
    await installAgents(page)
    const calls = await stubImport(page, {
      body: { rev: 7, skills: [{ name: 'pdf-tools', description: 'Work with PDFs.', files: 3, bytes: 900, scripts: 1 }] },
    })
    // After the import the list holds the skill, marked new.
    await page.route(/\/api\/chats\/skills(\?.*)?$/, route => route.fulfill({
      json: { skills: [{ name: 'pdf-tools', description: 'Work with PDFs.', layer: 'personal', path: '/skills/pdf-tools/SKILL.md', enabled: true, editable: true, new: true }], notes: [] },
    }))
    await page.goto('/app/skills')
    await page.getByTestId('skills-import-input').setInputFiles(ZIP)
    await expect(page.getByTestId('skills-import-done')).toContainText('pdf-tools')
    await expect(page.getByTestId('skills-import-done')).toContainText('never run by the import')
    expect(calls.imports).toBe(1)
    await expect(page.getByTestId('skill-new-pdf-tools')).toHaveText('New')
    await page.getByTestId('skill-row-pdf-tools').click()
    await expect(page.getByTestId('skill-new')).toContainText('not used yet')
  })

  test('shows the reason when the server refuses the archive, and lists nothing new', async ({ page }) => {
    await installAgents(page)
    const calls = await stubImport(page, {
      status: 409,
      body: { error: { message: 'a skill of that name exists already: "citations"', type: 'conflict', code: 409 } },
    })
    await page.goto('/app/skills')
    await page.getByTestId('skills-import-input').setInputFiles(ZIP)
    await expect(page.getByTestId('skills-import-error')).toContainText('exists already')
    expect(calls.imports).toBe(1)
    await expect(page.getByTestId('skills-import-done')).toHaveCount(0)
    await page.getByRole('button', { name: 'Dismiss' }).click()
    await expect(page.getByTestId('skills-import-error')).toHaveCount(0)
  })

  test('exports a personal skill as a zip file, and offers it for no other layer', async ({ page }) => {
    await installAgents(page)
    const calls = await stubImport(page, { body: { rev: 1, skills: [] } })
    await page.goto('/app/skills')
    await page.getByTestId('skill-row-citations').click()
    const download = page.waitForEvent('download')
    await page.getByTestId('skill-export').click()
    expect((await download).suggestedFilename()).toBe('citations.zip')
    expect(calls.exports).toEqual(['/api/chats/skills/personal/citations/export'])
    await page.getByTestId('skill-row-pdf').click()
    await expect(page.getByTestId('skill-title')).toHaveText('pdf')
    await expect(page.getByTestId('skill-export')).toHaveCount(0)
  })
})
