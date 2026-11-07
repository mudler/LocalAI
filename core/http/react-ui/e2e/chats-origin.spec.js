import { test, expect } from './coverage-fixtures.js'
import { mockOriginChats } from './chats-origin.js'

// Chats that a trigger, a connector, a job or a page started stay out of the
// lists of a person's own chats and have a tab of their own in All chats. The
// API is stubbed with a list that honours the origin parameter.

test.describe('Chats that no person started', () => {
  test('the sidebar and All chats list only the chats a person started by default', async ({ page }) => {
    const { lists } = await mockOriginChats(page)
    await page.goto('/app/chats/all')
    const list = page.getByTestId('all-chats-list')
    await expect(list.getByTestId('chats-row')).toHaveCount(2)
    await expect(list.getByText('Chat App · alice')).toHaveCount(0)
    const recent = page.getByTestId('sidebar-chats').getByTestId('sidebar-chat')
    await expect(recent).toHaveCount(2)
    await expect(page.getByTestId('sidebar-chats').getByText('Chat App · alice')).toHaveCount(0)
    expect(lists).toContain('person')
    expect(lists).toContain('automated')
  })

  test('the second tab groups them by kind and by origin, with counts, status and badges', async ({ page }) => {
    await mockOriginChats(page)
    await page.goto('/app/chats/all')
    await expect(page.getByTestId('all-chats-count-mine')).toHaveText('2')
    await expect(page.getByTestId('all-chats-count-automated')).toHaveText('6')
    await page.getByTestId('all-chats-tab-automated').click()
    const panel = page.getByTestId('automated-chats')
    await expect(panel).toBeVisible()
    // Kinds keep a fixed order and the unknown one comes last.
    await expect(panel.locator('.ac-kind__title')).toHaveText([/Triggers/, /Connectors/, /Jobs/, /Other/])
    const connector = panel.getByTestId('automated-kind-connector')
    const groups = connector.getByTestId('automated-group')
    await expect(groups).toHaveCount(2)
    // Titles come from the binding's name, then from the title of its kind.
    await expect(groups.nth(0).getByTestId('automated-group-title')).toHaveText('Team assistant')
    await expect(groups.nth(1).getByTestId('automated-group-title')).toHaveText('Chat App')
    await expect(groups.nth(0).getByTestId('automated-count')).toHaveText('Chats: 2')
    await expect(groups.nth(0).getByTestId('automated-origin-badge')).toHaveText('Connector')
    await expect(groups.nth(0).getByTestId('automated-running')).toBeVisible()
    await expect(groups.nth(0).getByTestId('automated-waiting')).toBeVisible()
    await expect(groups.nth(1).getByTestId('automated-waiting')).toHaveCount(0)
    await expect(panel.getByTestId('automated-kind-trigger').getByTestId('automated-group-title')).toHaveText('Morning digest')
    await expect(panel.getByTestId('automated-kind-future').getByTestId('automated-origin-badge')).toHaveText('Automated')
    // The origin links to its page.
    await expect(groups.nth(0).getByTestId('automated-origin-link')).toHaveAttribute('href', '/app/addons/chatapp/bot')
    await expect(panel.getByTestId('automated-kind-trigger').getByTestId('automated-origin-link')).toHaveAttribute('href', '/app/triggers/trig1')
    // A chat of the group opens as usual.
    await groups.nth(0).getByTestId('automated-chat').filter({ hasText: 'Chat App · bob' }).locator('.ac-row__open').click()
    await expect(page).toHaveURL(/\/app\/chats\/b2$/)
  })

  test('the tab that was open is remembered in this browser', async ({ page }) => {
    await mockOriginChats(page)
    await page.goto('/app/chats/all')
    await page.getByTestId('all-chats-tab-automated').click()
    await page.reload()
    await expect(page.getByTestId('automated-chats')).toBeVisible()
    await expect(page.getByTestId('all-chats-tab-automated')).toHaveAttribute('aria-selected', 'true')
    await page.getByTestId('all-chats-tab-mine').click()
    await page.reload()
    await expect(page.getByTestId('all-chats-list')).toBeVisible()
  })

  test('a browser without storage still shows the first tab', async ({ page }) => {
    await mockOriginChats(page)
    await page.addInitScript(() => {
      // Only the key of the tab throws, as it would in a window without site data.
      const get = Storage.prototype.getItem
      const set = Storage.prototype.setItem
      Storage.prototype.getItem = function (k) { if (k === 'localai_all_chats_tab') throw new Error('denied'); return get.call(this, k) }
      Storage.prototype.setItem = function (k, v) { if (k === 'localai_all_chats_tab') throw new Error('denied'); return set.call(this, k, v) }
    })
    await page.goto('/app/chats/all')
    await expect(page.getByTestId('all-chats-list')).toBeVisible()
    await page.getByTestId('all-chats-tab-automated').click()
    await expect(page.getByTestId('automated-chats')).toBeVisible()
  })

  test('without such chats there are no tabs', async ({ page }) => {
    await mockOriginChats(page, { automated: [] })
    await page.goto('/app/chats/all')
    await expect(page.getByTestId('all-chats-list')).toBeVisible()
    await expect(page.getByTestId('all-chats-tabs')).toHaveCount(0)
    await expect(page.getByTestId('sidebar-automated-badge')).toHaveCount(0)
  })

  test('the All chats entry shows how many of them wait for the person', async ({ page }) => {
    await mockOriginChats(page, {
      notifications: [{ id: 1, kind: 'needs_attention', status: 'ok', title: 'x', body: '', link: '/app/chats/t1', read: false, created: new Date().toISOString() }],
    })
    await page.goto('/app')
    // b2 paused at its step limit, t1 has an unread item that asks for the person.
    const badge = page.getByTestId('sidebar-automated-badge')
    await expect(badge).toHaveText('2')
    await expect(badge).toHaveAttribute('aria-label', 'Automated chats waiting for you: 2')
    await page.getByTestId('sidebar-all-chats').click()
    await expect(page.getByTestId('all-chats-waiting')).toHaveText('2')
  })

  test('only a running turn shows a quiet dot instead of a number', async ({ page }) => {
    await mockOriginChats(page, { automated: [{ id: 'b1', title: 'Chat App · alice', model: 'qwen3-8b', kind: 'chat', origin: 'connector', origin_id: 'bind1', created: new Date().toISOString(), updated: new Date().toISOString(), running: true }] })
    await page.goto('/app')
    await expect(page.getByTestId('sidebar-automated-running')).toBeVisible()
    await expect(page.getByTestId('sidebar-automated-badge')).toHaveCount(0)
  })

  test('Home does not offer them under Jump back in', async ({ page }) => {
    const { lists } = await mockOriginChats(page)
    await page.goto('/app')
    await expect(page.getByTestId('home-conversation')).toHaveCount(1)
    await expect(page.getByTestId('home-conversation').first()).toContainText('Plan the week')
    expect(lists).toContain('person')
  })

  test('Ctrl+K in a transcript finds every chat and marks the ones a person did not start', async ({ page }) => {
    await mockOriginChats(page)
    await page.goto('/app/chats/p1')
    await page.getByTestId('chats-trigger').click()
    const menu = page.getByTestId('chats-menu')
    await expect(menu.getByTestId('chats-row')).toHaveCount(8)
    await menu.getByRole('combobox', { name: 'Search conversations...' }).fill('alice')
    const row = menu.getByTestId('chats-row')
    await expect(row).toHaveCount(1)
    await expect(row.getByTestId('chats-origin-badge')).toHaveText('Team assistant')
    await menu.getByRole('combobox', { name: 'Search conversations...' }).fill('Plan')
    await expect(menu.getByTestId('chats-row').getByTestId('chats-origin-badge')).toHaveCount(0)
  })

  test('a chat a connector started opens with a banner that names the origin and links to it', async ({ page }) => {
    await mockOriginChats(page)
    await page.goto('/app/chats/b1')
    const banner = page.getByTestId('chat-origin-banner')
    // A connector chat has a header of its own: the connection, then where it lives.
    await expect(banner.getByTestId('connector-name')).toHaveText('Team assistant')
    await expect(banner.getByTestId('connector-kind')).toHaveText('Chat App')
    await expect(page.getByTestId('chat-origin-link')).toHaveAttribute('href', '/app/addons/chatapp/bot')
    await page.goto('/app/chats/f1')
    await expect(page.getByTestId('chat-origin-banner')).toContainText('started by something else')
    await expect(page.getByTestId('chat-origin-link')).toHaveCount(0)
    await page.goto('/app/chats/p1')
    await expect(page.getByTestId('chat-input')).toBeVisible()
    await expect(page.getByTestId('chat-origin-banner')).toHaveCount(0)
  })

  test.describe('on a phone', () => {
    test.use({ viewport: { width: 390, height: 844 } })

    test('the tabs and the groups fit the width', async ({ page }) => {
      await mockOriginChats(page)
      await page.goto('/app/chats/all')
      await page.getByTestId('all-chats-tab-automated').click()
      await expect(page.getByTestId('automated-chats')).toBeVisible()
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
      expect(overflow).toBeLessThanOrEqual(0)
      const box = await page.getByTestId('all-chats-tabs').boundingBox()
      expect(box.x + box.width).toBeLessThanOrEqual(390)
    })
  })
})
