import { test, expect } from './coverage-fixtures.js'

// Sharing a collection from the Knowledge page: the owner opens the Share
// dialog with the roles view and edit and the grant to everyone; a collection
// shared with the person is listed apart, flagged with its owner and role,
// and is read only except that an editor adds and removes files. The API is
// stubbed.

const RID = 'r0123456789abcdef0123456789abcde'
const SHARED_VIEW = { name: 'handbook', title: 'handbook', owner: 'Alice Martin', role: 'viewer' }
const SHARED_EDIT = { name: `shared:${RID}`, title: 'manuals', owner: 'Carla Rossi', role: 'editor', everyone: true }

async function api(page, { shared = [SHARED_VIEW, SHARED_EDIT], shares = {}, sharing = true } = {}) {
  const s = { calls: [] }
  await page.route(/\/api\/collections(\?.*)?$/, r => r.fulfill({ json: { collections: ['manuals'], shared, shares, sharing, embedding_model_installed: true } }))
  await page.route(/\/api\/collections\/[^/]+\/entries(\?.*)?$/, r => r.fulfill({ json: { entries: ['a1b2/guide.txt'], count: 1 } }))
  await page.route(/\/api\/collections\/[^/]+\/sources(\?.*)?$/, r => r.fulfill({ json: { sources: [{ url: 'https://example.com/feed', update_interval: 60 }], count: 1 } }))
  await page.route(/\/api\/collections\/[^/]+\/share$/, r => {
    s.calls.push(`share ${decodeURIComponent(new URL(r.request().url()).pathname.split('/')[3])}`)
    return r.fulfill({ json: { resource_id: RID, shared_with: 0 } })
  })
  await page.route(`**/api/shares/collection/${RID}/grants`, r => {
    s.calls.push(`grants ${r.request().headers()['x-localai-confirm']}`)
    return r.fulfill({ json: { kind: 'collection', resourceId: RID, roles: ['viewer', 'editor'], everyone: true, grants: [{ userId: '@everyone', displayName: '', role: 'viewer' }] } })
  })
  return s
}

test.describe('Sharing collections', () => {
  test('lists the collections shared with the person, with the owner and the role', async ({ page }) => {
    await api(page)
    await page.goto('/app/collections')
    const shared = page.getByTestId('collections-shared')
    await expect(shared.getByRole('heading', { name: 'Shared with you' })).toBeVisible()
    const view = shared.getByTestId('collection-shared-row-handbook')
    await expect(view).toContainText('handbook')
    await expect(view.getByTestId('shared-by-line')).toHaveText('Shared by Alice Martin')
    await expect(view.getByTestId('role-badge')).toHaveText('Can view')
    const edit = shared.getByTestId(`collection-shared-row-shared:${RID}`)
    await expect(edit).toContainText('manuals')
    await expect(edit.getByTestId('shared-by-line')).toHaveText('Shared with everyone by Carla Rossi')
    await expect(edit.getByTestId('role-badge')).toHaveText('Can edit')
  })

  test('opens a viewer collection read only: no add, reset, delete or share', async ({ page }) => {
    await api(page)
    await page.goto('/app/collections/handbook')
    const pane = page.getByTestId('collection-pane')
    await expect(pane.getByTestId('collection-title')).toHaveText('handbook')
    await expect(pane.getByTestId('collection-shared-by')).toContainText('Shared by Alice Martin')
    await expect(pane.getByTestId('collection-shared-note')).toHaveText('You can search it and read its files.')
    await expect(pane.getByTestId('add-source-toggle')).toHaveCount(0)
    await expect(pane.getByRole('button', { name: 'Reset' })).toHaveCount(0)
    await expect(pane.getByTestId('collection-share')).toHaveCount(0)
    await expect(pane.getByRole('button', { name: /^Delete/ })).toHaveCount(0)
    await expect(pane.getByRole('button', { name: /^Remove/ })).toHaveCount(0)
    await expect(pane.getByRole('button', { name: /^View/ })).toHaveCount(1)
    await expect(pane.getByRole('button', { name: 'Search' })).toBeVisible()
  })

  test('lets an editor add and remove files, and not sources, reset or share', async ({ page }) => {
    await api(page)
    await page.goto(`/app/collections/${encodeURIComponent(`shared:${RID}`)}`)
    const pane = page.getByTestId('collection-pane')
    await expect(pane.getByTestId('collection-title')).toHaveText('manuals')
    await pane.getByTestId('add-source-toggle').click()
    const adding = pane.getByTestId('add-source-panel')
    await expect(adding.getByLabel('A file')).toBeVisible()
    await expect(adding.getByLabel('A web page or feed')).toHaveCount(0)
    await expect(pane.getByRole('button', { name: /^Delete/ })).toHaveCount(1)
    await expect(pane.getByRole('button', { name: 'Reset' })).toHaveCount(0)
    await expect(pane.getByTestId('collection-share')).toHaveCount(0)
  })

  test('opens the Share dialog of an own collection with view, edit and everyone', async ({ page }) => {
    const s = await api(page)
    await page.goto('/app/collections/manuals')
    await page.getByTestId('collection-share').click()
    const dialog = page.getByTestId('collection-share-dialog')
    await expect(dialog.getByRole('heading', { name: 'Share "manuals"' })).toBeVisible()
    // The grant to everyone is allowed for a collection, and this one has it.
    await expect(dialog.getByTestId('share-grant')).toContainText('Everyone')
    expect(s.calls).toEqual(['share manuals', `grants ${RID}`])
  })

  test('shows nothing of sharing when the server cannot share', async ({ page }) => {
    await api(page, { sharing: false })
    await page.goto('/app/collections/manuals')
    await expect(page.getByTestId('collection-title')).toHaveText('manuals')
    await expect(page.getByTestId('collection-share')).toHaveCount(0)
    await expect(page.getByTestId('collections-shared')).toHaveCount(0)
  })
})
