import { test, expect } from './coverage-fixtures.js'
import { installMove } from './project-agent-move-fixtures.js'
import { writes } from './projects-fixtures.js'
import { mockAttached } from './project-attached-fixtures.js'

// What a project attaches (on for every chat and agent of the project) next to
// its limit, the project skills on the project page, the locked rows of the
// chat menus, and the report of an agent move. The API is stubbed.

const KINDS = ['skills', 'addons', 'mcp_servers', 'connectors', 'collections']

test.describe('Project form: attached tools', () => {
  test('the form has an Attached part next to the Limit, each kind with its own picker, and says what each does', async ({ page }) => {
    const s = await mockAttached(page)
    await page.goto('/app/projects')
    await page.getByTestId('project-new').click()
    const sheet = page.getByTestId('project-sheet')
    await sheet.getByLabel('Name', { exact: true }).fill('Launch')
    await expect(sheet.getByTestId('project-attached-kind')).toHaveText("Always on for this project's chats and agents")
    await expect(sheet.getByTestId('project-defaults-kind')).toHaveText('Limit: only these can be used')
    for (const key of KINDS) await expect(sheet.getByTestId(`project-attached-section-${key}`)).toBeVisible()

    await sheet.getByTestId('project-attached-section-skills').locator('summary').click()
    await sheet.getByTestId('project-attached-pick-skills').getByRole('button', { name: /summarize/ }).click()
    await sheet.getByTestId('project-attached-section-addons').locator('summary').click()
    await sheet.getByTestId('project-attached-pick-addons').getByRole('button', { name: /Mail/ }).click()
    await sheet.getByTestId('project-attached-section-mcp_servers').locator('summary').click()
    await sheet.getByTestId('project-attached-pick-mcp_servers').getByRole('button', { name: /docs/ }).click()
    await sheet.getByTestId('project-attached-section-connectors').locator('summary').click()
    await sheet.getByTestId('project-attached-pick-connectors').getByRole('button', { name: /Work mail/ }).click()
    await sheet.getByTestId('project-attached-section-collections').locator('summary').click()
    await sheet.getByTestId('project-attached-pick-collections').getByRole('button', { name: /papers/ }).click()
    await expect(sheet.getByTestId('project-attached').getByTestId('project-attached-entry')).toHaveCount(5)
    // The Limit part did not change.
    await expect(sheet.getByTestId('project-defaults').getByTestId('project-default-entry')).toHaveCount(0)
    await sheet.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByRole('heading', { name: 'Launch', level: 1 })).toBeVisible()
    const body = writes(s, 'POST', '/api/chats/projects')[0].body
    expect(body.attached).toEqual({ skills: ['summarize'], addons: ['mail-addon'], mcp_servers: ['docs'], connectors: ['mail'], collections: ['papers'] })
    expect(body.defaults).toEqual({})
  })

  test('an attached entry outside the limit is marked at once, and the refusal of the server shows inline with the sheet open', async ({ page }) => {
    const s = await mockAttached(page)
    await page.goto('/app/projects/p2')
    await page.getByTestId('project-edit').click()
    const sheet = page.getByTestId('project-sheet')
    // A limit with one skill, then an attached skill that the limit leaves out.
    await sheet.getByTestId('project-defaults-section-skills').locator('summary').click()
    await sheet.getByTestId('project-pick-skills').getByRole('button', { name: /brand-voice/ }).click()
    await sheet.getByTestId('project-attached-section-skills').locator('summary').click()
    await sheet.getByTestId('project-attached-pick-skills').getByRole('button', { name: /summarize/ }).click()
    const chip = sheet.getByTestId('project-attached-skills').getByTestId('project-attached-entry')
    await expect(chip).toHaveAttribute('data-state', 'outside')
    await expect(chip).toContainText('Outside the limit')
    await expect(sheet.getByTestId('project-attached-outside')).toContainText('summarize')
    await sheet.getByRole('button', { name: 'Save' }).click()
    const error = sheet.getByTestId('project-sheet-error')
    await expect(error).toContainText('attached skills entry "summarize" is outside the limit')
    await expect(sheet).toBeVisible()
    expect(s.calls.filter(c => c.refused)).toHaveLength(1)
    // Adding it to the limit clears the mark and the save goes through.
    await sheet.getByTestId('project-pick-skills').getByRole('button', { name: /summarize/ }).click()
    await expect(sheet.getByTestId('project-attached-outside')).toHaveCount(0)
    await sheet.getByRole('button', { name: 'Save' }).click()
    await expect(sheet).toHaveCount(0)
    expect(writes(s, 'PATCH', '/projects/p2').at(-1).body.attached).toEqual({ skills: ['summarize'] })
  })
})

test.describe('Project page: Tools tab', () => {
  test('lists the attached tools of all five kinds under their own label, and saves only what changed', async ({ page }) => {
    const s = await mockAttached(page)
    await page.goto('/app/projects/p1?tab=tools')
    const attached = page.getByTestId('project-attached')
    await expect(attached.getByTestId('project-attached-kind')).toContainText('Always on')
    await expect(attached.getByTestId('project-attached-skills')).toContainText('brand-voice')
    await expect(attached.getByTestId('project-attached-addons')).toContainText('Mail')
    await expect(attached.getByTestId('project-attached-mcp_servers')).toContainText('docs')
    await expect(attached.getByTestId('project-attached-connectors')).toContainText('Work mail')
    await expect(attached.getByTestId('project-attached-collections')).toContainText('papers')
    await expect(page.getByTestId('project-defaults-kind')).toContainText('Limit')
    await attached.getByTestId('project-attached-section-skills').locator('summary').click()
    await attached.getByTestId('project-attached-skills').getByRole('button', { name: /Remove brand-voice/ }).click()
    await page.getByTestId('project-tools-save').click()
    await expect(page.getByTestId('project-tools-save')).toBeDisabled()
    const patch = writes(s, 'PATCH', '/projects/p1')[0]
    expect(patch.body).toEqual({ attached: { addons: ['mail-addon'], mcp_servers: ['docs'], connectors: ['mail'], collections: ['papers'] } })
  })

  test('says when the attached block is off because the folder has other writers, and the lists are read-only', async ({ page }) => {
    await mockAttached(page)
    await page.goto('/app/projects/p9?tab=tools')
    await expect(page.getByTestId('project-attached-blocked')).toContainText('Attached tools are off while this folder has other writers')
    await expect(page.getByTestId('project-attached').getByRole('button', { name: /Remove/ })).toHaveCount(0)
  })

  test('a refusal of the server shows under the lists and the edit stays', async ({ page }) => {
    await mockAttached(page)
    await page.goto('/app/projects/p1?tab=tools')
    await page.getByTestId('project-attached-section-skills').locator('summary').click()
    await page.getByTestId('project-attached-pick-skills').getByRole('button', { name: /summarize/ }).click()
    await expect(page.getByTestId('project-attached-outside')).toContainText('summarize')
    await page.getByTestId('project-tools-save').click()
    await expect(page.getByTestId('project-tools-error')).toContainText('outside the limit')
    await expect(page.getByTestId('project-tools-save')).toBeEnabled()
  })
})

test.describe('Project page: Skills tab', () => {
  test('sits next to Memory and lists the project skills', async ({ page }) => {
    await mockAttached(page)
    await page.goto('/app/projects/p1')
    await expect(page.getByTestId('project-tab-skills')).toBeVisible()
    const tabs = await page.getByRole('tab').allTextContents()
    expect(tabs.indexOf('Skills')).toBe(tabs.indexOf('Memory') + 1)
    await page.getByTestId('project-tab-skills').click()
    await expect(page).toHaveURL(/tab=skills/)
    await expect(page.getByTestId('project-skill')).toHaveCount(2)
    await expect(page.getByTestId('project-skill').first()).toContainText('house-style')
    await expect(page.getByTestId('project-skill').first()).toContainText('Plain words')
    await expect(page.getByTestId('project-skills-blocked')).toHaveCount(0)
  })

  test('create goes through the skill editor in the project layer and returns to the tab', async ({ page }) => {
    const s = await mockAttached(page)
    await page.goto('/app/projects/p1?tab=skills')
    await page.getByTestId('project-skill-new').click()
    await expect(page).toHaveURL(/\/app\/skills\/new\?layer=project&project=p1/)
    await expect(page.getByTestId('skill-edit-layer')).toHaveText('Project')
    await page.getByLabel('Name').fill('press-notes')
    await page.getByLabel('Description').fill('How to write press notes.')
    await page.getByRole('button', { name: 'Create' }).click()
    await expect(page).toHaveURL(/\/app\/projects\/p1\?tab=skills/)
    await expect(page.getByTestId('project-skill')).toHaveCount(3)
    expect(s.skillWrites[0].body).toMatchObject({ layer: 'project', project: 'p1', name: 'press-notes' })
  })

  test('edit uses the editor with the project in the route, delete asks first', async ({ page }) => {
    const s = await mockAttached(page)
    await page.goto('/app/projects/p1?tab=skills')
    await page.getByTestId('project-skill').first().getByTestId('project-skill-edit').click()
    await expect(page).toHaveURL(/\/app\/skills\/edit\/project\/house-style\?project=p1/)
    await page.getByLabel('Description').fill('Plain words only.')
    await page.getByRole('button', { name: 'Save' }).click()
    await expect(page).toHaveURL(/\/app\/projects\/p1\?tab=skills/)
    const put = s.skillWrites.find(w => w.method === 'PUT')
    expect(put.query).toBe('?project=p1')
    expect(put.body).toMatchObject({ description: 'Plain words only.', project: 'p1' })

    await page.getByTestId('project-skill').nth(1).getByTestId('project-skill-delete').click()
    await expect(page.getByRole('alertdialog')).toContainText('Delete the skill launch-checklist?')
    await page.getByRole('alertdialog').getByRole('button', { name: 'Cancel' }).click()
    await expect(page.getByTestId('project-skill')).toHaveCount(2)
    await page.getByTestId('project-skill').nth(1).getByTestId('project-skill-delete').click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Delete' }).click()
    await expect(page.getByTestId('project-skill')).toHaveCount(1)
    const del = s.skillWrites.find(w => w.method === 'DELETE')
    expect(del.path).toMatch(/\/skills\/project\/launch-checklist$/)
    expect(del.query).toBe('?project=p1')
  })

  test('says when the layer is off because the folder has other writers', async ({ page }) => {
    await mockAttached(page)
    await page.goto('/app/projects/p9?tab=skills')
    await expect(page.getByTestId('project-skills-blocked')).toHaveText('Project skills are off while this folder has other writers.')
    await expect(page.getByTestId('project-skill-new')).toBeDisabled()
  })

  test('an archived project lists its skills with no actions', async ({ page }) => {
    await mockAttached(page)
    await page.goto('/app/projects/p3?tab=skills')
    await expect(page.getByTestId('project-skill-new')).toBeDisabled()
    await expect(page.getByTestId('project-skill-edit')).toHaveCount(0)
  })
})

test.describe('Chat of a project: attached tools', () => {
  const stubChat = async (page) => {
    const s = await mockAttached(page, { listChats: true })
    await page.route(/\/api\/chats\/c1\/skills$/, r => r.fulfill({ json: { off: [] } }))
    await page.route(/\/api\/chats\/skills(\?.*)?$/, (r) => {
      if (new URL(r.request().url()).searchParams.get('chat') !== 'c1') return r.fallback()
      return r.fulfill({ json: { skills: [
        { id: 'personal:brand-voice', name: 'brand-voice', layer: 'personal', description: 'Keeps the tone plain.', enabled: false, editable: true },
        { id: 'personal:summarize', name: 'summarize', layer: 'personal', description: 'Short summaries.', enabled: true, editable: true },
        { id: 'project:house-style', name: 'house-style', layer: 'project', project: 'p1', description: 'Plain words.', enabled: true },
      ] } })
    })
    await page.route(/\/api\/chats\/c1\/addons(\?.*)?$/, r => r.fulfill({ json: { addons: [{ id: 'mail-addon', title: 'Mail', tools: 3, off: true }, { id: 'cal-addon', title: 'Calendar', tools: 2, off: true }] } }))
    await page.route(/\/api\/chats\/c1\/tools(\?.*)?$/, r => r.fulfill({ json: { builtins: [], servers: [{ name: 'docs', off: true }], connectors: [{ id: 'mail', name: 'Work mail', off: true }], off: [] } }))
    return s
  }

  test('the Skills menu shows attached and project skills on, with the project mark and no switch to use', async ({ page }) => {
    await stubChat(page)
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-skills-button').click()
    const rows = page.getByTestId('chat-skills-item')
    await expect(rows).toHaveCount(3)
    const brand = rows.filter({ hasText: 'brand-voice' })
    await expect(brand).toHaveAttribute('data-fromproject', 'true')
    await expect(brand.getByTestId('chat-skills-source')).toHaveText('From project')
    await expect(brand.getByRole('switch')).toBeChecked()
    await expect(brand.getByRole('switch')).toBeDisabled()
    await expect(rows.filter({ hasText: 'house-style' }).getByRole('switch')).toBeDisabled()
    // A skill the project does not attach keeps its own switch.
    await expect(rows.filter({ hasText: 'summarize' }).getByRole('switch')).toBeEnabled()
    await expect(page.getByTestId('chat-skills-summary')).toHaveText('1 of 1 on')
  })

  test('the Tools menu shows attached add-ons, servers and connectors on and locked, and names the collections', async ({ page }) => {
    await stubChat(page)
    await page.goto('/app/chats/c1')
    await page.getByTestId('chat-tools-button').click()
    const rows = page.getByTestId('chat-tools-item')
    for (const [name, locked] of [['Mail', true], ['docs', true], ['Work mail', true], ['Calendar', false]]) {
      const row = rows.filter({ hasText: name }).first()
      await expect(row.getByRole('switch')).toHaveAttribute('aria-checked', locked ? 'true' : 'false')
      if (locked) {
        await expect(row.getByRole('switch')).toBeDisabled()
        await expect(row).toContainText('From project')
      } else {
        await expect(row.getByRole('switch')).toBeEnabled()
        await expect(row).not.toContainText('From project')
      }
    }
    await expect(page.getByTestId('chat-tools-project-collections')).toHaveText('Collections from the project: papers.')
  })
})

test.describe('Agent move: attached tools', () => {
  test('the dialog lists what the old project attached and what the new one attaches', async ({ page }) => {
    const s = await installMove(page, {
      report: { attached_old: { skills: ['house-style'], connectors: ['mail'] }, attached_new: { addons: ['calendar'] }, project_skills_switch: true },
    })
    await page.goto('/app/agents/a9/edit')
    await page.getByTestId('agent-project-select').selectOption('p2')
    await page.getByTestId('agent-project-move').click()
    const dialog = page.getByTestId('agent-move-dialog')
    await expect(dialog.locator('[data-effect="attachedLost"]')).toContainText('2 tools that no longer apply: house-style, mail')
    await expect(dialog.locator('[data-effect="attachedGained"]')).toContainText('1 tool that the agent gets: calendar')
    await expect(dialog.locator('[data-effect="projectSkills"]')).toContainText('skills of the old project stop applying')
    expect(s.moves.every(m => m.body.dry_run)).toBe(true)
  })
})
