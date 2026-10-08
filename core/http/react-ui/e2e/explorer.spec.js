import { test, expect } from './coverage-fixtures.js'
import { NETWORKS } from './tools-fixtures.js'

// The Explorer page shows what a LocalAI explorer server returns from
// GET /networks and lists a swarm with POST /network/add. It claims nothing the
// two endpoints do not carry: no map, no latency, no worker details.

async function listNetworks(page, data = NETWORKS) {
  await page.route('**/networks', route => route.fulfill({ json: data }))
}

test.describe('Explorer', () => {
  test('lists the swarms, most workers first, with type, token and worker count', async ({ page }) => {
    await listNetworks(page)
    await page.goto('/explorer')
    await expect(page.getByRole('heading', { name: 'Public swarms' })).toBeVisible()
    await expect(page.getByText('3 swarms online')).toBeVisible()
    const rows = page.getByTestId('explorer-list').locator('li')
    await expect(rows).toHaveCount(3)
    await expect(rows.nth(0)).toContainText('Open weekend cluster')
    await expect(rows.nth(0)).toContainText('9 workers')
    await expect(rows.nth(1)).toContainText('Home lab swarm')
    await expect(rows.nth(1)).toContainText('federated')
    await expect(rows.nth(1)).toContainText('4 workers')
    await expect(rows.nth(2)).toContainText('2 workers')
    // The token is cut for display and never shown whole in the list.
    await expect(rows.nth(1)).toContainText('ab12cd…')
    await expect(rows.nth(1)).not.toContainText(NETWORKS[0].token)
    await expect(page.getByTestId('explorer-page')).toContainText('Use at your own risk')
  })

  test('How to join gives the token and, for a federated swarm, both commands', async ({ page }) => {
    await listNetworks(page)
    await page.goto('/explorer')
    await page.locator('[data-network="Home lab swarm"]').getByTestId('join-open').click()
    const sheet = page.getByTestId('join-sheet')
    await expect(sheet).toContainText('Join Home lab swarm')
    await expect(sheet).toContainText(NETWORKS[0].token)
    await expect(sheet).toContainText('LOCALAI_P2P_NETWORK_ID=home-lab')
    await expect(sheet).toContainText('docker run -d --restart=always')
    await expect(sheet).toContainText('local-ai federated --debug')
    await sheet.getByRole('button', { name: 'Close' }).last().click()
    await expect(sheet).toHaveCount(0)
  })

  test('a swarm that is not federated has the token and no commands', async ({ page }) => {
    await listNetworks(page)
    await page.goto('/explorer')
    await page.locator('[data-network="Studio render pool"]').getByTestId('join-open').click()
    await expect(page.getByTestId('join-sheet')).toContainText(NETWORKS[2].token)
    await expect(page.getByTestId('join-sheet')).not.toContainText('docker run')
    await expect(page.getByTestId('join-sheet')).toContainText('The token above is what a node needs to join.')
  })

  test('copying the token reports it', async ({ page, context }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write'])
    await listNetworks(page)
    await page.goto('/explorer')
    await page.locator('[data-network="Home lab swarm"]').getByTestId('join-open').click()
    await page.getByTestId('copy-token').click()
    await expect(page.getByTestId('copy-token')).toContainText('Copied')
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(NETWORKS[0].token)
  })

  test('List a swarm needs every field and the acknowledgement, then posts and refreshes', async ({ page }) => {
    let posted = null
    let list = NETWORKS.slice(1)
    await page.route('**/networks', route => route.fulfill({ json: list }))
    await page.route('**/network/add', route => { posted = route.request().postDataJSON(); list = NETWORKS; route.fulfill({ json: { message: 'Token added' } }) })
    await page.goto('/explorer')
    await expect(page.getByText('2 swarms online')).toBeVisible()
    await page.getByTestId('list-open').click()
    const submit = page.getByTestId('list-submit')
    await expect(submit).toBeDisabled()
    await page.fill('#ex-name', 'Home lab swarm')
    await page.fill('#ex-desc', 'Four machines.')
    await page.fill('#ex-token', 'dG9rZW4=')
    await expect(submit).toBeDisabled()
    await expect(page.getByTestId('list-sheet')).toContainText('Listing publishes the token')
    await page.getByTestId('list-understood').check()
    await expect(submit).toBeEnabled()
    await submit.click()
    await expect.poll(() => posted).toEqual({ name: 'Home lab swarm', description: 'Four machines.', token: 'dG9rZW4=' })
    await expect(page.getByTestId('explorer-notice')).toContainText('The swarm is listed.')
    await expect(page.getByText('3 swarms online')).toBeVisible()
  })

  test('a refused listing shows the server message and keeps the sheet', async ({ page }) => {
    await listNetworks(page)
    await page.route('**/network/add', route => route.fulfill({ status: 400, json: { error: 'Token already exists' } }))
    await page.goto('/explorer')
    await page.getByTestId('list-open').click()
    await page.fill('#ex-name', 'x')
    await page.fill('#ex-desc', 'y')
    await page.fill('#ex-token', 'dG9rZW4=')
    await page.getByTestId('list-understood').check()
    await page.getByTestId('list-submit').click()
    await expect(page.getByTestId('list-error')).toHaveText('Token already exists')
    await expect(page.getByTestId('list-sheet')).toBeVisible()
  })

  test('no swarm with workers: an empty state that names the next step', async ({ page }) => {
    await listNetworks(page, [])
    await page.goto('/explorer')
    await expect(page.getByTestId('explorer-empty')).toContainText('No swarm has workers online')
    await expect(page.getByTestId('explorer-empty').getByRole('button', { name: 'List a swarm' })).toBeVisible()
  })

  test('a server that is not an explorer says so', async ({ page }) => {
    await page.route('**/networks', route => route.fulfill({ status: 404, contentType: 'text/html', body: '<html>not found</html>' }))
    await page.goto('/explorer')
    await expect(page.getByTestId('explorer-off')).toContainText('This server is not an explorer')
    await expect(page.getByTestId('explorer-off')).toContainText('local-ai explorer')
    await expect(page.getByTestId('list-open')).toHaveCount(0)
  })

  test('an explorer that does not answer shows a retry', async ({ page }) => {
    let fail = true
    await page.route('**/networks', route => (fail ? route.abort() : route.fulfill({ json: NETWORKS })))
    await page.goto('/explorer')
    await expect(page.getByTestId('explorer-error')).toBeVisible()
    fail = false
    await page.getByRole('button', { name: 'Try again' }).click()
    await expect(page.getByTestId('explorer-list')).toBeVisible()
  })

  test('the page has no sidebar and links back to the app', async ({ page }) => {
    await listNetworks(page)
    await page.goto('/explorer')
    await expect(page.locator('.sidebar')).toHaveCount(0)
    await expect(page.getByRole('link', { name: /Open LocalAI/ }).first()).toHaveAttribute('href', '/app')
  })
})

test.describe('Explorer: phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('the list, the join sheet and the listing sheet fit the screen', async ({ page }) => {
    await listNetworks(page)
    await page.goto('/explorer')
    await expect(page.getByTestId('explorer-list')).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)).toBe(false)
    await page.locator('[data-network="Home lab swarm"]').getByTestId('join-open').click()
    await expect(page.getByTestId('join-sheet')).toBeVisible()
    const box = await page.getByTestId('join-sheet').boundingBox()
    expect(box.width).toBeLessThanOrEqual(390)
    expect(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)).toBe(false)
  })
})

test.describe('Explorer: reduced motion', () => {
  test('the page does not animate', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await listNetworks(page)
    await page.goto('/explorer')
    await expect(page.getByTestId('explorer-list')).toBeVisible()
    const duration = await page.getByTestId('explorer-list').locator('li').first().evaluate(el => getComputedStyle(el).transitionDuration)
    expect(parseFloat(duration)).toBeLessThan(0.01)
  })
})
