import { test, expect } from './coverage-fixtures.js'
import { mockProjects, writes } from './projects-fixtures.js'

// The project form and the Tool defaults: pickers fed from what is installed,
// the "not available" state, the Tool defaults tab as an editor, the name limit, the
// delete confirm, the archived project and the project page on a phone.

const PHONE = { width: 390, height: 780 }

test.describe('Project form: tool defaults', () => {
  test('the pickers list what is installed and add the entry under its readable name', async ({ page }) => {
    const s = await mockProjects(page)
    await page.goto('/app/projects')
    await page.getByTestId('project-new').click()
    const sheet = page.getByTestId('project-sheet')
    await sheet.getByLabel('Name', { exact: true }).fill('Reading list')
    await sheet.getByTestId('project-defaults-section-connectors').locator('summary').click()
    const picks = sheet.getByTestId('project-pick-connectors')
    await expect(picks.getByTestId('project-pick-option')).toHaveCount(2)
    await picks.getByRole('button', { name: /Work mail/ }).click()
    const chip = sheet.getByTestId('project-defaults-connectors').getByTestId('project-default-entry')
    await expect(chip).toContainText('Work mail')
    await expect(chip).toHaveAttribute('data-state', 'ok')
    await sheet.getByTestId('project-defaults-section-addons').locator('summary').click()
    await sheet.getByTestId('project-pick-addons').getByRole('button', { name: /Mail/ }).click()
    await sheet.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByRole('heading', { name: 'Reading list', level: 1 })).toBeVisible()
    // The server matches IDs, so the connector and the add-on go by ID, not by the name shown.
    expect(writes(s, 'POST', '/api/chats/projects')[0].body.defaults).toEqual({ connectors: ['mail'], addons: ['mail-addon'] })
  })

  test('an entry that nothing installed matches is kept and marked not available', async ({ page }) => {
    const s = await mockProjects(page)
    await page.goto('/app/projects')
    await page.getByTestId('project-new').click()
    const sheet = page.getByTestId('project-sheet')
    await sheet.getByLabel('Name', { exact: true }).fill('Typos')
    await sheet.getByTestId('project-defaults-section-skills').locator('summary').click()
    await sheet.getByLabel('Add to Skills').fill('does-not-exist-skill')
    await expect(sheet.getByTestId('project-pick-free')).toContainText('not installed here')
    await sheet.getByLabel('Add to Skills').press('Enter')
    const chip = sheet.getByTestId('project-defaults-skills').getByTestId('project-default-entry')
    await expect(chip).toHaveAttribute('data-state', 'missing')
    await expect(chip).toContainText('Not available')
    await expect(chip).toHaveAttribute('title', /does nothing until it exists/)
    await expect(sheet.getByTestId('project-defaults-warn-skills')).toContainText('1 not available')
    await sheet.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByRole('heading', { name: 'Typos', level: 1 })).toBeVisible()
    expect(writes(s, 'POST', '/api/chats/projects')[0].body.defaults).toEqual({ skills: ['does-not-exist-skill'] })
  })

  test('a connector that is switched off says so, and a source that fails claims nothing', async ({ page }) => {
    const s = await mockProjects(page, { projects: [{ id: 'q1', name: 'Mixed', description: '', slug: 'mixed', status: 'active', defaults: { connectors: ['old'], skills: ['gone-skill'] }, updated: new Date().toISOString(), chat_count: 0 }] })
    s.failSources = ['skills']
    await page.goto('/app/projects/q1?tab=tools')
    await expect(page.getByTestId('project-defaults-connectors').getByTestId('project-default-entry')).toHaveAttribute('data-state', 'off')
    await expect(page.getByTestId('project-defaults-connectors')).toContainText('Off now')
    await expect(page.getByTestId('project-defaults-skills').getByTestId('project-default-entry')).toHaveAttribute('data-state', 'unknown')
    await expect(page.getByTestId('project-defaults-warn-skills')).toHaveCount(0)
  })

  test('the form says what empty means and that the defaults cap project agents', async ({ page }) => {
    await mockProjects(page)
    await page.goto('/app/projects/p1')
    await page.getByTestId('project-edit').click()
    const sheet = page.getByTestId('project-sheet')
    const group = sheet.getByRole('group', { name: 'Tool defaults' })
    await expect(group).toBeVisible()
    await expect(sheet.getByTestId('project-defaults-lead')).toContainText('Empty means no limit')
    await expect(sheet.getByTestId('project-defaults-lead')).toContainText('every agent of this project can use only those entries')
    for (const key of ['skills', 'addons', 'mcp_servers', 'connectors', 'collections']) {
      await expect(sheet.getByTestId(`project-defaults-section-${key}`).locator('.pj-note').first()).not.toBeEmpty()
    }
    // The hint is tied to the input that it explains.
    const hintId = await sheet.getByTestId('project-defaults-section-skills').locator('.pj-note').first().getAttribute('id')
    await expect(sheet.getByLabel('Add to Skills')).toHaveAttribute('aria-describedby', new RegExp(hintId))
    await expect(sheet.getByTestId('project-defaults-section-connectors')).toContainText('Messaging apps are separate')
    await expect(sheet.getByTestId('project-defaults-section-mcp_servers').locator('summary')).toContainText('MCP servers of a model')
  })

  test('the name stops at 120 characters, counts near the limit, and the description hint gives the 200-character fact', async ({ page }) => {
    await mockProjects(page)
    await page.goto('/app/projects')
    await page.getByTestId('project-new').click()
    const sheet = page.getByTestId('project-sheet')
    const name = sheet.getByLabel('Name', { exact: true })
    await expect(name).toHaveAttribute('maxlength', '120')
    await expect(sheet.getByTestId('project-name-counter')).toHaveCount(0)
    await name.fill('x'.repeat(130))
    await expect(name).toHaveValue('x'.repeat(120))
    await expect(sheet.getByTestId('project-name-counter')).toHaveText('120 of 120 characters')
    await expect(name).toHaveAttribute('aria-describedby', /name-count/)
    await expect(sheet.getByTestId('project-description-hint')).toContainText('The first 200 characters are given to chats as context.')
    await expect(sheet.getByLabel('Description')).toHaveAttribute('aria-describedby', /desc-hint/)
  })

  test('a refusal of the server wraps inside the sheet on a phone', async ({ page }) => {
    await mockProjects(page)
    await page.route('**/api/chats/projects', (r) => (r.request().method() === 'POST'
      ? r.fulfill({ status: 400, json: { error: { message: `name is not allowed: ${'averyveryveryverylongwordwithoutbreaks'.repeat(3)}` } } })
      : r.fallback()))
    await page.setViewportSize(PHONE)
    await page.goto('/app/projects')
    await page.getByTestId('project-new').click()
    const sheet = page.getByTestId('project-sheet')
    await sheet.getByLabel('Name', { exact: true }).fill('Anything')
    await sheet.getByRole('button', { name: 'Save' }).click()
    const err = sheet.getByTestId('project-sheet-error')
    await expect(err).toBeVisible()
    const fits = await err.evaluate(el => el.scrollWidth <= el.clientWidth + 1)
    expect(fits).toBe(true)
  })
})

test.describe('Project page: Tool defaults tab', () => {
  test('the tab edits the defaults with the same editor and one Save', async ({ page }) => {
    const s = await mockProjects(page)
    await page.goto('/app/projects/p1?tab=tools')
    await expect(page.getByRole('tab', { name: 'Tool defaults' })).toHaveAttribute('aria-selected', 'true')
    await expect(page.getByTestId('project-tools-save')).toBeDisabled()
    await page.getByTestId('project-defaults-section-skills').locator('summary').click()
    await page.getByTestId('project-pick-skills').getByRole('button', { name: /summarize/ }).click()
    await expect(page.getByTestId('project-tools-save')).toBeEnabled()
    await page.getByTestId('project-tools-discard').click()
    await expect(page.getByTestId('project-defaults-skills').getByTestId('project-default-entry')).toHaveCount(1)
    await page.getByTestId('project-pick-skills').getByRole('button', { name: /summarize/ }).click()
    await page.getByTestId('project-tools-save').click()
    await expect(page.getByTestId('project-tools-save')).toBeDisabled()
    const patch = writes(s, 'PATCH', '/projects/p1')[0]
    expect(patch.body).toEqual({ defaults: { skills: ['brand-voice', 'summarize'], mcp_servers: ['docs'], connectors: ['mail'], collections: ['papers', 'faq'] } })
    await page.getByTestId('project-tab-overview').click()
    await expect(page.getByTestId('project-stat-tools')).toContainText('6')
    await expect(page.getByTestId('project-stat-tools')).toContainText('Tool defaults')
  })

  test('an archived project shows its tool defaults read-only', async ({ page }) => {
    await mockProjects(page)
    await page.goto('/app/projects/p3?tab=tools')
    await expect(page.getByTestId('project-tools-archived')).toContainText('Restore it to change its tool defaults')
    await expect(page.getByTestId('project-tools-save')).toHaveCount(0)
    await expect(page.getByLabel('Add to Skills')).toHaveCount(0)
  })
})

test.describe('Project page: delete, archive and wording', () => {
  test('Delete asks first, says what depends on the project, and Cancel changes nothing', async ({ page }) => {
    const s = await mockProjects(page, { projects: [{ id: 'd1', name: 'Busy project', description: '', slug: 'busy-project', status: 'active', defaults: {}, updated: new Date().toISOString(), chat_count: 3, agent_count: 2 }] })
    s.triggers = [{ id: 't1' }, { id: 't2' }, { id: 't3' }]
    await page.goto('/app/projects/d1')
    await page.getByRole('button', { name: 'More actions for Busy project' }).click()
    await page.getByRole('menuitem', { name: 'Delete' }).click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('Delete “Busy project”?')
    const effects = dialog.getByTestId('project-delete-effects')
    await expect(effects).toContainText('Agents that stop working: 2')
    await expect(effects).toContainText('Triggers of those agents that stop: 3')
    await expect(effects).toContainText('Chats that keep their record but lose the project: 3')
    await expect(dialog).not.toContainText('/users/')
    await dialog.getByRole('button', { name: 'Cancel' }).click()
    await expect(dialog).toHaveCount(0)
    await expect(page).toHaveURL(/\/app\/projects\/d1$/)
    expect(writes(s, 'DELETE', '/projects/d1')).toHaveLength(0)
  })

  test('a project nothing depends on says so in the delete dialog', async ({ page }) => {
    await mockProjects(page)
    await page.goto('/app/projects/p2')
    await page.getByRole('button', { name: 'More actions for Home lab notes' }).click()
    await page.getByRole('menuitem', { name: 'Delete' }).click()
    await expect(page.getByTestId('project-delete-effects')).toContainText('No agents, triggers or chats depend on this project.')
  })

  test('an archived project has a Restore in its banner and Edit is off until then', async ({ page }) => {
    const s = await mockProjects(page)
    await page.goto('/app/projects/p3')
    const banner = page.getByTestId('project-archived')
    await expect(banner).toContainText('This project is archived.')
    await expect(page.getByTestId('project-edit')).toBeDisabled()
    await banner.getByRole('button', { name: 'Restore' }).click()
    await expect(page.getByTestId('project-archived')).toHaveCount(0)
    await expect(page.getByTestId('project-edit')).toBeEnabled()
    expect(writes(s, 'POST', '/projects/p3/restore')).toHaveLength(1)
  })

  test('the Overview does not count an index that holds only a heading', async ({ page }) => {
    await mockProjects(page, { memory: { index: '# Memory\n', files: [{ name: 'index.md', size: 9, modified: new Date().toISOString() }] } })
    await page.goto('/app/projects/p1')
    await expect(page.getByTestId('project-stat-memory')).toContainText('0')
    await expect(page.getByTestId('project-overview')).toContainText('Nothing in the memory yet.')
  })

  test('the empty tabs use one empty-state style', async ({ page }) => {
    await mockProjects(page)
    await page.route('**/api/chats/projects/p2/agents', r => r.fulfill({ json: { agents: [] } }))
    await page.route('**/api/chats/projects/p2/connections', r => r.fulfill({ json: { connections: [], kinds: [] } }))
    await page.goto('/app/projects/p2?tab=agents')
    await expect(page.getByTestId('project-agents-empty').locator('.ui-empty')).toHaveClass(/ui-empty--no-results/)
    await expect(page.getByTestId('project-agents-empty').locator('.ui-empty__icon')).toHaveCount(1)
    await page.getByTestId('project-tab-chats').click()
    await expect(page.getByTestId('project-chats').locator('.ui-empty')).toHaveClass(/ui-empty--no-results/)
    await expect(page.getByTestId('project-chats').locator('.ui-empty__icon')).toHaveCount(1)
  })
})

test.describe('Project pages on a phone', () => {
  test('the tabs scroll, with a hint that more tabs are off screen', async ({ page }) => {
    await mockProjects(page)
    await page.setViewportSize(PHONE)
    await page.goto('/app/projects/p1')
    const wrap = page.getByTestId('project-tabs-wrap')
    await expect(wrap).toHaveAttribute('data-end', 'true')
    await expect(page.getByTestId('project-tabs-next')).toBeVisible()
    await expect(page.getByTestId('project-tab-tools')).not.toBeInViewport({ ratio: 0.5 })
    await page.getByTestId('project-tabs-next').click()
    await expect(wrap).toHaveAttribute('data-start', 'true')
    // The arrow scrolls by a part of the strip; it ends when nothing is left behind the edge.
    for (let i = 0; i < 4 && (await wrap.getAttribute('data-end')); i++) {
      await page.getByTestId('project-tabs-next').click()
      await page.waitForTimeout(400)
    }
    await expect(wrap).not.toHaveAttribute('data-end', 'true')
    await expect(page.getByTestId('project-tab-tools')).toBeInViewport({ ratio: 0.9 })
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  })

  test('a broken row wraps its error at words and the list sorts ignoring case', async ({ page }) => {
    await mockProjects(page, {
      projects: [
        { id: 'b1', name: 'zebra notes', description: '', slug: 'zebra-notes', status: 'active', defaults: {}, updated: new Date().toISOString(), chat_count: 0 },
        { id: 'b2', name: 'Apple plan', description: '', slug: 'apple-plan', status: 'active', defaults: {}, updated: new Date().toISOString(), chat_count: 0 },
        { id: 'b3', name: 'banana', description: '', slug: 'banana', status: 'broken', error: 'project.json is missing', defaults: {}, updated: new Date().toISOString(), chat_count: 0 },
      ],
    })
    await page.setViewportSize(PHONE)
    await page.goto('/app/projects')
    await expect(page.getByTestId('project-row')).toHaveText([/Apple plan/, /banana/, /zebra notes/])
    const err = page.getByTestId('project-row-error')
    const lineCount = await err.evaluate(el => Math.round(el.getBoundingClientRect().height / parseFloat(getComputedStyle(el).lineHeight || '20')))
    expect(lineCount).toBeLessThanOrEqual(2)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  })

  test('the Edit sheet has no horizontal overflow and the folder hint wraps', async ({ page }) => {
    await mockProjects(page)
    await page.setViewportSize(PHONE)
    await page.goto('/app/projects/p1')
    await page.getByTestId('project-edit').click()
    const sheet = page.getByTestId('project-sheet')
    await expect(sheet).toContainText('Folder: Projects/client-site-redesign')
    const fits = await sheet.evaluate(el => el.scrollWidth <= el.clientWidth + 1)
    expect(fits).toBe(true)
  })
})
