import { test, expect } from './coverage-fixtures.js'
import { mockLibrary, SKILLS } from './library-fixtures.js'

// Skills page (src/pages/Skills.jsx): the library of instructions, who uses
// each one, and how to add one to an agent.

test.describe('Skills page', () => {
  test('renders the skills list with create affordances', async ({ page }) => {
    await page.goto('/app/skills')
    await expect(page).toHaveURL(/\/app\/skills$/)
    await expect(page.getByRole('heading', { name: 'Skills', exact: true })).toBeVisible()
    await expect(page.getByRole('button', { name: 'New skill' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Git repositories' })).toBeVisible()
  })

  test('New skill navigates to the skill editor', async ({ page }) => {
    await page.goto('/app/skills')
    await page.getByRole('button', { name: 'New skill' }).click()
    await expect(page).toHaveURL(/\/app\/skills\/new$/)
  })
})

test.describe('Skills library', () => {
  test('each skill says who uses it, from the agents configs, and never claims Chat', async ({ page }) => {
    await mockLibrary(page)
    await page.goto('/app/skills')
    await expect(page.getByTestId('skill-used-summarise-pdf')).toHaveText('Used by research-assistant +1')
    await expect(page.getByTestId('skill-used-query-database')).toHaveText('Used by research-assistant')
    await expect(page.getByTestId('skill-used-triage-issue')).toHaveText('Used by support-desk')
    await expect(page.getByTestId('skill-used-translate-document')).toHaveText('Not used yet')
    await expect(page.getByTestId('skill-used-weekly-digest')).toHaveText('Not used yet')
    for (const skill of SKILLS) {
      await expect(page.getByTestId(`skill-used-${skill.name}`)).not.toContainText(/chat/i)
    }
  })

  test('an agent with skills on and no selection uses every skill', async ({ page }) => {
    await mockLibrary(page, {
      agents: { everything: { name: 'everything', model: 'm', enable_skills: true, selected_skills: [] } },
    })
    await page.goto('/app/skills?skill=weekly-digest')
    await expect(page.getByTestId('skill-used-weekly-digest')).toHaveText('Used by everything')
    const chip = page.getByTestId('used-by-everything')
    await expect(chip).toContainText('all skills')
  })

  test('the Used and Not used yet filters split the list', async ({ page }) => {
    await mockLibrary(page)
    await page.goto('/app/skills')
    await page.getByTestId('skills-filter-used').click()
    await expect(page.locator('[data-testid^="skill-row-"]')).toHaveCount(3)
    await expect(page.getByTestId('skill-row-translate-document')).toHaveCount(0)
    await page.getByTestId('skills-filter-unused').click()
    await expect(page.locator('[data-testid^="skill-row-"]')).toHaveCount(2)
    await expect(page.getByTestId('skill-row-translate-document')).toBeVisible()
    await expect(page.getByTestId('skill-row-weekly-digest')).toBeVisible()
    await page.getByTestId('skills-filter-all').click()
    await expect(page.locator('[data-testid^="skill-row-"]')).toHaveCount(5)
  })

  test('search narrows the list to the matching skills', async ({ page }) => {
    await mockLibrary(page)
    await page.goto('/app/skills')
    await page.getByTestId('skills-search').fill('triage')
    await expect(page.locator('[data-testid^="skill-row-"]')).toHaveCount(1)
    await expect(page.getByTestId('skill-row-triage-issue')).toBeVisible()
    await page.getByTestId('skills-search').fill('zzzz')
    await expect(page.getByTestId('skills-none')).toBeVisible()
  })

  test('the pane shows instructions, an estimate, files and who uses the skill', async ({ page }) => {
    await mockLibrary(page)
    await page.goto('/app/skills')
    await page.getByTestId('skill-row-summarise-pdf').click()
    await expect(page.getByTestId('skill-title')).toHaveText('summarise-pdf')
    await expect(page.getByTestId('used-by-research-assistant').getByRole('link')).toHaveAttribute('href', '/app/agents/research-assistant')
    await expect(page.getByTestId('used-by-handbook')).toBeVisible()
    // 456 characters, 114 tokens by the stated rule
    await expect(page.getByTestId('skill-tokens')).toContainText('About 114 tokens')
    await page.getByRole('tab', { name: 'Instructions' }).click()
    await expect(page.getByTestId('skill-content')).toContainText('Read the file the user attached')
    await page.getByRole('tab', { name: 'Files' }).click()
    await expect(page.getByText('scripts/extract.py')).toBeVisible()
    await expect(page.getByText('references/style.md')).toBeVisible()
  })

  test('Add to... lists agents with the cost, and adding saves the agent', async ({ page }) => {
    const { seen } = await mockLibrary(page)
    await page.goto('/app/skills?skill=translate-document')
    await expect(page.getByTestId('skill-not-used')).toBeVisible()
    await page.getByTestId('add-to-button').click()
    const menu = page.getByTestId('add-to-menu')
    await expect(menu.getByTestId('add-to-research-assistant')).toContainText('tokens per message (estimate)')
    await expect(menu.getByTestId('add-to-support-desk')).toContainText('Loaded when the model asks for it')
    await menu.getByRole('button', { name: 'Add to handbook' }).click()
    await expect.poll(() => seen.saves.length).toBe(1)
    expect(seen.saves[0].name).toBe('handbook')
    expect(seen.saves[0].body.selected_skills).toEqual(['summarise-pdf', 'translate-document'])
    expect(seen.saves[0].body.enable_skills).toBe(true)
    await expect(page.getByTestId('skill-used-translate-document')).toHaveText('Used by handbook')
    await expect(page.getByTestId('skill-not-used')).toHaveCount(0)
  })

  test('adding to an agent with skills off turns them on with only that skill', async ({ page }) => {
    const { seen } = await mockLibrary(page)
    await page.goto('/app/skills?skill=translate-document')
    await page.getByTestId('add-to-button').click()
    await page.getByRole('button', { name: 'Add to idle-agent' }).click()
    await expect.poll(() => seen.saves.length).toBe(1)
    expect(seen.saves[0].body).toMatchObject({ name: 'idle-agent', enable_skills: true, selected_skills: ['translate-document'] })
  })

  test('removing a skill from an agent saves the rest, and Undo puts it back', async ({ page }) => {
    const { seen } = await mockLibrary(page)
    await page.goto('/app/skills?skill=query-database')
    await page.getByRole('button', { name: 'Remove from research-assistant' }).click()
    await expect.poll(() => seen.saves.length).toBe(1)
    expect(seen.saves[0].body.selected_skills).toEqual(['summarise-pdf'])
    await expect(page.getByTestId('skill-used-query-database')).toHaveText('Not used yet')
    await page.getByTestId('library-undo-toast').getByRole('button', { name: 'Undo' }).click()
    await expect.poll(() => seen.saves.length).toBe(2)
    expect(seen.saves[1].body.selected_skills).toEqual(['summarise-pdf', 'query-database'])
    await expect(page.getByTestId('skill-used-query-database')).toHaveText('Used by research-assistant')
  })

  test('removing the only skill switches skills off instead of selecting every skill', async ({ page }) => {
    const { seen } = await mockLibrary(page)
    await page.goto('/app/skills?skill=triage-issue')
    await page.getByRole('button', { name: 'Remove from support-desk' }).click()
    await expect.poll(() => seen.saves.length).toBe(1)
    expect(seen.saves[0].body).toMatchObject({ name: 'support-desk', enable_skills: false, selected_skills: [] })
    await expect(page.getByTestId('library-undo-toast')).toContainText('Skills are now off')
  })

  test('when the agents cannot be read the page says nothing about use', async ({ page }) => {
    await mockLibrary(page, { agentsStatus: 500 })
    await page.goto('/app/skills')
    await expect(page.getByTestId('skill-row-summarise-pdf')).toBeVisible()
    await expect(page.getByTestId('skill-used-summarise-pdf')).toHaveText('Use unknown')
    await expect(page.getByTestId('skills-filter-used')).toBeDisabled()
    await expect(page.locator('[data-testid^="skill-used-"]').filter({ hasText: 'Not used yet' })).toHaveCount(0)
    await expect(page.getByTestId('used-by')).toContainText('could not be read')
  })

  test('an empty library teaches what a skill is', async ({ page }) => {
    await mockLibrary(page, { skills: [] })
    await page.goto('/app/skills')
    const empty = page.getByTestId('skills-empty')
    await expect(empty.getByRole('heading', { name: 'Skills are instructions an agent can follow' })).toBeVisible()
    await expect(empty.getByRole('button', { name: 'Create skill' })).toBeVisible()
    await expect(empty.getByText('Import', { exact: true })).toBeVisible()
  })

  test('Git repositories open in the pane and a repository can be added', async ({ page }) => {
    await mockLibrary(page)
    await page.goto('/app/skills')
    await page.getByTestId('skills-git-toggle').click()
    const git = page.getByTestId('skills-git')
    await expect(git.getByText('No Git repos configured')).toBeVisible()
    await git.getByLabel('Repository URL').fill('https://github.com/example/skills')
    await git.getByRole('button', { name: 'Add repo' }).click()
    await expect(git.getByText('https://github.com/example/skills')).toBeVisible()
  })

  test('on a phone the list and the skill take turns', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await mockLibrary(page)
    await page.goto('/app/skills')
    await expect(page.getByTestId('skill-row-summarise-pdf')).toBeVisible()
    await expect(page.getByTestId('skill-pane')).toBeHidden()
    await page.getByTestId('skill-row-summarise-pdf').click()
    await expect(page.getByTestId('skill-title')).toBeVisible()
    await expect(page.getByTestId('skill-row-summarise-pdf')).toBeHidden()
    await page.getByRole('button', { name: 'All skills' }).click()
    await expect(page.getByTestId('skill-row-summarise-pdf')).toBeVisible()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(0)
  })

  test('wide, the first skill is open until another is picked', async ({ page }) => {
    await mockLibrary(page)
    await page.goto('/app/skills')
    await expect(page.getByTestId('skill-title')).toHaveText('summarise-pdf')
    await page.getByTestId('skill-row-triage-issue').click()
    await expect(page.getByTestId('skill-title')).toHaveText('triage-issue')
  })
})
