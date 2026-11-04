import { test, expect } from './coverage-fixtures.js'

// Home is the front door: the sidebar has no Chats entry, it lists the recent
// chats with New chat and All chats, and All chats holds the history. Against
// the real test server; the chats are made through the API, with a title that
// no other spec uses.

async function makeChat(request, title) {
  const r = await request.post('/api/chats', { data: { model: 'mock-model' } })
  expect(r.ok()).toBeTruthy()
  const { id } = await r.json()
  expect((await request.patch(`/api/chats/${id}`, { data: { title } })).ok()).toBeTruthy()
  return id
}

const unique = (word) => `${word} ${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`

test.describe('Recent chats in the sidebar', () => {
  test('a new chat is listed, a row opens its transcript and New chat opens Home', async ({ page, request }) => {
    const title = unique('Sidebar recent')
    const id = await makeChat(request, title)
    await page.goto('/app')
    const recent = page.getByTestId('sidebar-chats')
    const row = recent.getByTestId('sidebar-chat').filter({ hasText: title })
    await expect(row).toBeVisible()
    await row.click()
    await expect(page).toHaveURL(new RegExp(`/app/chats/${id}$`))
    await expect(row).toHaveAttribute('aria-current', 'page')
    await recent.getByRole('button', { name: 'New chat' }).click()
    await expect(page).toHaveURL(/\/app$/)
    await expect(page.locator('.home-page')).toBeVisible()
  })

  test('the collapsed rail hides the rows and keeps the All chats icon', async ({ page, request }) => {
    await makeChat(request, unique('Rail'))
    await page.addInitScript(() => localStorage.setItem('localai_sidebar_collapsed', 'true'))
    await page.goto('/app')
    const all = page.getByTestId('sidebar-all-chats')
    await expect(all).toBeVisible()
    await expect(all).toHaveAttribute('title', 'All chats')
    await expect(page.getByTestId('sidebar-chat').first()).toBeHidden()
    await all.click()
    await expect(page).toHaveURL(/\/app\/chats\/all$/)
  })

  test('the phone drawer lists the recent chats and closes on a click', async ({ page, request }) => {
    const title = unique('Drawer')
    await makeChat(request, title)
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/app')
    await page.getByRole('button', { name: 'Open menu' }).click()
    const row = page.getByTestId('sidebar-chat').filter({ hasText: title })
    await expect(row).toBeVisible()
    await row.click()
    await expect(page).toHaveURL(/\/app\/chats\/[^/]+$/)
    await expect(page.locator('.sidebar.open')).toHaveCount(0)
  })
})

test.describe('All chats', () => {
  test('Home links to it, search finds a chat by name and a row opens it', async ({ page, request }) => {
    const title = unique('Findable')
    const id = await makeChat(request, title)
    await page.goto('/app')
    await page.getByTestId('home-all-chats').first().click()
    await expect(page).toHaveURL(/\/app\/chats\/all$/)
    await expect(page.getByRole('heading', { name: 'All chats', level: 1 })).toBeVisible()
    const list = page.getByTestId('all-chats-list')
    await list.getByRole('combobox', { name: 'Search conversations...' }).fill(title)
    await expect(list.getByTestId('chats-row')).toHaveCount(1)
    await list.getByTestId('chats-row').click()
    await expect(page).toHaveURL(new RegExp(`/app/chats/${id}$`))
  })

  test('a delete hides the row and offers Undo; the server deletes it when the offer ends', async ({ page, request }) => {
    const title = unique('Delete me')
    const id = await makeChat(request, title)
    await page.goto('/app/chats/all')
    const row = page.getByTestId('all-chats-list').getByTestId('chats-row').filter({ hasText: title })
    await row.hover()
    await row.getByRole('button', { name: 'Delete chat' }).click()
    await expect(row).toHaveCount(0)
    // The sidebar leaves it out too while the offer runs.
    await expect(page.getByTestId('sidebar-chat').filter({ hasText: title })).toHaveCount(0)
    const toast = page.getByTestId('chat-undo-toast')
    await expect(toast).toContainText(title)
    await toast.getByRole('button', { name: 'Undo' }).click()
    await expect(row).toBeVisible()
    expect((await request.get(`/api/chats/${id}`)).status()).toBe(200)

    await row.hover()
    await row.getByRole('button', { name: 'Delete chat' }).click()
    await page.getByTestId('chat-undo-toast').getByRole('button', { name: 'Dismiss' }).click()
    await expect.poll(async () => (await request.get(`/api/chats/${id}`)).status()).toBe(404)
  })

  test('rename from the list changes the title on the server', async ({ page, request }) => {
    const title = unique('Old name')
    const id = await makeChat(request, title)
    await page.goto('/app/chats/all')
    const row = page.getByTestId('all-chats-list').getByTestId('chats-row').filter({ hasText: title })
    await row.hover()
    await row.getByRole('button', { name: 'Rename' }).click()
    const box = page.getByTestId('all-chats-list').getByRole('textbox', { name: 'Rename' })
    await box.fill(`${title} renamed`)
    await box.press('Enter')
    await expect.poll(async () => (await (await request.get(`/api/chats/${id}`)).json()).title).toBe(`${title} renamed`)
  })
})

test('/app/chats as an address opens Home', async ({ page }) => {
  await page.goto('/app/chats')
  await expect(page).toHaveURL(/\/app$/)
  await expect(page.locator('.home-page')).toBeVisible()
})
