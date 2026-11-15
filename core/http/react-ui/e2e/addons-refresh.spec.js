import { test, expect } from './coverage-fixtures.js'
import { addonsScene, APPS, CONNECTIONS, KINDS } from './addons-refresh-fixtures.js'
import { installAgents } from './agents-fixtures.js'

// The refreshed Add-ons page, with the add-on, library and connection routes
// stubbed (addons-refresh-fixtures.js): groups, the one setup strip, the one
// action of a row, the details pane, the library cards, the icons and the
// connection rows. The stubbed icon route answers a one pixel image, so the
// specs look at which tile is drawn, not at the picture.

const tile = (page, id) => page.locator(`[data-testid="addon-tile"][data-app="${id}"]`)

test.describe('Add-ons: installed', () => {
  test('the list is grouped, and each row has one state and at most one action', async ({ page }) => {
    await addonsScene(page)
    await page.goto('/app/addons')
    const groups = page.getByTestId('addon-group')
    await expect(groups).toHaveCount(3)
    await expect(groups.nth(0)).toHaveAttribute('data-group', 'chat')
    await expect(groups.nth(1)).toHaveAttribute('data-group', 'tools')
    await expect(groups.nth(2)).toHaveAttribute('data-group', 'models')
    await expect(groups.nth(0).getByRole('heading')).toHaveText('Chat apps')
    await expect(groups.nth(0).getByTestId('addon-tile')).toHaveCount(2)
    await expect(groups.nth(1).getByRole('heading')).toHaveText('Tools and services')
    await expect(groups.nth(2).getByRole('heading')).toHaveText('Media and models')
    await expect(groups.nth(2).getByTestId('addon-tile')).toHaveCount(1)
    // One chip in the head of every row, and no row with two buttons beside its menu.
    for (const row of await page.getByTestId('addon-tile').all()) {
      await expect(row.locator('.ad-tile__head .ui-chip')).toHaveCount(1)
      await expect(row.locator('.ad-tile__side').getByRole('button').or(row.locator('.ad-tile__side a.dk-btn'))).not.toHaveCount(3)
      expect(await row.locator('.ad-tile__side .dk-btn:not(.action-menu__trigger)').count()).toBeLessThanOrEqual(1)
    }
    // Open for a running page, Upgrade for an update, nothing for a tool-only add-on.
    await expect(tile(page, 'slack').getByRole('link', { name: 'Open Slack' })).toBeVisible()
    await expect(tile(page, 'sandbox').getByTestId('update-upgrade-card')).toBeVisible()
    await expect(tile(page, 'github').locator('.ad-tile__side .dk-btn:not(.action-menu__trigger)')).toHaveCount(0)
  })

  test('one strip names what needs setup, and its button opens the details', async ({ page }) => {
    await addonsScene(page)
    await page.goto('/app/addons')
    const strip = page.getByTestId('attention-setup')
    await expect(strip).toHaveCount(1)
    await expect(strip).toContainText('1 add-on needs setup')
    const line = strip.getByTestId('attention-connection')
    await expect(line).toHaveCount(1)
    await expect(line).toContainText('Telegram')
    await expect(tile(page, 'telegram').getByTestId('needs-setup-chip')).toBeVisible()
    await expect(tile(page, 'telegram').getByTestId('tile-fix')).toHaveText('Set up')
    await line.getByRole('button', { name: 'Set up Telegram' }).click()
    await expect(page).toHaveURL(/detail=telegram/)
    await expect(page.getByTestId('addon-detail')).toBeVisible()
  })

  test('there is no strip when every connection works', async ({ page }) => {
    await addonsScene(page, { connections: false })
    await page.goto('/app/addons')
    await expect(page.getByTestId('addon-tile').first()).toBeVisible()
    await expect(page.getByTestId('attention-setup')).toHaveCount(0)
    await expect(page.getByTestId('needs-setup-chip')).toHaveCount(0)
  })

  test('the details show the connections in words, and Remove comes last', async ({ page }) => {
    await addonsScene(page)
    await page.goto('/app/addons?detail=telegram')
    const pane = page.getByTestId('addon-detail')
    const conns = pane.getByTestId('addon-connections')
    await expect(conns.getByRole('heading', { name: 'Connections' })).toBeVisible()
    const one = conns.getByTestId('addon-connection')
    await expect(one).toHaveCount(1)
    await expect(one).toContainText('Support bot')
    await expect(one).toContainText('Answers as Support')
    await expect(one).toContainText('Accepts anyone')
    await expect(one).toContainText('Needs setup')
    await expect(one).not.toContainText('telegram ·')
    await expect(conns.getByTestId('addon-connections-manage')).toHaveAttribute('href', '/app/addons/telegram/bots')
    await expect(pane.getByRole('heading', { name: 'What it can do' })).toBeVisible()
    // Remove is the last thing in the body of the pane.
    const last = await pane.locator('.ad-pane__body > *').last().getAttribute('class')
    expect(last).toContain('ad-pane__danger')
    await expect(pane.getByTestId('pane-remove')).toBeVisible()
  })

  test('an add-on without a connector kind shows no connections section', async ({ page }) => {
    await addonsScene(page)
    await page.goto('/app/addons?detail=github')
    await expect(page.getByTestId('addon-detail')).toBeVisible()
    await expect(page.getByTestId('addon-connections')).toHaveCount(0)
  })
})

test.describe('Add-ons: icons', () => {
  test('an add-on with an icon file draws it; one without draws a tile with its letter', async ({ page }) => {
    await addonsScene(page)
    await page.goto('/app/addons')
    await expect(tile(page, 'slack').locator('img.ad-icon')).toHaveAttribute('src', /\/api\/addons\/apps\/slack\/icon/)
    // Calendar has no icon file: its first letter on a tile of its own colour.
    const calendar = tile(page, 'calendar').locator('.ad-icon')
    await expect(calendar).toHaveClass(/ad-icon--mark/)
    await expect(calendar).toHaveText('C')
    // So has a third-party add-on, on the same tile.
    const acme = tile(page, 'acme-crm').locator('.ad-icon')
    await expect(acme).toHaveClass(/ad-icon--mark/)
    await expect(acme).toHaveText('A')
  })

  test('an icon file that does not load falls back to the drawn tile', async ({ page }) => {
    await addonsScene(page)
    await page.route(/\/api\/addons\/apps\/github\/icon/, route => route.fulfill({ status: 403, body: '' }))
    await page.goto('/app/addons')
    const icon = tile(page, 'github').locator('.ad-icon')
    await expect(icon).toHaveClass(/ad-icon--mark/)
    await expect(icon).toHaveText('G')
  })

  test('a drawn tile has the same size and rounded shape as an icon file', async ({ page }) => {
    await addonsScene(page)
    await page.goto('/app/addons')
    const size = async (id) => tile(page, id).locator('.ad-icon').evaluate(el => { const r = el.getBoundingClientRect(); return [Math.round(r.width), Math.round(r.height), getComputedStyle(el).borderTopLeftRadius] })
    expect(await size('slack')).toEqual(await size('calendar'))
    expect(await size('slack')).toEqual(await size('acme-crm'))
  })
})

test.describe('Add-ons: find', () => {
  test('the library is a grid of cards with one control each, and a category narrows it', async ({ page }) => {
    await addonsScene(page)
    await page.goto('/app/addons?tab=browse')
    const cards = page.locator('article.ad-card')
    await expect(cards.first()).toBeVisible()
    for (const card of await cards.all()) {
      const controls = card.getByTestId('entry-control').locator('.dk-btn, .ui-chip')
      expect(await controls.count()).toBe(1)
    }
    const discord = page.locator('article.ad-card[data-addon="discord"]')
    await expect(discord.getByRole('button', { name: 'Install Discord' })).toBeVisible()
    await expect(discord.locator('.ad-icon')).toHaveText('D')
    await expect(page.locator('article.ad-card[data-addon="slack"]').getByTestId('entry-installed')).toBeVisible()
    await expect(page.locator('article.ad-card[data-addon="sandbox"]').getByTestId('update-upgrade-card')).toHaveText('Update to 0.7.0')
    // Category: Chat apps keeps what serves a connector kind (Slack, Telegram).
    // Discord is not installed, so the host tells nothing that makes it a chat app.
    await page.getByRole('button', { name: 'Chat apps', exact: true }).click()
    await expect(cards).toHaveCount(2)
    await expect(page.locator('article.ad-card[data-addon="slack"]')).toBeVisible()
    await expect(discord).toHaveCount(0)
    await expect(page.locator('article.ad-card[data-addon="github"]')).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'Home', exact: true })).toHaveCount(0)
    await page.getByRole('button', { name: 'All', exact: true }).click()
    await expect(cards.first()).toBeVisible()
  })

  test('a search with no match in the category says so', async ({ page }) => {
    await addonsScene(page)
    await page.goto('/app/addons?tab=browse')
    await expect(page.locator('article.ad-card').first()).toBeVisible()
    await page.getByRole('button', { name: 'Models', exact: true }).click()
    await page.getByRole('searchbox').or(page.getByPlaceholder('Search add-ons in your sources')).first().fill('discord')
    await expect(page.locator('article.ad-card')).toHaveCount(0)
    await expect(page.getByText('Nothing matches the search.')).toBeVisible()
  })
})

test.describe('Add-ons: connections elsewhere', () => {
  test('a connection row shows the app icon and readable facts', async ({ page }) => {
    await addonsScene(page)
    // Slack and Telegram are messaging kinds; the page opens on Accounts.
    await page.goto('/app/chats/connectors?tab=messaging')
    const rows = page.getByTestId('connector-row')
    await expect(rows).toHaveCount(CONNECTIONS.length)
    const slack = rows.filter({ hasText: 'Team workspace' })
    await expect(slack.locator('img.ad-icon')).toHaveAttribute('src', /\/api\/addons\/apps\/slack\/icon/)
    await expect(slack).toContainText('Answers as Research')
    await expect(slack).toContainText('Accepts 3 people')
    const telegram = rows.filter({ hasText: 'Support bot' })
    await expect(telegram).toContainText('Answers as Support')
    await expect(telegram).toContainText('Accepts anyone')
    await expect(telegram).not.toContainText('Agent:')
  })

  test('step 3 of the new-agent wizard shows the icon of each app', async ({ page }) => {
    await installAgents(page)
    await page.route(/\/api\/chats\/connectors\/kinds$/, r => r.fulfill({ json: { kinds: KINDS, capabilities: [] } }))
    await page.route('**/api/addons/apps', r => r.fulfill({ json: { apps: APPS } }))
    await page.route('**/api/addons/dispatch', r => r.fulfill({ json: { payload: { releases: [], total: 0 } } }))
    // The icon route answers a picture. Without it the server says 404, the
    // image fails and the row falls back to its letter tile, so the src
    // attribute exists only for the moment between the request and the answer.
    await page.route(/\/api\/addons\/apps\/[^/]+\/icon/, route => route.fulfill({ status: 200, contentType: 'image/png', body: Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==', 'base64') }))
    await page.goto('/app/agents/new')
    await page.getByLabel('Name', { exact: false }).first().fill('helper')
    await page.getByLabel('Instructions', { exact: false }).first().fill('Be brief.')
    await page.getByTestId('wizard-next').click()
    await page.getByTestId('wizard-next').click()
    const slack = page.getByTestId('wizard-group-chat').locator('[data-app="slack/slack"], [data-app="kind:slack/slack"]').first()
    await expect(slack.locator('.ad-icon')).toHaveAttribute('src', /\/api\/addons\/apps\/slack\/icon/)
  })
})
