import { test, expect } from './coverage-fixtures.js'
import { mockConnectorThread } from './chats-connector-thread.js'

// A chat that a messaging connection started reads like the thread it came
// from: the header says where it lives, each message names who wrote it, the
// earlier messages of the thread sit in a shut block where the bot was first
// addressed, and the notes of the host show as small lines. The API is stubbed.

test.describe('A chat from a messaging thread', () => {
  test('the header names the connection, the channel and the people, and links to the thread', async ({ page }) => {
    await mockConnectorThread(page)
    await page.goto('/app/chats/cc1')
    const header = page.getByTestId('chat-origin-banner')
    await expect(header.getByTestId('connector-name')).toHaveText('Team workspace')
    await expect(header.getByTestId('connector-kind')).toHaveText('Slack')
    await expect(header.getByTestId('connector-where')).toHaveText('Channel ops-deploys')
    await expect(header.getByTestId('connector-thread')).toBeVisible()
    const link = header.getByTestId('connector-permalink')
    await expect(link).toHaveAttribute('href', /^https:\/\/acme\.example\.test\/archives\/C1\//)
    await expect(link).toHaveAttribute('rel', /noopener/)
    const people = header.getByTestId('connector-person')
    await expect(people).toHaveCount(2)
    await expect(people.nth(0)).toContainText('Edsger Dijkstra')
    await expect(people.nth(0).getByTestId('sender-standing')).toHaveText('Channel member')
    await expect(people.nth(1)).toContainText('Unknown sender')
    await expect(people.nth(1).getByTestId('sender-standing')).toHaveText('Anyone')
  })

  test('each message shows the sender, a badge and the time; a bare id is small print beside a label', async ({ page }) => {
    await mockConnectorThread(page)
    await page.goto('/app/chats/cc1')
    const senders = page.getByTestId('message-sender')
    await expect(senders).toHaveCount(3)
    await expect(senders.nth(0)).toContainText('Edsger Dijkstra')
    await expect(senders.nth(0)).toContainText('@edsger')
    await expect(senders.nth(0).getByTestId('sender-standing')).toHaveAttribute('data-standing', 'channel')
    await expect(senders.nth(0).locator('time')).toHaveAttribute('datetime', '2026-11-05T09:01:00.000Z')
    await expect(senders.nth(1).locator('.cx-sender__name')).toHaveText('Unknown sender')
    await expect(senders.nth(1).getByTestId('sender-small')).toHaveText('U09XYZ')
    await expect(senders.nth(1).getByTestId('sender-standing')).toHaveText('Anyone')
  })

  test('the earlier thread messages are shut, counted, open on a click and sit before the first message', async ({ page }) => {
    await mockConnectorThread(page)
    await page.goto('/app/chats/cc1')
    const block = page.getByTestId('thread-context')
    await expect(page.getByTestId('thread-context-toggle')).toHaveText('Earlier in the thread (3 messages)')
    await expect(page.getByTestId('thread-context-toggle')).toHaveAttribute('aria-expanded', 'false')
    await expect(page.getByTestId('thread-context-lines')).toBeHidden()
    // It comes before the message that addressed the bot.
    const order = await page.getByTestId('chat-thread').evaluate((el) => [...el.children].map((c) => c.getAttribute('data-testid')).filter(Boolean))
    expect(order.indexOf('thread-context')).toBeLessThan(order.indexOf('chat-message'))
    await page.getByTestId('thread-context-toggle').click()
    const lines = block.locator('.cx-thread-ctx__line')
    await expect(lines).toHaveCount(3)
    await expect(lines.nth(0)).toContainText('Ada Lovelace')
    await expect(lines.nth(0)).toContainText('the nightly deploy failed again')
    await expect(lines.nth(2)).toContainText('Assistant (you)')
    await expect(block.getByTestId('thread-context-cut')).toHaveText('Older messages of the thread are not shown.')
    await page.getByTestId('thread-context-toggle').click()
    await expect(page.getByTestId('thread-context-lines')).toBeHidden()
  })

  test('what the host did shows as small status lines, folded when equal, with the reason', async ({ page }) => {
    await mockConnectorThread(page)
    await page.goto('/app/chats/cc1')
    const notes = page.getByTestId('host-note')
    await expect(notes).toHaveCount(2)
    await expect(notes.nth(0)).toHaveAttribute('data-code', 'held_back')
    await expect(notes.nth(0)).toContainText('Some tools were not offered to this sender')
    await notes.nth(0).locator('summary').click()
    await expect(notes.nth(0).getByTestId('host-note-text')).toHaveText('The owner has not enabled reminders for this bot.')
    await expect(notes.nth(1)).toHaveAttribute('data-code', 'unsupported')
    await expect(notes.nth(1)).toContainText('Message not read')
    await expect(notes.nth(1).getByTestId('host-note-text')).toHaveText('I can only read text and files.')
    await expect(notes.nth(1)).toContainText('×2')
  })

  test('an answer shows once, as one row', async ({ page }) => {
    await mockConnectorThread(page)
    await page.goto('/app/chats/cc1')
    await expect(page.getByTestId('chat-message').filter({ hasText: 'I retried the migration' })).toHaveCount(1)
  })

  test('a chat stored before senders and blocks existed still opens, with the plain banner row and nothing invented', async ({ page }) => {
    await mockConnectorThread(page)
    await page.goto('/app/chats/cc0')
    await expect(page.getByTestId('chat-origin-banner').getByTestId('connector-name')).toHaveText('Team workspace')
    await expect(page.getByTestId('connector-where')).toHaveText('Channel or group')
    await expect(page.getByTestId('connector-permalink')).toHaveCount(0)
    await expect(page.getByTestId('connector-people')).toHaveCount(0)
    await expect(page.getByTestId('message-sender')).toHaveCount(0)
    await expect(page.getByTestId('thread-context')).toHaveCount(0)
    await expect(page.getByTestId('chat-message')).toHaveCount(2)
  })

  test('a link that is not https is not shown', async ({ page }) => {
    await mockConnectorThread(page, { chats: [{ id: 'cc1', title: 'x', model: 'qwen3-8b', kind: 'chat', origin: 'connector', origin_id: 'conn1', created: '2026-11-05T09:00:00Z', updated: '2026-11-05T09:00:00Z', place: { link: 'javascript:alert(1)' } }], messages: { cc1: [] } })
    await page.goto('/app/chats/cc1')
    await expect(page.getByTestId('chat-origin-banner')).toBeVisible()
    await expect(page.getByTestId('connector-permalink')).toHaveCount(0)
  })

  for (const width of [320, 375, 1280]) {
    test(`the page does not scroll sideways at ${width}px`, async ({ page }) => {
      await page.setViewportSize({ width, height: 800 })
      await mockConnectorThread(page)
      await page.goto('/app/chats/cc1')
      await page.getByTestId('thread-context-toggle').click()
      await expect(page.getByTestId('thread-context-lines')).toBeVisible()
      const wide = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
      expect(wide).toBeLessThanOrEqual(0)
      const header = await page.getByTestId('chat-origin-banner').evaluate((el) => el.scrollWidth - el.clientWidth)
      expect(header).toBeLessThanOrEqual(0)
    })
  }
})
