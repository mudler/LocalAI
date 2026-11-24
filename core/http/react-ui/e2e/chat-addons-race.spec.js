import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// An open chat reads its add-ons list again when what the server says about
// files changes. If the person switches an add-on while that second read is
// still out, its late answer is older than the switch and must not put the
// rows back (the switch shows its rows at once and takes the confirmed ones
// from its own response).

const DETAIL = { id: 'c1', title: 'Quotes', model: 'qwen3-8b', kind: 'chat', created: '2026-10-25T10:00:00Z', updated: '2026-10-25T10:00:00Z', running: false, last_turn: '', pending: [] }

test('a late add-ons list does not overwrite a switch made after it was asked', async ({ page }) => {
  await mockChat(page)
  await page.route('**/api/chats/c1/events', route => route.abort())
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: [{ seq: 1, turn: 1, role: 'user', text: 'compare the quotes', at: '2026-10-25T10:00:00Z' }] } }))
  await page.route('**/api/chats/c1', route => route.fulfill({ json: DETAIL }))
  await page.route('**/api/chats/c1/tools', route => route.fulfill({ json: { addons: [], servers: [], connectors: [], off: [], builtins: [] } }))

  let releaseFiles
  const filesGate = new Promise((r) => { releaseFiles = r })
  await page.route(/\/api\/chats(\?.*)?$/, async route => {
    // Files turn off a moment after the page opened: the add-ons are read again.
    await filesGate
    return route.fulfill({ json: { conversations: [{ ...DETAIL, pending: undefined }], persistent: true, files: false, files_reason: 'off' } })
  })

  let releaseLate
  const lateGate = new Promise((r) => { releaseLate = r })
  // Reads answer at once until files change; the one after that is held.
  let held = false
  let heldReads = 0
  await page.route('**/api/chats/c1/addons', async route => {
    if (route.request().method() === 'PUT') {
      const off = route.request().postDataJSON().off
      return route.fulfill({ json: { addons: [{ id: 'office', tools: 4, off: off.includes('office') }] } })
    }
    if (!held) return route.fulfill({ json: { addons: [{ id: 'office', tools: 4, off: false }] } })
    heldReads++
    await lateGate
    return route.fulfill({ json: { addons: [{ id: 'office', tools: 4, off: false }] } })
  })

  await page.goto('/app/chats/c1')
  await page.getByTestId('chat-tools-button').click()
  const office = page.getByRole('switch', { name: 'office' })
  await expect(office).toBeChecked()
  held = true
  releaseFiles()
  await expect.poll(() => heldReads).toBeGreaterThan(0)
  // The second read is out; the switch goes in before its answer.
  await office.click()
  await expect(office).not.toBeChecked()
  releaseLate()
  await page.waitForTimeout(600)
  await expect(office).not.toBeChecked()
})
