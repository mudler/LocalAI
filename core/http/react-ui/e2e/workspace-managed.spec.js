import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// The folders LocalAI manages in the Files page, with the API stubbed: the
// marker, the group under the user's own files, the line at the top of
// LocalAI/, and the extra confirmation before a delete or a rename.

const HOME = [
  { path: '/Chats', name: 'Chats', kind: 'dir', size: 0, version: 1, managed: 'chats' },
  { path: '/LocalAI', name: 'LocalAI', kind: 'dir', size: 0, version: 1, managed: 'localai' },
  { path: '/Notes', name: 'Notes', kind: 'dir', size: 0, version: 1 },
  { path: '/report.txt', name: 'report.txt', kind: 'file', size: 12, version: 1 },
]
const SETTINGS = [
  { path: '/LocalAI/persona.json', name: 'persona.json', kind: 'file', size: 40, version: 2, managed: 'localai' },
  { path: '/LocalAI/ui.json', name: 'ui.json', kind: 'file', size: 30, version: 1, managed: 'localai' },
]

async function stubFiles(page) {
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
    return route.fulfill({ json: { path, entries: path === '/LocalAI' ? SETTINGS : HOME } })
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
    await expect(rows).toHaveCount(4)
    await expect(rows.nth(0)).toContainText('Notes/')
    await expect(rows.nth(1)).toContainText('report.txt')
    const group = page.getByTestId('ws-managed')
    await expect(group.getByRole('heading', { name: 'Managed by LocalAI' })).toBeVisible()
    const managed = group.getByTestId('ws-row')
    await expect(managed.nth(0)).toContainText('LocalAI/')
    await expect(managed.nth(0)).toContainText('managed by LocalAI')
    await expect(managed.nth(0)).toContainText('Settings LocalAI reads: persona, profile, shortcuts, Home, theme.')
    await expect(managed.nth(1)).toContainText('Chats/')
    await expect(page.getByTestId('ws-managed-tag')).toHaveCount(2)
    await page.screenshot({ path: process.env.LAYOUT_SHOTS ? `${process.env.LAYOUT_SHOTS}/files-home-after.png` : undefined, fullPage: true })

    await managed.nth(0).getByRole('button', { name: 'LocalAI/' }).click()
    await expect(page.getByTestId('ws-folder-about')).toContainText('Settings LocalAI reads')
    await expect(page.getByTestId('ws-managed')).toHaveCount(0)
    await expect(page.getByTestId('ws-row')).toHaveCount(2)
    await page.screenshot({ path: process.env.LAYOUT_SHOTS ? `${process.env.LAYOUT_SHOTS}/files-localai-after.png` : undefined, fullPage: true })
  })

  test('asks before LocalAI/ goes to the Trash and says what stops working; Cancel changes nothing', async ({ page }) => {
    const calls = await stubFiles(page)
    await page.goto('/app/files')
    const row = page.getByTestId('ws-managed').getByTestId('ws-row').first()
    await row.getByRole('button', { name: 'Move to Trash' }).click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('Move LocalAI to the Trash?')
    await expect(dialog).toContainText('LocalAI forgets your persona, your profile, your shortcuts, your Home layout and your theme.')
    await expect(dialog).toContainText('You can restore it from the Trash.')
    await page.screenshot({ path: process.env.LAYOUT_SHOTS ? `${process.env.LAYOUT_SHOTS}/files-confirm-after.png` : undefined })
    await dialog.getByRole('button', { name: 'Cancel' }).click()
    await expect(dialog).toHaveCount(0)
    expect(calls).toEqual([])

    await row.getByRole('button', { name: 'Move to Trash' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Move to Trash' }).click()
    await expect.poll(() => calls).toEqual([['delete', '/LocalAI']])
  })

  test('asks before a settings file is renamed, and not before the user\'s own file goes', async ({ page }) => {
    const calls = await stubFiles(page)
    await page.goto('/app/files?path=/LocalAI')
    const persona = page.getByTestId('ws-row').filter({ hasText: 'persona.json' })
    await persona.getByRole('button', { name: 'Rename' }).click()
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
    await page.getByTestId('ws-row').filter({ hasText: 'report.txt' }).getByRole('button', { name: 'Move to Trash' }).click()
    await expect.poll(() => calls.at(-1)).toEqual(['delete', '/report.txt'])
    await expect(page.getByRole('alertdialog')).toHaveCount(0)
  })
})
