import { test, expect } from './coverage-fixtures.js'
import { chatOf, mockChat, openChat, controlStream } from './chat-fixtures.js'

async function start(page) {
  await mockChat(page)
  await controlStream(page)
  await openChat(page, [chatOf('c1', 'Throughput', 'qwen3-8b', [])])
  // Only Date is fixed: browser timers and React continue running normally.
  await page.clock.setFixedTime(new Date('2026-10-26T12:00:00Z'))
  await page.getByTestId('chat-input').fill('hello')
  await page.getByTestId('chat-send').click()
  await expect.poll(() => page.evaluate(() => window.__sse.calls)).toBe(1)
}

async function atTwoSeconds(page, usage) {
  await page.clock.setFixedTime(new Date('2026-10-26T12:00:02Z'))
  await page.evaluate((usage) => {
    window.__sse.push({ choices: [{ delta: { reasoning: 'thinking about the answer' } }] })
    window.__sse.push({ choices: [{ delta: { content: 'The answer has several tokens.' } }] })
    if (usage) window.__sse.push({ choices: [], usage })
    window.__sse.end()
  }, usage)
}

test('chat throughput uses completion usage, including reasoning, over request duration', async ({ page }) => {
  await start(page)
  await atTwoSeconds(page, { prompt_tokens: 10, completion_tokens: 100, total_tokens: 110,
    completion_tokens_details: { reasoning_tokens: 80 } })
  await expect(page.getByTestId('chat-foot')).toContainText('50 tok/s · end-to-end')
  await expect(page.getByTestId('chat-foot')).not.toContainText('estimated')
  await expect(page.getByTestId('chat-foot')).not.toContainText('Peak')
})

test('chat throughput labels live and missing-usage rates as estimates', async ({ page }) => {
  await start(page)
  await page.clock.setFixedTime(new Date('2026-10-26T12:00:01Z'))
  await page.evaluate(() => window.__sse.push({ choices: [{ delta: { content: '12345678' } }] }))
  await expect(page.getByTestId('chat-foot')).toContainText('2 tok/s · estimated')
  await page.clock.setFixedTime(new Date('2026-10-26T12:00:02Z'))
  await page.evaluate(() => window.__sse.end())
  await expect(page.getByTestId('chat-foot')).toContainText('1 tok/s · estimated')
})

test('chat throughput accepts a reported zero completion count', async ({ page }) => {
  await start(page)
  await atTwoSeconds(page, { prompt_tokens: 10, completion_tokens: 0, total_tokens: 10 })
  await expect(page.getByTestId('chat-foot')).toContainText('0 tok/s · end-to-end')
})
