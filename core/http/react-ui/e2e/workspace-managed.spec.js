import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// The folders LocalAI manages in the Files page, with the API stubbed: the
// marker, the group under the user's own files, the line at the top of
// LocalAI/, and the extra confirmation before a delete or a rename.

const HOME = [
  { path: '/Chats', name: 'Chats', kind: 'dir', size: 0, version: 1, managed: 'chats' },
  { path: '/LocalAI', name: 'LocalAI', kind: 'dir', size: 0, version: 1, managed: 'localai' },
  { path: '/Memory', name: 'Memory', kind: 'dir', size: 0, version: 1, managed: 'memory' },
  { path: '/pages', name: 'pages', kind: 'dir', size: 0, version: 1, managed: 'pages' },
  { path: '/.skills', name: '.skills', kind: 'dir', size: 0, version: 1, managed: 'skills' },
  { path: '/Scratch', name: 'Scratch', kind: 'dir', size: 0, version: 1, managed: 'future' },
  { path: '/Notes', name: 'Notes', kind: 'dir', size: 0, version: 1 },
  { path: '/report.txt', name: 'report.txt', kind: 'file', size: 12, version: 1 },
]
const SETTINGS = [
  { path: '/LocalAI/persona.json', name: 'persona.json', kind: 'file', size: 40, version: 2, managed: 'localai' },
  { path: '/LocalAI/ui.json', name: 'ui.json', kind: 'file', size: 30, version: 1, managed: 'localai' },
]

// The home with Projects/ and the folders of two projects in it. The server
// labels Projects/ only: the folders inside it are the user's own.
const PROJECTS_ROW = { path: '/Projects', name: 'Projects', kind: 'dir', size: 0, version: 1, managed: 'projects' }
const PROJECT_FOLDERS = [
  { path: '/Projects/client-site', name: 'client-site', kind: 'dir', size: 0, version: 1 },
  { path: '/Projects/orphan', name: 'orphan', kind: 'dir', size: 0, version: 1 },
]

async function stubFiles(page, { withProjects = false } = {}) {
  const calls = []
  await page.route(/\/api\/chats\/files\/trash(\?.*)?$/, route => route.fulfill({ json: { entries: [] } }))
  await page.route(/\/api\/chats\/files\/move(\?.*)?$/, route => {
    calls.push(['move', route.request().postDataJSON()])
    return route.fulfill({ json: { path: '/x', name: 'x', kind: 'dir', size: 0, version: 1 } })
  })
  await page.route(/\/api\/chats\/files(\?.*)?$/, route => {
    const url = new URL(route.request().url())
    const path = url.searchParams.get('path') || '/'
    if (route.request().method() === 'DELETE') {
      calls.push(['delete', path])
      return route.fulfill({ json: {} })
    }
    if (path === '/Projects') return route.fulfill({ json: { path, entries: PROJECT_FOLDERS } })
    const home = withProjects ? [...HOME, PROJECTS_ROW] : HOME
    return route.fulfill({ json: { path, entries: path === '/LocalAI' ? SETTINGS : home } })
  })
  return calls
}

test.describe('Files: the folders LocalAI manages', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
  })

  test('lists the user\'s own files first and the managed folders last, marked and explained', async ({ page }) => {
    await stubFiles(page)
    await page.goto('/app/files')
    const rows = page.getByTestId('ws-row')
    await expect(rows).toHaveCount(8)
    await expect(rows.nth(0)).toContainText('Notes')
    await expect(rows.nth(1)).toContainText('report.txt')
    const group = page.getByTestId('ws-managed')
    await expect(group.getByRole('heading', { name: 'Managed by LocalAI' })).toBeVisible()
    const managed = group.getByTestId('ws-row')
    await expect(managed.nth(0)).toContainText('LocalAI')
    await expect(managed.nth(0)).toContainText('managed by LocalAI')
    await expect(managed.nth(0)).toContainText('Settings LocalAI reads: persona, profile, shortcuts, Home, theme.')
    await expect(managed.nth(1)).toContainText('Chats')
    await expect(managed.nth(2)).toContainText('Memory')
    await expect(managed.nth(2)).toContainText('The notes the assistant keeps about you.')
    await expect(managed.nth(3)).toContainText('pages')
    await expect(managed.nth(4)).toContainText('.skills')
    await expect(managed.nth(4)).toContainText('The skills the agents use.')
    await expect(page.getByTestId('ws-managed-tag')).toHaveCount(6)
    await page.screenshot({ path: process.env.LAYOUT_SHOTS ? `${process.env.LAYOUT_SHOTS}/files-home-after.png` : undefined, fullPage: true })

    await managed.nth(0).getByRole('button', { name: 'LocalAI', exact: true }).click()
    await expect(page.getByTestId('ws-folder-about')).toContainText('Settings LocalAI reads')
    await expect(page.getByTestId('ws-managed')).toHaveCount(0)
    await expect(page.getByTestId('ws-row')).toHaveCount(2)
    await page.screenshot({ path: process.env.LAYOUT_SHOTS ? `${process.env.LAYOUT_SHOTS}/files-localai-after.png` : undefined, fullPage: true })
  })

  test('groups a folder it never heard of because the listing marks it, and keeps the folders of the person out of the group', async ({ page }) => {
    await stubFiles(page)
    await page.goto('/app/files')
    const group = page.getByTestId('ws-managed')
    const unknown = group.getByTestId('ws-row').filter({ hasText: 'Scratch' })
    await expect(unknown).toHaveCount(1)
    await expect(unknown).toContainText('managed by LocalAI')
    await expect(unknown).toContainText('Created and used by LocalAI.')
    await expect(unknown.getByTestId('ws-managed-glyph')).toBeVisible()
    await expect(group.getByRole('button', { name: 'Notes', exact: true })).toHaveCount(0)
    await expect(group.getByRole('button', { name: 'report.txt', exact: true })).toHaveCount(0)
    await expect(page.getByTestId('ws-row').filter({ has: page.getByRole('button', { name: 'Notes', exact: true }) }).getByTestId('ws-managed-glyph')).toHaveCount(0)
  })

  test('opens the page of a managed folder from the keyboard, and offers none for a folder without one', async ({ page }) => {
    await stubFiles(page)
    await page.goto('/app/files')
    const skills = page.getByTestId('ws-managed').getByTestId('ws-row').filter({ hasText: '.skills' })
    const trigger = skills.getByRole('button', { name: 'More actions for .skills' })
    await trigger.focus()
    await page.keyboard.press('Enter')
    await expect(page.getByRole('menuitem', { name: 'Open Skills' })).toBeVisible()
    await page.keyboard.press('Escape')
    const chats = page.getByTestId('ws-managed').getByTestId('ws-row').filter({ hasText: 'Chats' })
    await chats.getByRole('button', { name: 'More actions for Chats' }).click()
    await expect(page.getByRole('menuitem', { name: /^Open (Skills|Pages|Memory)$/ })).toHaveCount(0)
    await page.keyboard.press('Escape')
    await skills.getByRole('button', { name: 'More actions for .skills' }).click()
    await page.getByRole('menuitem', { name: 'Open Skills' }).click()
    await expect(page).toHaveURL(/\/app\/skills/)
  })

  test('asks before LocalAI/ goes to the Trash and says what stops working; Cancel changes nothing', async ({ page }) => {
    const calls = await stubFiles(page)
    await page.goto('/app/files')
    const row = page.getByTestId('ws-managed').getByTestId('ws-row').first()
    const trash = async () => {
      await row.getByRole('button', { name: 'More actions for LocalAI' }).click()
      await page.getByRole('menuitem', { name: 'Move to Trash' }).click()
    }
    await trash()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('Move LocalAI to the Trash?')
    await expect(dialog).toContainText('LocalAI forgets your persona, your profile, your shortcuts, your Home layout and your theme.')
    await expect(dialog).toContainText('You can restore it from the Trash.')
    await page.screenshot({ path: process.env.LAYOUT_SHOTS ? `${process.env.LAYOUT_SHOTS}/files-confirm-after.png` : undefined })
    await dialog.getByRole('button', { name: 'Cancel' }).click()
    await expect(dialog).toHaveCount(0)
    expect(calls).toEqual([])

    await trash()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Move to Trash' }).click()
    await expect.poll(() => calls).toEqual([['delete', '/LocalAI']])
  })

  test('asks before a settings file is renamed, and not before the user\'s own file goes', async ({ page }) => {
    const calls = await stubFiles(page)
    await page.goto('/app/files?path=/LocalAI')
    const persona = page.getByTestId('ws-row').filter({ hasText: 'persona.json' })
    await persona.getByRole('button', { name: 'More actions for persona.json' }).click()
    await page.getByRole('menuitem', { name: 'Rename' }).click()
    const box = page.getByRole('textbox', { name: 'Rename' })
    await box.fill('old-persona.json')
    await box.press('Enter')
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('Rename LocalAI/persona.json?')
    await expect(dialog).toContainText('The assistant loses its name and its instructions.')
    await dialog.getByRole('button', { name: 'Rename' }).click()
    await expect.poll(() => calls.length).toBe(1)
    expect(calls[0][0]).toBe('move')

    await page.goto('/app/files')
    await page.getByTestId('ws-row').filter({ hasText: 'report.txt' }).getByRole('button', { name: 'More actions for report.txt' }).click()
    await page.getByRole('menuitem', { name: 'Move to Trash' }).click()
    await expect.poll(() => calls.at(-1)).toEqual(['delete', '/report.txt'])
    await expect(page.getByRole('alertdialog')).toHaveCount(0)
  })
  test('labels Projects/ as managed, in the order after Chats, with its own page and a description', async ({ page }) => {
    await stubFiles(page, { withProjects: true })
    await page.goto('/app/files')
    const managed = page.getByTestId('ws-managed').getByTestId('ws-row')
    await expect(managed.nth(1)).toContainText('Chats')
    await expect(managed.nth(2)).toContainText('Projects')
    await expect(managed.nth(2)).toContainText('managed by LocalAI')
    await expect(managed.nth(2)).toContainText('Your projects. Each folder is one project.')
    await managed.nth(2).getByRole('button', { name: 'More actions for Projects' }).click()
    await page.getByRole('menuitem', { name: 'Open Projects' }).click()
    await expect(page).toHaveURL(/\/app\/projects$/)
  })

  test('asks before the Projects root goes to the Trash and says what every project loses', async ({ page }) => {
    const calls = await stubFiles(page, { withProjects: true })
    await page.goto('/app/files')
    const row = page.getByTestId('ws-managed').getByTestId('ws-row').filter({ hasText: 'Your projects' })
    await row.getByRole('button', { name: 'More actions for Projects' }).click()
    await page.getByRole('menuitem', { name: 'Move to Trash' }).click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('Move Projects to the Trash?')
    await expect(dialog).toContainText('Every project loses its files and memory')
    await dialog.getByRole('button', { name: 'Cancel' }).click()
    expect(calls).toEqual([])
    await row.getByRole('button', { name: 'More actions for Projects' }).click()
    await page.getByRole('menuitem', { name: 'Move to Trash' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Move to Trash' }).click()
    await expect.poll(() => calls).toEqual([['delete', '/Projects']])
  })

  test('inside Projects/ a folder shows the project name and links to the project page', async ({ page }) => {
    await stubFiles(page, { withProjects: true })
    await page.route(/\/api\/chats\/projects(\?.*)?$/, route => route.fulfill({
      json: { projects: [{ id: 'p1', name: 'Client site redesign', slug: 'client-site', status: 'active' }] },
    }))
    await page.goto('/app/files?path=/Projects')
    const named = page.getByTestId('ws-row').filter({ hasText: 'client-site' })
    const link = named.getByTestId('ws-project-link')
    await expect(link).toHaveText('Client site redesign')
    await expect(link).toHaveAttribute('href', '/app/projects/p1')
    await expect(page.getByTestId('ws-row').filter({ hasText: 'orphan' }).getByTestId('ws-project-link')).toHaveCount(0)
    await expect(page.getByTestId('ws-managed-tag')).toHaveCount(0)
    await link.click()
    await expect(page).toHaveURL(/\/app\/projects\/p1$/)
  })
})
