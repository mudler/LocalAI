import { test, expect } from './coverage-fixtures.js'
import { installAgents } from './agents-fixtures.js'
import { PID, SECRET, ROTATED, eventTrigger, serveEventProposal } from './event-trigger-fixtures.js'

// The card of an event trigger that a chat proposes: what it says before it is
// created, the hook URL, the secret shown once with a copy button and a curl
// example after Create trigger, and what is left after a reload (the URL, and
// Rotate secret, but not the secret). The API is stubbed by a small model of
// the server; that the server shows the secret only once is tested in Go.

const card = (page) => page.getByTestId('trigger-proposal')
const hook = (page) => page.getByTestId('event-hook')
const shots = process.env.EVENT_TRIGGER_SHOTS || 'test-results'

async function open(page) {
  const s = await serveEventProposal(page)
  await page.goto('/app/chats/c1')
  await expect(page.getByTestId('chat-message').nth(1)).toBeVisible()
  await expect(card(page)).toHaveAttribute('data-state', 'proposed')
  return s
}

// Everything the page keeps in this browser, as text.
const stored = (page) => page.evaluate(() => JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }))

for (const [name, size] of [['desktop', { width: 1280, height: 900 }], ['phone', { width: 390, height: 844 }]]) {
  test.describe(name, () => {
    test.use({ viewport: size })

    test('the card of an event proposal says when it runs in plain words', async ({ page }) => {
      await open(page)
      const row = card(page).locator('[data-row="when"]')
      await expect(row.getByTestId('event-sentence')).toHaveText('When github sends pull_request where action=opened')
      await expect(row.locator('code')).toHaveText(['github', 'pull_request', 'action=opened'])
      await expect(card(page).getByRole('button', { name: 'Create trigger' })).toBeVisible()
      // It is not a schedule: no first run, no time zone.
      await expect(row).not.toContainText('First run')
      await card(page).scrollIntoViewIfNeeded()
      await card(page).screenshot({ path: `${shots}/event-${name}-1-proposal.png` })
      const box = await card(page).boundingBox()
      expect(box.x + box.width).toBeLessThanOrEqual(size.width)
    })

    test('after Create trigger the card shows the URL, the secret once and a curl example', async ({ page, context }) => {
      await context.grantPermissions(['clipboard-read', 'clipboard-write'])
      const s = await open(page)
      await card(page).getByRole('button', { name: 'Create trigger' }).click()
      await expect(card(page)).toHaveAttribute('data-state', 'accepted')
      await expect(card(page)).toContainText('Listening:')

      const url = await page.getByTestId('hook-url').innerText()
      expect(url).toBe(`${new URL(page.url()).origin}/api/chats/triggers/${PID}/hook`)
      await expect(page.getByTestId('hook-secret')).toHaveText(SECRET)
      await expect(page.getByTestId('hook-paste')).toHaveText("Paste the URL and the secret into the sender's webhook settings. The secret is not shown again.")

      const curl = page.getByTestId('hook-curl')
      await expect(curl).toContainText(`curl -X POST '${url}?source=github'`)
      await expect(curl).toContainText(`-H 'Authorization: Bearer ${SECRET}'`)
      await expect(curl).toContainText(`-d '{"type":"pull_request","action":"opened"}'`)

      await card(page).scrollIntoViewIfNeeded()
      await card(page).screenshot({ path: `${shots}/event-${name}-2-secret.png` })
      const box = await card(page).boundingBox()
      expect(box.x + box.width).toBeLessThanOrEqual(size.width)

      // The copy buttons are named, and copy what they say.
      const copySecret = page.getByRole('button', { name: 'Copy the secret' })
      await copySecret.click()
      await expect(copySecret).toContainText('Copied')
      expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(SECRET)
      await page.getByRole('button', { name: 'Copy the hook URL' }).click()
      expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(url)
      await page.getByRole('button', { name: 'Copy the example call' }).click()
      expect(await page.evaluate(() => navigator.clipboard.readText())).toContain(`Bearer ${SECRET}`)

      // The secret is real text that can be selected, and it is nowhere that survives a reload.
      expect(await page.getByTestId('hook-secret').evaluate((el) => getComputedStyle(el).userSelect)).not.toBe('none')
      expect(await stored(page)).not.toContain(SECRET)
      expect(page.url()).not.toContain(SECRET)
      expect(s.calls).toContain('POST confirm')

    })

    test('after a reload the URL is still there and the secret is gone', async ({ page }) => {
      await open(page)
      await card(page).getByRole('button', { name: 'Create trigger' }).click()
      await expect(page.getByTestId('hook-secret')).toHaveText(SECRET)
      await page.reload()
      await expect(card(page)).toHaveAttribute('data-state', 'accepted')
      await expect(page.getByTestId('hook-url')).toContainText(`/api/chats/triggers/${PID}/hook`)
      await expect(page.getByTestId('hook-secret')).toHaveCount(0)
      await expect(page.getByTestId('hook-curl')).toHaveCount(0)
      await expect(page.getByTestId('hook-no-secret')).toBeVisible()
      await expect(page.getByRole('button', { name: 'Rotate secret' })).toBeVisible()
      expect(await page.content()).not.toContain(SECRET)
      expect(await stored(page)).not.toContain(SECRET)
      await card(page).scrollIntoViewIfNeeded()
      await card(page).screenshot({ path: `${shots}/event-${name}-3-reloaded.png` })
    })

    test('Rotate secret asks first, then shows the new secret once', async ({ page }) => {
      const s = await open(page)
      await card(page).getByRole('button', { name: 'Create trigger' }).click()
      await page.reload()
      await expect(page.getByTestId('hook-secret')).toHaveCount(0)

      await page.getByRole('button', { name: 'Rotate secret' }).click()
      const dialog = page.getByRole('alertdialog')
      await expect(dialog).toContainText('The old secret stops working at once.')
      await dialog.getByRole('button', { name: 'Cancel' }).click()
      expect(s.calls).not.toContain('POST secret')
      await expect(page.getByTestId('hook-secret')).toHaveCount(0)
      await card(page).scrollIntoViewIfNeeded()
      await card(page).screenshot({ path: `${shots}/event-${name}-4-before-rotate.png` })

      await page.getByRole('button', { name: 'Rotate secret' }).click()
      await page.getByRole('alertdialog').getByRole('button', { name: 'Rotate', exact: true }).click()
      await expect(page.getByTestId('hook-secret')).toHaveText(ROTATED)
      await expect(page.getByTestId('hook-curl')).toContainText(`Bearer ${ROTATED}`)
      expect(s.calls).toContain('POST secret')
      expect(await stored(page)).not.toContain(ROTATED)
      await card(page).scrollIntoViewIfNeeded()
      await card(page).screenshot({ path: `${shots}/event-${name}-5-rotated.png` })

      await page.reload()
      await expect(page.getByTestId('hook-secret')).toHaveCount(0)
      expect(await page.content()).not.toContain(ROTATED)
    })
  })
}

test.describe('Trigger form', () => {
  test('an event trigger asks for the sender, the type and the matches, and shows the secret once after it is made', async ({ page }) => {
    await installAgents(page)
    const sent = []
    // Registered after the agents' stubs, so it answers the create first and leaves the list to them.
    await page.route(/\/api\/chats\/triggers(\?.*)?$/, (route) => {
      const req = route.request()
      if (req.method() !== 'POST') return route.fallback()
      sent.push(req.postDataJSON())
      return route.fulfill({ status: 201, json: { ...eventTrigger({ enabled: true, hook_path: `/api/chats/triggers/${PID}/hook` }), secret: SECRET } })
    })
    await page.goto('/app/triggers/new')
    await page.getByLabel('Name', { exact: true }).fill('Review new pull requests')
    await page.getByLabel('Agent', { exact: true }).selectOption('a1')
    await page.getByLabel('What to do', { exact: true }).fill('Read the pull request and write a short review.')
    await page.getByLabel('When', { exact: true }).selectOption('event')
    await expect(page.getByLabel('Time zone')).toHaveCount(0)
    await page.getByLabel('Sender').fill('github')
    await page.getByLabel('Event type').fill('pull_request')
    await page.getByLabel('Only when').fill('action=opened\n\ndraft=false')
    await page.getByRole('button', { name: 'Create trigger' }).first().click()

    expect(sent).toHaveLength(1)
    expect(sent[0]).toMatchObject({ kind: 'event', source: 'github', event_type: 'pull_request', matches: ['action=opened', 'draft=false'], agent_id: 'a1' })
    expect(sent[0].schedule).toBeUndefined()
    await expect(page.getByTestId('task-created')).toBeVisible()
    await expect(page.getByTestId('hook-secret')).toHaveText(SECRET)
    await expect(page.getByTestId('hook-curl')).toContainText(`Bearer ${SECRET}`)
    expect(await stored(page)).not.toContain(SECRET)
    expect(page.url()).not.toContain(SECRET)
    await page.screenshot({ path: `${shots}/event-form-created.png` })
  })

  test('an event trigger without a sender is not sent', async ({ page }) => {
    const s = await installAgents(page)
    await page.goto('/app/triggers/new')
    await page.getByLabel('Name', { exact: true }).fill('No sender')
    await page.getByLabel('What to do', { exact: true }).fill('Do it.')
    await page.getByLabel('When', { exact: true }).selectOption('event')
    await page.getByRole('button', { name: 'Create trigger' }).first().click()
    await expect(page.getByText('Name the sender of the events.').first()).toBeVisible()
    expect(s.calls.filter((c) => c.method === 'POST')).toHaveLength(0)
  })
})
