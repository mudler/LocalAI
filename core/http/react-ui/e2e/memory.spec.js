import { test, expect } from './coverage-fixtures.js'
import { mockLibrary } from './library-fixtures.js'

// Memory library: collections, who uses each one, a question box that works
// on the collection alone, and what the API reports about sources and files.

test.describe('Memory library', () => {
  test('a collection is used by the agent that carries its name, and otherwise by nobody', async ({ page }) => {
    await mockLibrary(page)
    await page.goto('/app/collections')
    await expect(page.getByTestId('collection-used-handbook')).toHaveText('Used by handbook')
    await expect(page.getByTestId('collection-used-release-notes')).toHaveText('Not used yet')
    await expect(page.getByTestId('collection-used-meeting-notes')).toHaveText('Not used yet')
    for (const name of ['handbook', 'release-notes', 'meeting-notes']) {
      await expect(page.getByTestId(`collection-used-${name}`)).not.toContainText(/chat/i)
    }
  })

  test('an agent with the knowledge base off does not use its collection', async ({ page }) => {
    await mockLibrary(page, { collections: ['idle-agent'] })
    await page.goto('/app/collections')
    await expect(page.getByTestId('collection-used-idle-agent')).toHaveText('Not used yet')
  })

  test('the Used and Not used yet filters split the list', async ({ page }) => {
    await mockLibrary(page)
    await page.goto('/app/collections')
    await page.getByTestId('collections-filter-used').click()
    await expect(page.locator('[data-testid^="collection-row-"]')).toHaveCount(1)
    await page.getByTestId('collections-filter-unused').click()
    await expect(page.locator('[data-testid^="collection-row-"]')).toHaveCount(2)
    await page.getByTestId('collections-filter-all').click()
    await page.getByTestId('collections-filter').fill('notes')
    await expect(page.locator('[data-testid^="collection-row-"]')).toHaveCount(2)
    await page.getByTestId('collections-filter').fill('nothing-like-this')
    await expect(page.getByTestId('collections-none')).toBeVisible()
  })

  test('the pane lists web sources with their refresh interval and the files', async ({ page }) => {
    await mockLibrary(page)
    await page.goto('/app/collections/handbook')
    await expect(page.getByTestId('collection-title')).toHaveText('handbook')
    await expect(page.getByTestId('collection-counts')).toHaveText('2 files, 1 web sources')
    const sources = page.getByTestId('sources-list')
    await expect(sources).toContainText('https://docs.example.org/guide')
    await expect(sources).toContainText('refreshes every 60 min')
    await expect(page.getByTestId('entries-list')).toContainText('employee-handbook.pdf')
    await expect(page.getByTestId('entries-list')).toContainText('vpn-setup.md')
  })

  test('a standalone collection says it is not used yet and offers to try it', async ({ page }) => {
    await mockLibrary(page)
    await page.goto('/app/collections/release-notes')
    await expect(page.getByTestId('collection-not-used')).toContainText('It stays in the library until you add it somewhere')
    await expect(page.getByTestId('used-by-none')).toHaveText('Not used yet')
    await expect(page.getByTestId('collection-try')).toBeVisible()
    await expect(page.getByTestId('entries-none')).toBeVisible()
  })

  test('a question runs on the collection alone and shows ranked passages with scores', async ({ page }) => {
    const { seen } = await mockLibrary(page)
    await page.goto('/app/collections/handbook')
    await page.locator('#search-query').fill('How many days can I work from home?')
    await page.getByRole('button', { name: 'Search', exact: true }).click()
    const results = page.getByTestId('ask-results')
    await expect(results).toContainText('3 passages, ranked by similarity')
    await expect(results.locator('.lib-passage').first()).toContainText('Remote work')
    await expect(results.locator('.lib-passage').first()).toContainText('0.74')
    await expect(results.locator('.lib-passage').nth(2)).toContainText('0.51')
    expect(seen.searches).toEqual([{ name: 'handbook', query: 'How many days can I work from home?', max_results: 10 }])
  })

  test('the number of passages is tunable and nothing returned says so', async ({ page }) => {
    const { seen } = await mockLibrary(page, { passages: [] })
    await page.goto('/app/collections/handbook')
    await page.getByText('Tune the search').click()
    await page.locator('#search-max').fill('2')
    await page.locator('#search-query').fill('anything')
    await page.getByRole('button', { name: 'Search', exact: true }).click()
    await expect(page.getByTestId('ask-none')).toBeVisible()
    expect(seen.searches[0].max_results).toBe(2)
  })

  test('adding a web source posts the URL and the interval, and the list shows it', async ({ page }) => {
    const { seen } = await mockLibrary(page)
    await page.goto('/app/collections/handbook')
    await page.getByTestId('add-source-toggle').click()
    await page.locator('#source-url').fill('https://example.org/feed')
    await page.locator('#source-interval').fill('30')
    await page.getByRole('button', { name: 'Add URL' }).click()
    await expect.poll(() => seen.sourcePosts.length).toBe(1)
    expect(seen.sourcePosts[0]).toEqual({ name: 'handbook', url: 'https://example.org/feed', update_interval: 30 })
    await expect(page.getByTestId('sources-list')).toContainText('https://example.org/feed')
    await expect(page.getByTestId('sources-list')).toContainText('not fetched yet')
  })

  test('removing a source asks first', async ({ page }) => {
    await mockLibrary(page)
    await page.goto('/app/collections/handbook')
    await page.getByRole('button', { name: 'Remove https://docs.example.org/guide' }).click()
    await page.getByRole('button', { name: 'Remove', exact: true }).click()
    await expect(page.getByTestId('sources-none')).toBeVisible()
  })

  test('uploading a file adds it to the files', async ({ page }) => {
    const { seen } = await mockLibrary(page)
    await page.goto('/app/collections/handbook')
    await page.getByTestId('add-source-toggle').click()
    await page.locator('#upload-file').setInputFiles({ name: 'notes.txt', mimeType: 'text/plain', buffer: Buffer.from('hello') })
    await page.getByRole('button', { name: 'Upload' }).click()
    await expect.poll(() => seen.uploads).toEqual(['handbook'])
    await expect(page.getByTestId('entries-list')).toContainText('uploaded.txt')
  })

  test('a failed upload shows the server message and can be retried or dismissed', async ({ page }) => {
    const { seen } = await mockLibrary(page, { uploadError: 'embedding dimension mismatch' })
    await page.goto('/app/collections/handbook')
    await page.getByTestId('add-source-toggle').click()
    await page.locator('#upload-file').setInputFiles({ name: 'scan.pdf', mimeType: 'application/pdf', buffer: Buffer.from('x') })
    await page.getByRole('button', { name: 'Upload' }).click()
    await expect(page.getByTestId('upload-error')).toHaveText('embedding dimension mismatch')
    await expect(page.getByTestId('collection-failed-badge')).toHaveText('1 failed')
    await page.getByRole('button', { name: 'Retry' }).click()
    await expect.poll(() => seen.uploads.length).toBe(2)
    await expect(page.getByTestId('upload-error')).toBeVisible()
    await page.getByRole('button', { name: 'Dismiss' }).click()
    await expect(page.getByTestId('uploads-list')).toHaveCount(0)
  })

  test('an entry can be opened and closed with Escape', async ({ page }) => {
    await mockLibrary(page)
    await page.goto('/app/collections/handbook')
    await page.getByRole('button', { name: 'View vpn-setup.md' }).click()
    const dialog = page.getByTestId('entry-dialog')
    await expect(dialog).toContainText('12 chunks')
    await expect(dialog).toContainText('Full text of the entry.')
    await page.keyboard.press('Escape')
    await expect(dialog).toHaveCount(0)
  })

  test('Add to... offers only the agent that carries the collection name', async ({ page }) => {
    const { seen } = await mockLibrary(page, { collections: ['idle-agent', 'release-notes'] })
    await page.goto('/app/collections/idle-agent')
    await page.getByTestId('add-to-button').click()
    const menu = page.getByTestId('add-to-menu')
    await expect(menu).toContainText('An agent reads the collection that has its own name')
    await expect(menu.getByTestId('add-to-idle-agent')).toContainText('Turns its knowledge base on')
    await expect(menu.locator('[data-testid^="add-to-"][data-testid$="research-assistant"]')).toHaveCount(0)
    await menu.getByRole('button', { name: 'Add to idle-agent' }).click()
    await expect.poll(() => seen.saves.length).toBe(1)
    expect(seen.saves[0].body).toMatchObject({ name: 'idle-agent', enable_kb: true })
    await expect(page.getByTestId('collection-used-idle-agent')).toHaveText('Used by idle-agent')
  })

  test('with no agent of that name Add to... says how a collection gets used', async ({ page }) => {
    await mockLibrary(page)
    await page.goto('/app/collections/release-notes')
    await page.getByTestId('add-to-button').click()
    await expect(page.getByTestId('add-to-menu')).toContainText('No agent is named release-notes')
  })

  test('removing a collection from an agent turns its knowledge base off, and Undo restores it', async ({ page }) => {
    const { seen } = await mockLibrary(page)
    await page.goto('/app/collections/handbook')
    await page.getByRole('button', { name: 'Remove from handbook' }).click()
    await expect.poll(() => seen.saves.length).toBe(1)
    expect(seen.saves[0].body).toMatchObject({ name: 'handbook', enable_kb: false })
    await expect(page.getByTestId('collection-used-handbook')).toHaveText('Not used yet')
    await page.getByTestId('library-undo-toast').getByRole('button', { name: 'Undo' }).click()
    await expect.poll(() => seen.saves.length).toBe(2)
    expect(seen.saves[1].body).toMatchObject({ name: 'handbook', enable_kb: true })
  })

  test('the disclosure names the endpoints and says where files stay', async ({ page }) => {
    await mockLibrary(page)
    await page.goto('/app/collections/handbook')
    const api = page.getByTestId('collection-api')
    await api.getByText('Index, API and privacy').click()
    await expect(api).toContainText('/api/agents/collections/handbook/search')
    await expect(api).toContainText('/api/agents/collections/handbook/upload')
    await expect(api).toContainText('stay on this server')
  })

  test('a new collection is created and opened', async ({ page }) => {
    const { seen } = await mockLibrary(page)
    await page.goto('/app/collections')
    await page.getByTestId('new-collection').click()
    await page.getByLabel('New collection name').fill('field-notes')
    await page.getByTestId('create-form').getByRole('button', { name: 'Create' }).click()
    await expect.poll(() => seen.creates).toEqual(['field-notes'])
    await expect(page).toHaveURL(/\/app\/collections\/field-notes$/)
  })

  test('an empty library teaches what memory is', async ({ page }) => {
    await mockLibrary(page, { collections: [] })
    await page.goto('/app/collections')
    const empty = page.getByTestId('memory-empty')
    await expect(empty.getByRole('heading', { name: 'Memory holds documents an agent can search' })).toBeVisible()
    await empty.getByRole('button', { name: 'New collection' }).click()
    await expect(page.getByLabel('New collection name')).toBeVisible()
  })

  test('Reset asks first and empties the collection', async ({ page }) => {
    const { seen } = await mockLibrary(page)
    await page.goto('/app/collections/handbook')
    await page.getByRole('button', { name: 'Reset' }).click()
    await page.getByRole('button', { name: 'Remove entries' }).click()
    await expect.poll(() => seen.resets).toEqual(['handbook'])
  })

  test('on a phone the list and the collection take turns', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await mockLibrary(page)
    await page.goto('/app/collections')
    await expect(page.getByTestId('collection-row-handbook')).toBeVisible()
    await expect(page.getByTestId('collection-pane')).toBeHidden()
    await page.getByTestId('collection-row-handbook').click()
    await expect(page.getByTestId('collection-title')).toBeVisible()
    await page.getByRole('button', { name: 'All collections' }).click()
    await expect(page.getByTestId('collection-row-handbook')).toBeVisible()
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(0)
  })
})
