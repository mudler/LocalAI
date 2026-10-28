import { test, expect } from './coverage-fixtures.js'
import { installAgents } from './agents-fixtures.js'

// The skills page and the skill editor, with the skills API stubbed.

test.describe('Skills', () => {
  test('lists the skills with their layer, who uses them, and opens one', async ({ page }) => {
    await installAgents(page)
    await page.goto('/app/skills')
    await expect(page.getByTestId('skills-page')).toBeVisible()
    await expect(page.getByTestId('skill-row-citations')).toContainText('Personal')
    await expect(page.getByTestId('skill-used-citations')).toHaveText('Used by research-assistant')
    await expect(page.getByTestId('skill-row-pdf')).toContainText('Built-in')
    await expect(page.getByTestId('skill-row-house-style')).toContainText('Shared')
    await expect(page.getByTestId('skill-row-broken')).toContainText('Workspace')
    await expect(page.getByTestId('skill-used-broken')).toContainText('not a valid skill')

    await page.getByTestId('skill-row-citations').click()
    await expect(page.getByTestId('skill-title')).toHaveText('citations')
    await expect(page.getByTestId('skill-preview')).toContainText('Cite every claim.')
    await page.getByRole('tab', { name: 'Instructions' }).click()
    await expect(page.getByTestId('skill-content')).toContainText('List them at the end.')
  })

  test('only your own skills can be edited or deleted', async ({ page }) => {
    await installAgents(page)
    await page.goto('/app/skills')
    await page.getByTestId('skill-row-pdf').click()
    await expect(page.getByTestId('skill-title')).toHaveText('pdf')
    await expect(page.getByTestId('skill-layer')).toHaveText('Built-in')
    await expect(page.getByRole('button', { name: 'Edit' })).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'Delete' })).toHaveCount(0)
    await page.getByTestId('skill-row-citations').click()
    await expect(page.getByRole('button', { name: 'Edit' })).toBeVisible()
  })

  test('an invalid SKILL.md says why it was skipped', async ({ page }) => {
    await installAgents(page)
    await page.goto('/app/skills')
    await page.getByTestId('skill-row-broken').click()
    await expect(page.getByTestId('skill-invalid')).toContainText('the description is empty')
  })

  test('filters by search, by layer and by use', async ({ page }) => {
    await installAgents(page)
    await page.goto('/app/skills')
    await page.getByTestId('skills-search').fill('pdf')
    await expect(page.getByTestId('skill-row-pdf')).toBeVisible()
    await expect(page.getByTestId('skill-row-citations')).toHaveCount(0)
    await page.getByTestId('skills-search').fill('')
    await page.getByTestId('skills-layer-shared').click()
    await expect(page.getByTestId('skill-row-house-style')).toBeVisible()
    await expect(page.getByTestId('skill-row-pdf')).toHaveCount(0)
    await page.getByTestId('skills-layer-all').click()
    await page.getByTestId('skills-filter-unused').click()
    await expect(page.getByTestId('skill-row-citations')).toHaveCount(0)
    await expect(page.getByTestId('skill-row-pdf')).toBeVisible()
    await page.getByTestId('skills-search').fill('zzz')
    await expect(page.getByTestId('skills-none')).toBeVisible()
  })

  test('adds a skill to an agent and takes it away, with an undo', async ({ page }) => {
    const s = await installAgents(page)
    await page.goto('/app/skills')
    await page.getByTestId('skill-row-pdf').click()
    await page.getByTestId('add-to-button').click()
    await page.getByTestId('add-to-code-reviewer').getByRole('button', { name: 'Add to code-reviewer' }).click()
    await expect.poll(() => s.calls.find(c => c.method === 'PUT' && c.url === '/api/chats/agents/a2')?.body.skills).toEqual(['pdf'])
    await expect(page.getByTestId('used-by-code-reviewer')).toBeVisible()
    await page.getByRole('button', { name: 'Remove from code-reviewer' }).click()
    await expect(page.getByTestId('library-undo-toast')).toBeVisible()
    await expect.poll(() => s.calls.filter(c => c.method === 'PUT' && c.url === '/api/chats/agents/a2').at(-1)?.body.skills).toEqual([])
  })

  test('deletes a skill after asking', async ({ page }) => {
    const s = await installAgents(page)
    await page.goto('/app/skills')
    await page.getByTestId('skill-row-citations').click()
    await page.getByRole('button', { name: 'Delete' }).click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('Delete citations?')
    await dialog.getByRole('button', { name: 'Delete' }).click()
    await expect(page.getByTestId('skill-row-citations')).toHaveCount(0)
    expect(s.calls.some(c => c.method === 'DELETE' && c.url === '/api/chats/skills/personal/citations')).toBe(true)
  })

  test('says why the page is unavailable when the files are off', async ({ page }) => {
    await installAgents(page)
    await page.route(/\/api\/chats\/skills(\?.*)?$/, route => route.fulfill({ status: 409, json: { error: { message: 'Files are off on this server.', type: 'files_off', code: 409 } } }))
    await page.goto('/app/skills')
    await expect(page.getByTestId('files-off')).toContainText('Files are off on this server.')
  })

  test('the empty page walks through writing the first skill', async ({ page }) => {
    await installAgents(page, { skills: [] })
    await page.goto('/app/skills')
    await expect(page.getByTestId('skills-empty')).toBeVisible()
    await page.getByRole('button', { name: 'Create skill' }).click()
    await expect(page).toHaveURL(/\/app\/skills\/new$/)
  })

  test('the old address of the skills page redirects', async ({ page }) => {
    await installAgents(page)
    await page.goto('/app/chats/skills')
    await expect(page).toHaveURL(/\/app\/skills$/)
  })
})

test.describe('Skill editor', () => {
  test('writes a new skill in your own layer', async ({ page }) => {
    const s = await installAgents(page)
    await page.goto('/app/skills/new')
    await page.getByLabel(/^Name/).fill('release-notes')
    await page.getByLabel(/^Description/).fill('Write release notes from a changelog')
    await page.locator('.skilledit-sidebar-item', { hasText: 'Content' }).click()
    await page.getByLabel('Skill content (markdown)').fill('Group by feature.')
    await page.getByRole('button', { name: 'Create skill' }).click()
    await expect(page).toHaveURL(/\/app\/skills$/)
    expect(s.calls.find(c => c.method === 'POST').body).toEqual({
      layer: 'personal', name: 'release-notes', description: 'Write release notes from a changelog', body: 'Group by feature.',
    })
  })

  test('edits a skill: the name and the layer stay, the description and the body change', async ({ page }) => {
    const s = await installAgents(page)
    await page.goto('/app/skills/edit/personal/citations')
    await expect(page.getByLabel(/^Name/)).toBeDisabled()
    await expect(page.getByLabel(/^Name/)).toHaveValue('citations')
    await expect(page.getByTestId('skill-edit-layer')).toHaveText('Personal')
    await page.getByLabel(/^Description/).fill('Cite with numbers.')
    await page.locator('.skilledit-sidebar-item', { hasText: 'Content' }).click()
    await expect(page.getByLabel('Skill content (markdown)')).toHaveValue(/Number the sources/)
    await page.getByRole('button', { name: 'Save changes' }).click()
    await expect(page).toHaveURL(/\/app\/skills$/)
    expect(s.calls.find(c => c.method === 'PUT' && c.url.includes('/skills/')).body).toMatchObject({ description: 'Cite with numbers.' })
  })

  test('a skill you cannot write opens read-only', async ({ page }) => {
    await installAgents(page)
    await page.goto('/app/skills/edit/builtin/pdf')
    await expect(page.getByTestId('skill-readonly')).toBeVisible()
    await expect(page.getByRole('button', { name: 'Save changes' })).toHaveCount(0)
  })
})
