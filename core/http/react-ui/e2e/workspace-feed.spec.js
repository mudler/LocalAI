import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// The change feed of the files with the stream stubbed: a change that someone
// else made (a chat, another tab, another frontend) shows on the open Files
// page without a reload, through one shared stream that resumes after the last
// revision it saw.

test.describe('Files page and the change feed', () => {
  test('a change from elsewhere refreshes the open folder, and the stream resumes', async ({ page }) => {
    await mockChat(page)
    let lists = 0
    const feedUrls = []
    await page.route(/\/api\/chats\/files\/trash(\?.*)?$/, route => route.fulfill({ json: { entries: [] } }))
    await page.route(/\/api\/chats\/files(\?.*)?$/, route => {
      lists++
      const entries = [{ path: '/plan.txt', name: 'plan.txt', kind: 'file', size: 4, version: 1, rev: 3 }]
      if (lists > 1) entries.push({ path: '/from-chat.txt', name: 'from-chat.txt', kind: 'file', size: 9, version: 1, rev: 5 })
      return route.fulfill({ json: { path: '/', entries } })
    })
    await page.route('**/api/workspace/events*', route => {
      feedUrls.push(new URL(route.request().url()).search)
      // The first stream carries one change and ends, as a dropped connection
      // does; the client reconnects with the revision it saw.
      const body = feedUrls.length === 1
        ? 'id: 5\nevent: change\ndata: {"revision":5,"paths":["from-chat.txt"],"kinds":["agent"]}\n\n'
        : ': ping\n\n'
      return route.fulfill({ status: 200, headers: { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' }, body })
    })

    await page.goto('/app/files')
    const rows = page.getByTestId('ws-row')
    await expect(rows.filter({ hasText: 'plan.txt' })).toBeVisible()
    await expect(rows.filter({ hasText: 'from-chat.txt' })).toBeVisible()
    expect(feedUrls[0]).toBe('')
    await expect.poll(() => feedUrls.length, { timeout: 10_000 }).toBeGreaterThan(1)
    expect(feedUrls[1]).toBe('?after=5')
  })
})
