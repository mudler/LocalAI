import { test, expect } from './coverage-fixtures.js'
import { mockDiscovery, newState } from './discovery-fixtures.js'

// Account, About you and the assistant: the Help and suggestions card (the off
// switches, kept under seen in user.json), the privacy note of the You card,
// and the Versions of a settings file. Every API call is stubbed.

const SHOTS = process.env.DISCOVERY_SHOTS || ''
// Screenshots wait out the entry fade, so they show the settled screen.
const shot = async (page, name) => { if (SHOTS) { await page.waitForTimeout(600); await page.screenshot({ path: `${SHOTS}/${name}.png`, fullPage: true }) } }

const help = (page) => page.getByTestId('wk-card-help')

test('the switches follow the server default until the user chooses, and write seen in user.json', async ({ page }) => {
  const state = newState({ features: { chats_suggestions: false }, files: { user: { name: 'Marta', seen: { home_examples: { hidden: '2026-10-29' } } } } })
  await mockDiscovery(page, state)
  await page.goto('/app/account?tab=about')
  const offers = help(page).getByTestId('wk-help-offers')
  await expect(offers).toHaveAttribute('aria-checked', 'false', { timeout: 15_000 })
  await expect(help(page).getByTestId('wk-help-new')).toHaveAttribute('aria-checked', 'true')
  await offers.click()
  await expect(offers).toHaveAttribute('aria-checked', 'true')
  expect(state.files.user.seen.offers_off).toBe(false)
  expect(state.files.user.name).toBe('Marta')
  expect(state.files.user.seen.home_examples).toEqual({ hidden: '2026-10-29' })

  await help(page).getByTestId('wk-help-new').click()
  await expect.poll(() => state.files.user.seen.new_off).toBe(true)

  await help(page).getByTestId('wk-help-examples').click()
  await expect.poll(() => state.files.user.seen.home_examples).toBeUndefined()
  await expect(help(page).getByTestId('wk-help-examples')).toBeDisabled()
})

test('forgetting what the assistant learned clears the answers and the caps, and keeps the switches', async ({ page }) => {
  const state = newState({ files: { user: { seen: {
    offers_off: false, last_offer: '2026-10-30T09:00:00Z', offer_chats: ['c1'],
    offers: { profile: { state: 'declined', at: '2026-10-30' }, shortcut: { state: 'later', at: '2026-10-30', count: 1 } },
  } } } })
  await mockDiscovery(page, state)
  await page.goto('/app/account?tab=about')
  await expect(help(page)).toContainText('You turned down 2 offers.', { timeout: 15_000 })
  await shot(page, 'E1-account-about-help')
  await help(page).getByTestId('wk-help-forget').click()
  await expect.poll(() => state.files.user.seen).toEqual({ offers_off: false })
  await expect(help(page).getByRole('status').filter({ hasText: 'The assistant may offer again.' })).toBeVisible()
})

test('the You card says what reaches the model', async ({ page }) => {
  const state = newState()
  await mockDiscovery(page, state)
  await page.goto('/app/account?tab=about')
  await expect(page.getByTestId('wk-privacy')).toContainText('The assistant receives your name, this text, your language and time zone', { timeout: 15_000 })
})

test('Versions lists who changed the file and where, and reverts the one picked', async ({ page }) => {
  const state = newState({ files: { persona: { name: 'Ada', system_prompt: 'Keep answers short.' } } })
  const reverts = []
  await mockDiscovery(page, state)
  await page.route(/\/api\/chats\/wellknown\/persona\/versions/, route => route.fulfill({ json: { versions: [
    { rev: 30, at: new Date().toISOString(), kind: 'agent', chat: 'c1', chat_title: 'Context windows', valid: true, data: { name: 'Ada', system_prompt: 'Keep answers short.' } },
    { rev: 20, at: '2026-10-29T18:10:00Z', kind: 'user', valid: true, data: { name: 'Ada' } },
    { rev: 10, at: '2026-10-26T09:00:00Z', kind: 'agent', chat: 'c0', chat_title: 'Getting started', valid: false, errors: ['name: must be a string'] },
  ] } }))
  await page.route(/\/api\/chats\/wellknown\/persona\/revert/, (route) => {
    reverts.push(route.request().postDataJSON())
    state.files.persona = { name: 'Ada' }
    return route.fulfill({ json: { name: 'persona', path: '/LocalAI/persona.json', exists: true, version: 9, data: { name: 'Ada' }, valid: true } })
  })
  await page.goto('/app/account?tab=about')
  await page.getByTestId('wk-versions-persona').click({ timeout: 15_000 })
  const list = page.getByTestId('wk-versions-list-persona')
  await expect(list).toContainText('By the assistant in "Context windows" · current')
  await expect(list).toContainText('By you')
  await expect(list).toContainText('has an error')
  await expect(list).toContainText('Revert keeps the newer version in this list.')
  await list.getByRole('radio').nth(1).check()
  await shot(page, 'E2-account-versions')
  await list.getByTestId('wk-versions-revert').click()
  expect(reverts).toEqual([{ rev: 20 }])
  await expect(page.getByTestId('wk-field-persona-system_prompt')).toHaveValue('')
})

test('"Turn them back on in Account" lands on the card', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 600 })
  const state = newState()
  await mockDiscovery(page, state)
  await page.goto('/app/account?tab=about#help')
  await expect(help(page)).toBeInViewport({ timeout: 15_000 })
})

for (const theme of ['dark', 'light']) {
  test(`on a phone the card stacks its switches under their text (${theme})`, async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await page.addInitScript((t) => { try { localStorage.setItem('localai-theme', t) } catch { /* private window */ } }, theme)
    const state = newState()
    await mockDiscovery(page, state)
    await page.goto('/app/account?tab=about#help')
    await expect(help(page).getByTestId('wk-help-offers')).toBeVisible({ timeout: 15_000 })
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1)
    expect(overflow).toBe(false)
    if (SHOTS) { await page.waitForTimeout(600); await help(page).screenshot({ path: `${SHOTS}/E3-mobile-help-card-${theme}.png` }) }
  })
}
