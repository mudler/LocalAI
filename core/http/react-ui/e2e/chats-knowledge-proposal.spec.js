import { test, expect } from './coverage-fixtures.js'
import { makeInbox, stubInbox } from './inbox-fixtures.js'

// A text that a model proposed for a collection of the Knowledge, in the list
// of suggested changes. It names the collection instead of a file, shows the
// first lines and the size, is confirmed and undone like any other change, and
// never reads a file of the home. The API is stubbed; that the server never
// applies it unasked is tested in Go.

const AT = '2026-11-05T09:00:00Z'
const later = () => new Date(Date.now() + 5 * 3600_000).toISOString()
const TEXT = Array.from({ length: 24 }, (_, i) => `Step ${i + 1}: hold the reset button, wait for the light.`).join('\n') + '\n'
const make = (extra = {}) => ({
  v: 1, id: 'k1', owner: 'alice', kind: 'knowledge', scope: 'user', target: 'router-manuals', summary: 'Router reset steps', content: TEXT,
  source: { kind: 'connector', id: 'team-bot', chat: 'x1', sender: 'Ana' }, status: 'pending', created: AT, expires: later(), ...extra,
})

async function serve(page, list) {
  const s = { list, calls: [], fileReads: 0 }
  await stubInbox(page, makeInbox(0))
  await page.route('**/api/chats/files/content*', (route) => { s.fileReads++; return route.fulfill({ status: 404, json: { error: { message: 'not found' } } }) })
  await page.route(/\/api\/chats\/changes(\?.*)?$/, route => route.fulfill({ json: s.list.filter(p => p.status === 'pending') }))
  await page.route(/\/api\/chats\/changes\/([a-z0-9]+)(\/[a-z]+)?$/, (route) => {
    const req = route.request()
    const parts = new URL(req.url()).pathname.split('/')
    const verb = parts.length > 5 ? parts[5] : ''
    const p = s.list.find(x => x.id === parts[4])
    s.calls.push(`${req.method()} ${parts[4]} ${verb}`.trim())
    if (!p) return route.fulfill({ status: 404, json: { error: { message: 'not found' } } })
    if (verb === 'confirm') Object.assign(p, { status: 'applied' })
    else if (verb === 'dismiss') p.status = 'dismissed'
    else if (verb === 'undo') p.status = 'reverted'
    return route.fulfill({ json: p })
  })
  return s
}

async function shot(page, name, locator) {
  for (const theme of ['light', 'dark']) {
    await page.evaluate((t) => document.documentElement.setAttribute('data-theme', t), theme)
    await page.waitForTimeout(500)
    await (locator || page).screenshot({ path: `test-results/${name}-${theme}.png` })
  }
  await page.evaluate(() => document.documentElement.setAttribute('data-theme', 'light'))
}

test('a knowledge proposal names the collection, shows the text and its size, and reads no file', async ({ page }) => {
  const s = await serve(page, [make()])
  await page.goto('/app/chats/proposals')
  const card = page.getByTestId('change-proposal')
  await expect(card).toHaveCount(1)
  await expect(card).toHaveAttribute('data-kind', 'knowledge')
  await expect(card.getByTestId('change-summary')).toHaveText('Router reset steps')
  const rows = card.getByTestId('change-rows')
  await expect(rows.locator('[data-row="kind"]')).toContainText('Knowledge')
  await expect(rows.locator('[data-row="target"] dt')).toHaveText('Collection')
  await expect(rows.locator('[data-row="target"] dd')).toHaveText('router-manuals')
  await expect(rows.locator('[data-row="source"]')).toContainText('Connector team-bot, from Ana')
  const preview = card.getByTestId('change-knowledge-preview')
  await expect(preview).toContainText('Step 1: hold the reset button')
  await expect(preview).toContainText('Step 20:')
  await expect(preview).not.toContainText('Step 21:')
  await expect(preview).toContainText('4 more lines not shown')
  await expect(card.getByTestId('change-knowledge-size')).toContainText('of at most 64 KiB')
  await expect(preview).toContainText('Nothing is added until you confirm')
  await expect(card.getByTestId('change-diff')).toHaveCount(0)
  expect(s.fileReads).toBe(0)
  await shot(page, 'knowledge-suggested-change', page.getByTestId('proposals-page'))
})

test('the card says how many passages will be embedded and with which model', async ({ page }) => {
  await serve(page, [make()])
  await page.route(/\/api\/collections(\?.*)?$/, route => route.fulfill({ json: { collections: ['router-manuals'], embedding_model: 'granite-embedding-107m-multilingual', embedding_model_installed: true, search_mode: 'semantic', max_chunk_size: 400, chunk_overlap: 0 } }))
  await page.goto('/app/chats/proposals')
  const embed = page.getByTestId('change-proposal').getByTestId('change-knowledge-embed')
  await expect(embed).toHaveText('About 4 passages will be embedded with granite-embedding-107m-multilingual.')
  await page.setViewportSize({ width: 360, height: 740 })
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(0)
})

test('a small text is one passage, and without the model name the card says the server embeds it', async ({ page }) => {
  await serve(page, [make({ content: 'Hold the reset button for ten seconds.\n' })])
  // The list is not readable (the collections service is still starting): the
  // card falls back to the defaults of the server and names no model.
  await page.route(/\/api\/collections(\?.*)?$/, route => route.fulfill({ status: 503, json: { error: 'collections are starting, please retry shortly' } }))
  await page.goto('/app/chats/proposals')
  await expect(page.getByTestId('change-knowledge-embed')).toHaveText("1 passage will be embedded with the server's embedding model.")
})

test('a flagged text shows a warning next to the text and can still be confirmed here', async ({ page }) => {
  const s = await serve(page, [make({ id: 'k3', summary: 'Office Wi-Fi', content: 'Wi-Fi of the office\npassword: correct-horse-battery\n', warning: 'secret' }), make({ id: 'k4' })])
  await page.goto('/app/chats/proposals')
  const cards = page.getByTestId('change-proposal')
  await expect(cards).toHaveCount(2)
  const flagged = cards.first()
  await expect(flagged.getByTestId('change-warning')).toContainText('Check this text before you confirm.')
  await expect(flagged.getByTestId('change-warning')).toContainText('holds a secret')
  await expect(flagged.getByTestId('change-warning')).toContainText('A messaging chat cannot confirm it')
  // The text is still shown, so the owner can judge it.
  await expect(flagged.getByTestId('change-knowledge-preview')).toContainText('password: correct-horse-battery')
  await expect(cards.nth(1).getByTestId('change-warning')).toHaveCount(0)
  await page.setViewportSize({ width: 360, height: 740 })
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(0)
  await flagged.getByRole('button', { name: 'Confirm', exact: true }).click()
  await expect(flagged).toHaveAttribute('data-state', 'applied')
  expect(s.calls).toContain('POST k3 confirm')
})

test('Confirm adds it and keeps Undo although the change has no change set; Undo takes it back', async ({ page }) => {
  const s = await serve(page, [make()])
  await page.goto('/app/chats/proposals')
  const card = page.getByTestId('change-proposal')
  await card.getByRole('button', { name: 'Confirm', exact: true }).click()
  await expect(card).toHaveAttribute('data-state', 'applied')
  await expect(card).toContainText('router-manuals')
  await card.getByRole('button', { name: 'Undo' }).click()
  await expect(card).toHaveAttribute('data-state', 'reverted')
  expect(s.calls).toEqual(expect.arrayContaining(['POST k1 confirm', 'POST k1 undo']))
  expect(s.fileReads).toBe(0)
})

test('Edit then confirm labels the text with the collection, and Dismiss adds nothing', async ({ page }) => {
  const s = await serve(page, [make(), make({ id: 'k2', summary: 'Wiki page' })])
  await page.goto('/app/chats/proposals')
  const cards = page.getByTestId('change-proposal')
  await cards.first().getByRole('button', { name: 'Edit' }).click()
  await expect(cards.first().getByLabel('Text to add to router-manuals')).toBeVisible()
  await cards.first().getByRole('button', { name: 'Cancel' }).click()
  await cards.nth(1).getByRole('button', { name: 'Dismiss' }).click()
  await expect(cards.nth(1)).toHaveAttribute('data-state', 'dismissed')
  expect(s.calls).not.toContain('POST k2 confirm')
})
