import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// The skills picker of the composer with the API stubbed: one switch per
// skill, "Select all" and "Disable all" as one request, a search from nine
// skills on, and a refused change that goes back with a message and no jump.

function makeSkills(n, off = []) {
  return Array.from({ length: n }, (_, i) => {
    const name = `skill-${String(i + 1).padStart(2, '0')}`
    return {
      id: `personal:${name}`, name, layer: 'personal', path: `/s/${name}`, editable: true,
      description: i % 2 ? `Does the ${name} job with a long description that cannot fit on one line of the menu` : `Short ${name}`,
      enabled: !off.includes(`personal:${name}`),
    }
  })
}

// Stubs one chat and its skills. `puts` records the body of every PUT; with
// `fail` set the server refuses them.
async function stub(page, { count = 20, off = [] } = {}) {
  const state = { off: [...off], puts: [], fail: false, holdMs: 0 }
  await mockChat(page)
  await page.route('**/api/chats/c1/events', route => route.abort())
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: [{ seq: 1, turn: 1, role: 'user', text: 'hello', at: '2026-10-25T10:00:00Z' }] } }))
  await page.route(/\/api\/chats\/skills(\?.*)?$/, route => route.fulfill({ json: { skills: makeSkills(count, state.off) } }))
  await page.route('**/api/chats/c1/skills', async route => {
    const body = route.request().postDataJSON()
    state.puts.push(body)
    if (state.holdMs) await new Promise(r => setTimeout(r, state.holdMs))
    if (state.fail) return route.fulfill({ status: 500, json: { error: { message: 'disk full' } } })
    state.off = body.off
    return route.fulfill({ json: { off: body.off } })
  })
  await page.route('**/api/chats/c1', route => route.fulfill({ json: {
    id: 'c1', title: 'Quotes', model: 'qwen3-8b', kind: 'chat', created: '2026-10-25T10:00:00Z', updated: '2026-10-25T10:00:00Z', running: false, last_turn: '', pending: [],
  } }))
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({ json: { conversations: [{ id: 'c1', title: 'Quotes', model: 'qwen3-8b', kind: 'chat', created: '2026-10-25T10:00:00Z', updated: '2026-10-25T10:00:00Z', running: false, last_turn: '' }], persistent: true, files: true, files_reason: '' } }))
  return state
}

const open = async (page) => {
  await page.goto('/app/chats/c1')
  await page.getByTestId('chat-skills-button').click()
  const menu = page.getByTestId('chat-skills-menu')
  await expect(menu).toBeVisible()
  // Let the opening animation end, so a position read is a position at rest.
  await menu.evaluate(el => Promise.all(el.getAnimations().map(a => a.finished)))
}
const summary = (page) => page.getByTestId('chat-skills-summary')
const sw = (page, name) => page.getByRole('switch', { name, exact: true })

test.describe('Chat skills picker', () => {
  test('shows a switch per skill with its description and the count', async ({ page }) => {
    await stub(page, { off: ['personal:skill-02'] })
    await open(page)
    await expect(summary(page)).toHaveText('19 of 20 on')
    await expect(page.getByTestId('chat-skills-count')).toHaveText('19')
    await expect(page.getByRole('switch')).toHaveCount(20)
    await expect(sw(page, 'skill-01')).toBeChecked()
    await expect(sw(page, 'skill-02')).not.toBeChecked()
    // The full description is the tooltip; the row stays one line of 36 px.
    const row = page.getByTestId('chat-skills-item').nth(1)
    await expect(row.locator('label')).toHaveAttribute('title', /skill-02: Does the skill-02 job with a long description/)
    expect((await row.boundingBox()).height).toBe(36)
    await expect(page.getByText('Skills you add later start on.')).toBeVisible()
  })

  test('the Manage skills link opens the Skills page', async ({ page }) => {
    await stub(page)
    await open(page)
    const link = page.getByTestId('chat-skills-manage')
    await expect(link).toHaveText('Manage skills')
    await expect(link).toHaveAttribute('href', /\/app\/skills$/)
    await link.click()
    await expect(page).toHaveURL(/\/app\/skills$/)
    await expect(page.getByTestId('skills-page')).toBeVisible()
  })

  test('Disable all and Select all send one request each', async ({ page }) => {
    const api = await stub(page)
    await open(page)
    await page.getByTestId('chat-skills-none').click()
    await expect(summary(page)).toHaveText('0 of 20 on')
    await expect(page.getByRole('switch', { checked: true })).toHaveCount(0)
    await expect(page.getByTestId('chat-skills-none')).toBeDisabled()
    await expect.poll(() => api.puts.length).toBe(1)
    expect(api.puts[0].off).toHaveLength(20)
    expect(api.puts[0].off[0]).toBe('personal:skill-01')
    await expect(page.getByTestId('chat-skills-count')).toHaveCount(0)

    await page.getByTestId('chat-skills-all').click()
    await expect(summary(page)).toHaveText('20 of 20 on')
    await expect(page.getByTestId('chat-skills-all')).toBeDisabled()
    await expect.poll(() => api.puts.length).toBe(2)
    expect(api.puts[1]).toEqual({ off: [] })
  })

  test('one switch sends the list of the skills that are off', async ({ page }) => {
    const api = await stub(page, { off: ['personal:skill-05'] })
    await open(page)
    await sw(page, 'skill-03').click()
    await expect(sw(page, 'skill-03')).not.toBeChecked()
    await expect(summary(page)).toHaveText('18 of 20 on')
    await expect.poll(() => api.puts.at(-1)).toEqual({ off: ['personal:skill-03', 'personal:skill-05'] })
    await sw(page, 'skill-05').click()
    await expect.poll(() => api.puts.at(-1)).toEqual({ off: ['personal:skill-03'] })
  })

  test('Space on a focused switch toggles it', async ({ page }) => {
    const api = await stub(page)
    await open(page)
    await sw(page, 'skill-01').focus()
    await page.keyboard.press('Space')
    await expect(sw(page, 'skill-01')).toHaveAttribute('aria-checked', 'false')
    await expect.poll(() => api.puts.at(-1)).toEqual({ off: ['personal:skill-01'] })
    await page.keyboard.press('Space')
    await expect(sw(page, 'skill-01')).toHaveAttribute('aria-checked', 'true')
  })

  test('a click on the name toggles the switch too', async ({ page }) => {
    await stub(page)
    await open(page)
    await page.getByTestId('chat-skills-item').nth(2).locator('.swl-row__name').click()
    await expect(sw(page, 'skill-03')).not.toBeChecked()
  })

  test('the search narrows the list, and the actions work on what is shown', async ({ page }) => {
    const api = await stub(page)
    await open(page)
    const search = page.getByTestId('chat-skills-search')
    await search.fill('skill-1')
    await expect(page.getByRole('switch')).toHaveCount(10)
    await expect(summary(page)).toHaveText('20 of 20 on')
    await expect(page.getByTestId('chat-skills-none')).toHaveText('Disable shown')
    await page.getByTestId('chat-skills-none').click()
    await expect.poll(() => api.puts.at(-1).off.length).toBe(10)
    expect(api.puts.at(-1).off.every(id => /^personal:skill-1\d$|^personal:skill-20$/.test(id) || id === 'personal:skill-10')).toBe(true)
    await expect(summary(page)).toHaveText('10 of 20 on')
    await search.fill('does the skill-04 job')
    await expect(page.getByRole('switch')).toHaveCount(1)
    await search.fill('zzz')
    await expect(page.getByText('No skill matches “zzz”.')).toBeVisible()
    await search.fill('')
    await expect(page.getByRole('switch')).toHaveCount(20)
    await expect(page.getByTestId('chat-skills-none')).toHaveText('Disable all')
  })

  test('a short list has no search field', async ({ page }) => {
    await stub(page, { count: 8 })
    await open(page)
    await expect(page.getByRole('switch')).toHaveCount(8)
    await expect(page.getByTestId('chat-skills-search')).toHaveCount(0)
  })

  test('a refused switch goes back and says so without moving the rows', async ({ page }) => {
    const api = await stub(page)
    api.fail = true
    await open(page)
    const before = await sw(page, 'skill-10').boundingBox()
    const listBefore = await page.getByTestId('chat-skills-list').boundingBox()
    await sw(page, 'skill-04').click()
    await expect(page.getByTestId('chat-skills-error')).toContainText('Could not save. Nothing changed.')
    await expect(sw(page, 'skill-04')).toBeChecked()
    await expect(summary(page)).toHaveText('20 of 20 on')
    expect(await sw(page, 'skill-10').boundingBox()).toEqual(before)
    expect(await page.getByTestId('chat-skills-list').boundingBox()).toEqual(listBefore)
    // The next change that works clears the message.
    api.fail = false
    await sw(page, 'skill-04').click()
    await expect(sw(page, 'skill-04')).not.toBeChecked()
    await expect(page.getByTestId('chat-skills-error')).toHaveText('')
  })

  test('the menu keeps its own height when the shared menu sheet loads after it', async ({ page }) => {
    // The base rule of every composer menu caps it at 320px. The sheet of the
    // picker is a lazy chunk, so the base rule can land last; the picker must
    // still get its full height, or the list is a few rows and a click on a
    // row at its edge scrolls the list.
    await page.addInitScript(() => {
      const late = () => {
        const style = document.createElement('style')
        style.textContent = '.chat-mcp-dropdown-menu { max-height: 320px; overflow-y: auto; min-width: 240px; }'
        document.head.appendChild(style)
      }
      new MutationObserver(() => {
        if (!window.__lateSheet && document.querySelector('[data-testid=chat-skills-menu]')) { window.__lateSheet = true; late() }
      }).observe(document, { childList: true, subtree: true })
      window.addEventListener('DOMContentLoaded', late)
    })
    const api = await stub(page)
    api.fail = true
    await open(page)
    const menu = await page.getByTestId('chat-skills-menu').boundingBox()
    expect(menu.height).toBeGreaterThan(400)
    const before = await sw(page, 'skill-10').boundingBox()
    await sw(page, 'skill-04').click()
    await expect(page.getByTestId('chat-skills-error')).toContainText('Could not save. Nothing changed.')
    expect(await sw(page, 'skill-10').boundingBox()).toEqual(before)
  })

  test('a refused Disable all puts every skill back', async ({ page }) => {
    const api = await stub(page, { off: ['personal:skill-02'] })
    api.fail = true
    await open(page)
    await page.getByTestId('chat-skills-none').click()
    await expect(page.getByTestId('chat-skills-error')).toContainText('Could not save')
    await expect(summary(page)).toHaveText('19 of 20 on')
    await expect(sw(page, 'skill-02')).not.toBeChecked()
    await expect(sw(page, 'skill-01')).toBeChecked()
  })

  test('the picker fits a phone and Escape closes it', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await stub(page)
    await open(page)
    const box = await page.getByTestId('chat-skills-menu').boundingBox()
    expect(box.x).toBeGreaterThanOrEqual(0)
    expect(box.y).toBeGreaterThanOrEqual(0)
    expect(box.y + box.height).toBeLessThanOrEqual(844)
    expect(box.x + box.width).toBeLessThanOrEqual(390)
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
    await page.keyboard.press('Escape')
    await expect(page.getByTestId('chat-skills-menu')).toHaveCount(0)
    await expect(page.getByTestId('chat-skills-button')).toBeFocused()
  })
})

test.describe('Chat skills picker with add-on skills', () => {
  const shot = async (page, name) => { if (process.env.HOST_EXEC_SHOT_DIR) await page.screenshot({ path: `${process.env.HOST_EXEC_SHOT_DIR}/${name}.png` }) }
  async function stubAddon(page, skills) {
    const state = { puts: [], lists: 0 }
    await mockChat(page)
    await page.route('**/api/chats/c1/events', route => route.abort())
    await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: [] } }))
    await page.route(/\/api\/chats\/skills(\?.*)?$/, route => { state.lists++; return route.fulfill({ json: { skills: skills(state) } }) })
    await page.route('**/api/chats/c1/skills', route => {
      const body = route.request().postDataJSON()
      state.puts.push(body)
      return route.fulfill({ json: { off: body.off } })
    })
    await page.route('**/api/chats/c1', route => route.fulfill({ json: {
      id: 'c1', title: 'Quotes', model: 'qwen3-8b', kind: 'chat', created: '2026-10-25T10:00:00Z', updated: '2026-10-25T10:00:00Z', running: false, last_turn: '', pending: [],
    } }))
    await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({ json: { conversations: [{ id: 'c1', title: 'Quotes', model: 'qwen3-8b', kind: 'chat', created: '2026-10-25T10:00:00Z', updated: '2026-10-25T10:00:00Z', running: false, last_turn: '' }], persistent: true, files: true, files_reason: '' } }))
    return state
  }
  const office = { id: 'addon:xlsx', name: 'xlsx', description: 'Read and edit spreadsheets with the Office tools', layer: 'addon', addon: 'office', addon_title: 'Office', enabled: true }
  const mine = { id: 'personal:review', name: 'review', description: 'Check a change', layer: 'personal', enabled: true }

  test('labels document skills with their provider, with no removed built-in', async ({ page }) => {
    await stubAddon(page, () => [office, mine])
    await open(page)
    const sources = page.getByTestId('chat-skills-source')
    await expect(sources).toHaveText(['Office', 'Personal'])
    await expect(page.getByRole('switch')).toHaveCount(2)
    const replaced = page.getByTestId('chat-skills-replaced')
    await expect(replaced).toHaveCount(0)
    await expect(page.getByTestId('chat-skills-fallback')).toHaveCount(0)
    await expect(summary(page)).toHaveText('2 of 2 on')
    await shot(page, 'skills-picker-addon')
  })

  test('switching a document skill off persists OFF without a core fallback', async ({ page }) => {
    const state = await stubAddon(page, s => [{ ...office, enabled: !s.puts.at(-1)?.off.includes('addon:xlsx') }, mine])
    await open(page)
    await sw(page, 'xlsx').click()
    await expect.poll(() => state.puts).toEqual([{ off: ['addon:xlsx'] }])
    await expect(sw(page, 'xlsx')).not.toBeChecked()
    await expect(page.getByTestId('chat-skills-fallback')).toHaveCount(0)
    await expect(page.getByTestId('chat-skills-replaced')).toHaveCount(0)
    await expect(page.locator('[data-testid="chat-skills-source"][data-kind="builtin"]')).toHaveCount(0)
  })

  for (const addon of ['office', 'sandbox']) {
    test(`${addon} supplies familiar document names without a builtin`, async ({ page }) => {
      await stubAddon(page, () => ['docx', 'pptx', 'pdf', 'xlsx'].map(name => ({
        ...office, id: `addon:${name}`, name, addon, addon_title: addon === 'office' ? 'Office' : 'Sandbox',
      })))
      await open(page)
      await expect(page.getByRole('switch')).toHaveCount(4)
      await expect(page.getByTestId('chat-skills-source')).toHaveText(Array(4).fill(addon === 'office' ? 'Office' : 'Sandbox'))
      await expect(page.getByTestId('chat-skills-fallback')).toHaveCount(0)
      await expect(page.getByTestId('chat-skills-replaced')).toHaveCount(0)
    })
  }

  test('an unavailable pinned provider explains why without suggesting a fallback', async ({ page }) => {
    await stubAddon(page, () => [{ ...office, unavailable: 'Pinned provider is not available for this chat.' }, mine])
    await open(page)
    await expect(page.getByTestId('chat-skills-item').first()).toContainText('Pinned provider is not available for this chat.')
    await expect(page.getByTestId('chat-skills-fallback')).toHaveCount(0)
  })
})
