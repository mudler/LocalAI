import { test, expect } from './coverage-fixtures.js'
import { mockChat, openChat, storedChats, chatOf, pair } from './chat-fixtures.js'
import { sampleChats } from './home-fixtures.js'

// The conversations list opens on Ctrl K. It replaces the permanent history
// pane: groups by day like the Home list, search, resume, rename, delete with
// undo, and the same duplicate, copy and export actions as before.

const menu = (page) => page.getByTestId('chats-menu')
const rows = (page) => menu(page).getByTestId('chats-row')

async function openMenu(page) {
  await expect(page.getByTestId('chats-trigger')).toBeVisible()
  await page.keyboard.press('Control+k')
  await expect(menu(page)).toBeVisible()
  // The search box takes focus a moment after the menu opens; keys need it.
  await expect(menu(page).getByRole('combobox')).toBeFocused()
}

test.describe('Chat conversations menu', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page, { models: [
      { id: 'qwen3-8b-instruct', capabilities: ['FLAG_CHAT'] },
      { id: 'gemma-4-e4b-it-qat-q4_0', capabilities: ['FLAG_CHAT'] },
      { id: 'qwen2.5-vl-7b-instruct', capabilities: ['FLAG_CHAT'] },
    ], loaded: ['qwen3-8b-instruct'] })
    await openChat(page, sampleChats())
    await expect(page.getByTestId('chat-message').first()).toBeVisible()
  })

  test('opens on Ctrl K, closes on Esc and gives focus back', async ({ page }) => {
    const composer = page.getByTestId('chat-input')
    await composer.focus()
    await openMenu(page)
    await expect(menu(page).getByRole('combobox')).toBeFocused()
    await page.keyboard.press('Escape')
    await expect(menu(page)).toHaveCount(0)
    await expect(composer).toBeFocused()
    // The button opens it too, and Ctrl K closes it again.
    await page.getByTestId('chats-trigger').click()
    await expect(menu(page)).toBeVisible()
    await page.keyboard.press('Control+k')
    await expect(menu(page)).toHaveCount(0)
  })

  test('groups the chats by day, newest first, with the model and the time', async ({ page }) => {
    await openMenu(page)
    await expect(page.getByTestId('chats-day-today').getByRole('heading')).toHaveText('Today')
    await expect(page.getByTestId('chats-day-yesterday').getByRole('heading')).toHaveText('Yesterday')
    await expect(page.getByTestId('chats-day-week').getByRole('heading')).toHaveText('Earlier this week')
    await expect(page.getByTestId('chats-day-today').getByTestId('chats-row')).toHaveCount(2)
    await expect(rows(page).first()).toContainText('Rewrite the 3.4 release notes for clarity')
    await expect(rows(page).first()).toContainText('qwen3-8b-instruct')
    // The line under the name is the last thing said.
    await expect(rows(page).first()).toContainText('Anytime.')
    // The open chat is marked.
    await expect(rows(page).first()).toHaveAttribute('data-current', 'true')
  })

  test('search narrows by name and by what was said, and says when nothing matches', async ({ page }) => {
    await openMenu(page)
    const search = menu(page).getByRole('combobox')
    await search.fill('regex')
    await expect(rows(page)).toHaveCount(1)
    await expect(rows(page).first()).toContainText('Regex for semver')
    // A word that only appears inside a message.
    await search.fill('accounting')
    await expect(rows(page)).toHaveCount(1)
    await expect(rows(page).first()).toContainText('Draft a polite reply about the invoice')
    await expect(rows(page).first()).toContainText('accounting')
    await search.fill('zzzz')
    await expect(rows(page)).toHaveCount(0)
    await expect(menu(page)).toContainText('No conversations match your search')
  })

  test('arrow keys move and Enter resumes that conversation', async ({ page }) => {
    await openMenu(page)
    await page.keyboard.press('ArrowDown')
    await expect(rows(page).nth(1)).toHaveAttribute('aria-selected', 'true')
    await page.keyboard.press('Enter')
    await expect(menu(page)).toHaveCount(0)
    await expect(page.getByTestId('chat-title')).toHaveText('Why does llama-cpp stall at a 4096 context')
    await expect.poll(async () => (await storedChats(page)).activeChatId).toBe('c-today-b')
  })

  test('clicking a row resumes it', async ({ page }) => {
    await openMenu(page)
    await rows(page).filter({ hasText: 'Regex for semver' }).click()
    await expect(page.getByTestId('chat-title')).toHaveText('Regex for semver with prerelease tags')
  })

  test('F2 renames in place and Enter saves', async ({ page }) => {
    await openMenu(page)
    await page.keyboard.press('F2')
    const box = menu(page).getByRole('textbox', { name: 'Rename' })
    await expect(box).toBeFocused()
    await box.fill('Release notes 3.4')
    await box.press('Enter')
    await expect(rows(page).first()).toContainText('Release notes 3.4')
    await expect.poll(async () => (await storedChats(page)).chats.find(c => c.id === 'c-today-a').name).toBe('Release notes 3.4')
    // The header shows it too.
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('chat-title')).toHaveText('Release notes 3.4')
  })

  test('Escape in the rename box cancels and keeps the menu open', async ({ page }) => {
    await openMenu(page)
    await page.keyboard.press('F2')
    await menu(page).getByRole('textbox', { name: 'Rename' }).fill('Nope')
    await page.keyboard.press('Escape')
    await expect(menu(page)).toBeVisible()
    await expect(rows(page).first()).toContainText('Rewrite the 3.4 release notes for clarity')
  })

  test('the title in the header renames the chat with a click', async ({ page }) => {
    await page.getByTestId('chat-title').click()
    const box = page.getByTestId('chat-title-input')
    await expect(box).toBeFocused()
    await box.fill('Clearer notes')
    await box.press('Enter')
    await expect(page.getByTestId('chat-title')).toHaveText('Clearer notes')
    await expect.poll(async () => (await storedChats(page)).chats.find(c => c.id === 'c-today-a').name).toBe('Clearer notes')
  })

  test('Delete hides the row and offers an undo; Undo brings it back', async ({ page }) => {
    await openMenu(page)
    const target = rows(page).filter({ hasText: 'Regex for semver' })
    await target.hover()
    await page.keyboard.press('Delete')
    await expect(rows(page).filter({ hasText: 'Regex for semver' })).toHaveCount(0)
    const toast = page.getByTestId('chat-undo-toast')
    await expect(toast).toContainText('Deleted "Regex for semver with prerelease tags"')
    // Until the undo time ends the chat is still stored.
    expect((await storedChats(page)).chats.map(c => c.id)).toContain('c-week-a')

    await toast.getByRole('button', { name: 'Undo' }).click()
    await expect(toast).toHaveCount(0)
    await expect(rows(page).filter({ hasText: 'Regex for semver' })).toHaveCount(1)
    expect((await storedChats(page)).chats.map(c => c.id)).toContain('c-week-a')
  })

  test('the chat is deleted for good when the undo time ends', async ({ page }) => {
    await openMenu(page)
    const row = rows(page).filter({ hasText: 'Regex for semver' })
    await row.hover()
    await row.getByTitle('Delete chat').click()
    const toast = page.getByTestId('chat-undo-toast')
    await expect(toast).toBeVisible()
    // Dismissing the toast ends the undo time at once.
    await toast.getByRole('button', { name: 'Dismiss' }).click()
    await expect(toast).toHaveCount(0)
    await expect.poll(async () => (await storedChats(page)).chats.map(c => c.id)).not.toContain('c-week-a')
  })

  test('the undo time runs out by itself after six seconds', async ({ page }) => {
    await page.clock.install()
    await page.reload()
    await page.getByTestId('chat-composer').waitFor()
    await openMenu(page)
    const row = rows(page).filter({ hasText: 'Regex for semver' })
    await row.hover()
    await row.getByTitle('Delete chat').click()
    await expect(page.getByTestId('chat-undo-toast')).toBeVisible()
    await page.clock.runFor(6500)
    await expect(page.getByTestId('chat-undo-toast')).toHaveCount(0)
    await expect.poll(async () => (await storedChats(page)).chats.map(c => c.id)).not.toContain('c-week-a')
  })

  test('deleting the open chat opens another one, and Undo returns to it', async ({ page }) => {
    await openMenu(page)
    await page.keyboard.press('Delete')
    await expect(page.getByTestId('chat-undo-toast')).toBeVisible()
    await expect(page.getByTestId('chat-title')).not.toHaveText('Rewrite the 3.4 release notes for clarity')
    await page.getByTestId('chat-undo-toast').getByRole('button', { name: 'Undo' }).click()
    await expect(page.getByTestId('chat-title')).toHaveText('Rewrite the 3.4 release notes for clarity')
  })

  test('Duplicate, Copy chat and Export are on every row', async ({ page }) => {
    await openMenu(page)
    const row = rows(page).first()
    await expect(row.getByTitle('Duplicate chat')).toBeVisible()
    await expect(row.getByTitle('Copy chat')).toBeVisible()
    await expect(row.getByTitle('Export as Markdown')).toBeVisible()
    await expect(row.getByTitle('Rename')).toBeVisible()
    await expect(row.getByTitle('Delete chat')).toBeVisible()
  })

  test('New chat from the menu opens an empty chat', async ({ page }) => {
    await openMenu(page)
    await page.getByTestId('chats-new').click()
    await expect(menu(page)).toHaveCount(0)
    await expect(page.getByTestId('chat-empty')).toBeVisible()
  })

  test('Clear all asks before it removes anything', async ({ page }) => {
    await openMenu(page)
    await menu(page).getByRole('button', { name: 'Clear all' }).click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('Delete all chats?')
    await dialog.getByRole('button', { name: 'Delete all' }).click()
    await expect.poll(async () => (await storedChats(page)).chats.length).toBe(1)
    await expect(page.getByTestId('chat-empty')).toBeVisible()
  })
})

test.describe('Chat conversations menu with one chat', () => {
  test('has no delete button and no Clear all', async ({ page }) => {
    await mockChat(page)
    await openChat(page, [chatOf('only', 'The only chat', 'qwen3-8b', pair('hi', 'hello'))])
    await openMenu(page)
    await expect(rows(page)).toHaveCount(1)
    await expect(rows(page).first().getByTitle('Delete chat')).toHaveCount(0)
    await expect(menu(page).getByRole('button', { name: 'Clear all' })).toHaveCount(0)
  })
})
