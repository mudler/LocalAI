import { test, expect } from './coverage-fixtures.js'
import { installCoordinators, COORDINATOR, auditRows } from './coordinator-fixtures.js'

// The coordinator page (create, edit, state card, audit log) with the API
// stubbed. The agents page shows the coordinator flag the list carries.

const posts = (s) => s.calls.filter(c => c.method === 'POST' && c.url === '/api/chats/coordinators')
const puts = (s) => s.calls.filter(c => c.method === 'PUT')

async function addUserContact(page, { id, user, name }) {
  await page.getByTestId('coordinator-contact-add').click()
  const sheet = page.getByTestId('coordinator-contact-sheet')
  await sheet.getByLabel('Contact ID').fill(id)
  if (name) await sheet.locator('#crd-c-name').fill(name)
  await sheet.getByLabel('LocalAI user ID').fill(user)
  await sheet.getByRole('button', { name: 'Save contact' }).click()
  await expect(sheet).toHaveCount(0)
}

test.describe('Coordinator page', () => {
  test('creates a coordinator with a user contact and a chat contact, and opens its page', async ({ page }) => {
    const s = await installCoordinators(page, { coordinators: [] })
    await page.goto('/app/agents/new-coordinator')
    await expect(page.getByRole('heading', { name: 'New coordinator', level: 1 })).toBeVisible()
    // The standing text starts as the starter, and says whose rules these are.
    await expect(page.locator('#crd-instructions')).toHaveValue(/You are a coordinator/)
    await expect(page.getByTestId('coordinator-policy-note')).toContainText('LocalAI has no built-in list of topics')
    await page.locator('#crd-name').fill('release-watch')
    await page.locator('#crd-page').fill('release-watch')
    await addUserContact(page, { id: 'sam', user: 'u-sam', name: 'Sam' })

    // A chat contact: the binding is a list of the person's connections, the chat is suggested from it.
    await page.getByTestId('coordinator-contact-add').click()
    const sheet = page.getByTestId('coordinator-contact-sheet')
    await sheet.getByTestId('coordinator-contact-kind-external').click()
    await sheet.getByLabel('Contact ID').fill('ops-team')
    await sheet.getByLabel('Messaging connection').selectOption('b1')
    await sheet.getByLabel('Chat', { exact: true }).fill('C42')
    await sheet.getByRole('button', { name: 'Save contact' }).click()
    await expect(page.getByTestId('coordinator-contact')).toHaveCount(2)
    await expect(page.getByTestId('coordinator-contact').nth(1)).toContainText('Team chat, chat C42')

    await page.getByTestId('coordinator-wake-every').selectOption('3600')
    await page.getByLabel('Messages per minute').fill('5')
    await page.getByLabel('Helper depth').fill('1')
    await page.getByTestId('coordinator-save').click()

    await expect(page).toHaveURL(/\/app\/agents\/k1\/coordinator$/)
    expect(posts(s)).toHaveLength(1)
    const body = posts(s)[0].body
    expect(body).toMatchObject({ name: 'release-watch', page: 'release-watch', wake_every: 3600, send_per_minute: 5, max_depth: 1 })
    expect(body.instructions).toMatch(/You are a coordinator/)
    expect(body.contacts).toEqual([
      { id: 'sam', name: 'Sam', kind: 'user', user: 'u-sam' },
      { id: 'ops-team', kind: 'external', binding: 'b1', chat: 'C42' },
    ])
    expect('send_per_day' in body).toBe(false)
    // The saved coordinator opens on its own page with a state card.
    await expect(page.getByTestId('coordinator-card')).toBeVisible()
  })

  test('says what is missing in place and sends nothing', async ({ page }) => {
    const s = await installCoordinators(page, { coordinators: [] })
    await page.goto('/app/agents/new-coordinator')
    await page.getByTestId('coordinator-save').click()
    await expect(page.locator('#crd-name-msg')).toHaveText('Give the coordinator a name.')
    await expect(page.locator('#crd-name')).toBeFocused()
    expect(posts(s)).toHaveLength(0)

    await page.locator('#crd-name').fill('x')
    await page.getByLabel('Messages per day').fill('9999')
    await page.getByTestId('coordinator-save').click()
    await expect(page.locator('#crd-send_per_day-msg')).toContainText('whole number from 1 to 5000')
    await expect(page.getByLabel('Messages per day')).toBeFocused()
    expect(posts(s)).toHaveLength(0)
  })

  test('checks a contact in its sheet: a bad id, a repeated id, a missing user, a binding that cannot carry replies', async ({ page }) => {
    await installCoordinators(page, { coordinators: [] })
    await page.goto('/app/agents/new-coordinator')
    await addUserContact(page, { id: 'sam', user: 'u-sam' })
    await page.getByTestId('coordinator-contact-add').click()
    const sheet = page.getByTestId('coordinator-contact-sheet')
    await sheet.getByLabel('Contact ID').fill('Bad Id')
    await sheet.getByRole('button', { name: 'Save contact' }).click()
    await expect(sheet.locator('#crd-c-id-msg')).toContainText('lowercase letters, digits and dashes')
    await expect(sheet.getByLabel('Contact ID')).toBeFocused()
    await sheet.getByLabel('Contact ID').fill('sam')
    await sheet.getByRole('button', { name: 'Save contact' }).click()
    await expect(sheet.locator('#crd-c-id-msg')).toHaveText('Another contact already has this ID.')
    await sheet.getByLabel('Contact ID').fill('kim')
    await sheet.getByRole('button', { name: 'Save contact' }).click()
    await expect(sheet.locator('#crd-c-user-msg')).toHaveText('Enter the ID of a LocalAI user.')
    await sheet.getByTestId('coordinator-contact-kind-external').click()
    await sheet.getByLabel('Messaging connection').selectOption('b2')
    await expect(sheet.getByTestId('coordinator-contact-mode')).toContainText('starts a new chat for each "thread"')
    await sheet.getByRole('button', { name: 'Save contact' }).click()
    await expect(sheet.locator('#crd-c-chat-msg')).toHaveText('Enter the chat to write in.')
  })

  test('shows the reason of a 400 and keeps what was typed', async ({ page }) => {
    const s = await installCoordinators(page, { coordinators: [], createError: 'helpers: the highest allowed here is 16' })
    await page.goto('/app/agents/new-coordinator')
    await page.locator('#crd-name').fill('big')
    await page.getByLabel('Helpers at once').fill('40')
    await page.getByTestId('coordinator-save').click()
    const note = page.getByTestId('coordinator-server-error')
    await expect(note).toContainText('Not saved.')
    await expect(note).toContainText('the highest allowed here is 16')
    await expect(page.getByLabel('Helpers at once')).toHaveValue('40')
    await expect(page).toHaveURL(/new-coordinator$/)
    expect(posts(s)).toHaveLength(1)
  })

  test('edits a coordinator: the form holds what is saved and Save replaces it', async ({ page }) => {
    const s = await installCoordinators(page)
    await page.goto('/app/agents/k1/coordinator')
    await expect(page.getByRole('heading', { name: 'daily-ops', level: 1 })).toBeVisible()
    await expect(page.locator('#crd-name')).toHaveValue('daily-ops')
    await expect(page.locator('#crd-page')).toHaveValue('daily-ops')
    await expect(page.getByTestId('coordinator-wake-every')).toHaveValue('3600')
    await expect(page.getByLabel('Messages per day')).toHaveValue('100')
    await expect(page.getByTestId('coordinator-contact')).toHaveCount(2)
    await expect(page.getByTestId('coordinator-ceilings')).toContainText('limited to 48 a day')
    // The page loaded the coordinator, and read nothing it did not need.
    await page.locator('#crd-name').fill('nightly-ops')
    await page.getByTestId('coordinator-contact').first().getByRole('button', { name: 'Remove Sam' }).click()
    await page.getByTestId('coordinator-save').click()
    await expect.poll(() => puts(s).length).toBe(1)
    expect(puts(s)[0].url).toBe('/api/chats/coordinators/k1')
    expect(puts(s)[0].body).toMatchObject({ name: 'nightly-ops', wake_every: 3600, page: 'daily-ops' })
    expect(puts(s)[0].body.contacts.map(c => c.id)).toEqual(['ops-team'])
    await expect(page.getByRole('heading', { name: 'nightly-ops', level: 1 })).toBeVisible()
    await expect(page.getByText('Coordinator "nightly-ops" saved')).toBeVisible()
  })

  test('a coordinator that cannot be read says so and offers Try again', async ({ page }) => {
    await installCoordinators(page, { coordinators: [] })
    await page.goto('/app/agents/missing/coordinator')
    await expect(page.getByTestId('load-error')).toContainText('Could not load this coordinator.')
    await expect(page.getByRole('button', { name: 'Try again' })).toBeVisible()
  })

  test('pauses and resumes as the owner: the chip, the note and the next wake follow', async ({ page }) => {
    const s = await installCoordinators(page)
    await page.goto('/app/agents/k1/coordinator')
    const card = page.getByTestId('coordinator-card')
    await expect(page.getByTestId('coordinator-state')).toHaveText('Scheduled')
    await expect(page.getByTestId('coordinator-runs')).toHaveText('3 of 48 runs')
    await expect(page.getByTestId('coordinator-sent')).toHaveText('5 of 100 messages')
    await card.getByTestId('coordinator-pause').click()
    await expect(page.getByTestId('coordinator-state')).toHaveText('Paused')
    await expect(page.getByTestId('coordinator-paused-note')).toContainText('Paused by alice')
    await expect(page.getByTestId('coordinator-next-wake')).toHaveText('Not while paused')
    expect(s.calls.filter(c => c.url === '/api/chats/coordinators/k1/pause')).toHaveLength(1)
    await card.getByTestId('coordinator-resume').click()
    await expect(page.getByTestId('coordinator-state')).toHaveText('Scheduled')
    await expect(page.getByTestId('coordinator-paused-note')).toHaveCount(0)
    expect(s.calls.filter(c => c.url === '/api/chats/coordinators/k1/resume')).toHaveLength(1)
  })

  test('lists the audit rows, filters by outcome and pages to older rows', async ({ page }) => {
    const s = await installCoordinators(page)
    await page.goto('/app/agents/k1/coordinator')
    const rows = page.getByTestId('coordinator-audit-row')
    await expect(rows).toHaveCount(25)
    await expect(rows.first()).toContainText('Message')
    // The reason is words, not the stored code.
    await page.getByTestId('crd-f-outcome').selectOption('denied')
    await expect(rows.first()).toHaveAttribute('data-outcome', 'denied')
    await expect(rows.first()).toContainText('Not a listed contact')
    await expect(rows.first()).toContainText('coordinator → stranger')
    expect(await rows.evaluateAll(els => els.every(e => e.dataset.outcome === 'denied'))).toBe(true)
    await expect(rows).toHaveCount(12)
    expect(s.calls.some(c => c.url.endsWith('/audit') && c.search.includes('outcome=denied'))).toBe(true)
    await page.getByTestId('crd-f-clear').click()
    await expect(rows).toHaveCount(25)
    await page.getByTestId('coordinator-audit-more').click()
    await expect(rows).toHaveCount(50)
    const more = s.calls.filter(c => c.url.endsWith('/audit')).at(-1)
    expect(more.search).toContain('before=')
    await page.getByTestId('coordinator-audit-more').click()
    await expect(rows).toHaveCount(60)
    await expect(page.getByTestId('coordinator-audit-more')).toHaveCount(0)
    // A filter with no match says so and offers to clear.
    await page.getByTestId('crd-f-kind').selectOption('pause')
    await expect(page.getByTestId('coordinator-audit-empty')).toContainText('No entry matches these filters.')
  })

  test('says an empty log and a failed log in different words', async ({ page }) => {
    await installCoordinators(page, { audit: [] })
    await page.goto('/app/agents/k1/coordinator')
    await expect(page.getByTestId('coordinator-audit-empty')).toContainText('Nothing in the log yet.')
    const page2 = await page.context().newPage()
    await installCoordinators(page2, { auditError: true })
    await page2.goto('/app/agents/k1/coordinator')
    await expect(page2.getByTestId('coordinator-audit').getByTestId('load-error')).toContainText('Could not load the audit log.')
  })

  test('the agents page shows the coordinator flag from the list and asks for no coordinator list', async ({ page }) => {
    const s = await installCoordinators(page)
    await page.goto('/app/agents')
    const chip = page.getByTestId('agent-coordinator-chip')
    await expect(chip).toHaveCount(1)
    await expect(chip).toHaveText('Coordinator')
    await expect(page.locator('.ag-row[data-agent="code-reviewer"]').getByTestId('agent-coordinator-chip')).toHaveCount(0)
    await expect(page.getByTestId('agents-new-coordinator')).toHaveAttribute('href', '/app/agents/new-coordinator')
    expect(s.calls.filter(c => c.url.startsWith('/api/chats/coordinators'))).toHaveLength(0)
    // Edit on a coordinator opens its own page, not the agent form.
    await page.locator('.ag-row[data-agent="daily-ops"]').getByRole('button', { name: /More actions for daily-ops|daily-ops/ }).click()
    await page.getByRole('menuitem', { name: 'Edit' }).click()
    await expect(page).toHaveURL(/\/app\/agents\/k1\/coordinator$/)
  })

  test('works with the keyboard: the contact sheet takes focus, Escape closes it and returns focus', async ({ page }) => {
    await installCoordinators(page, { coordinators: [] })
    await page.goto('/app/agents/new-coordinator')
    const add = page.getByTestId('coordinator-contact-add')
    await add.focus()
    await page.keyboard.press('Enter')
    const sheet = page.getByTestId('coordinator-contact-sheet')
    await expect(sheet).toBeVisible()
    await expect(sheet.locator('#crd-c-name')).toBeVisible()
    expect(await page.evaluate(() => !!document.activeElement.closest('[role="dialog"]'))).toBe(true)
    await page.keyboard.press('Escape')
    await expect(sheet).toHaveCount(0)
    await expect(add).toBeFocused()
    // Every field has a name a screen reader reads.
    for (const label of [/^Name/, /^Description/, 'Instructions', /^Page name/, 'Wake on a schedule', 'Messages per minute', 'Messages per day', 'Helpers at once', 'Helper depth']) {
      await expect(page.getByLabel(label).first()).toBeVisible()
    }
  })

  test('fits a phone: no sideways scroll on the form, the card or the audit table', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 800 })
    await installCoordinators(page)
    for (const path of ['/app/agents/new-coordinator', '/app/agents/k1/coordinator']) {
      await page.goto(path)
      await expect(page.getByTestId('coordinator-page')).toBeVisible()
      if (path.includes('k1')) await expect(page.getByTestId('coordinator-audit-row').first()).toBeVisible()
      const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
      expect(over).toBeLessThanOrEqual(0)
    }
  })

  test('renders in the dark theme with the tokens, not fixed colours', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('localai-theme', 'dark'))
    await installCoordinators(page)
    await page.goto('/app/agents/k1/coordinator')
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
    const card = page.getByTestId('coordinator-card')
    await expect(card).toBeVisible()
    const [bg, text] = await card.evaluate(el => { const c = getComputedStyle(el); return [c.backgroundColor, getComputedStyle(el.querySelector('dd')).color] })
    const lum = (rgb) => { const [r, g, b] = rgb.match(/\d+/g).map(Number); return (0.2126 * r + 0.7152 * g + 0.0722 * b) / 255 }
    expect(lum(bg)).toBeLessThan(0.35)
    expect(lum(text)).toBeGreaterThan(0.6)
  })
})
