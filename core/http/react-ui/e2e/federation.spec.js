import { test, expect } from './coverage-fixtures.js'
import { mockAdmin } from './p2p-share-fixtures.js'
import { mockFederation } from './federation-fixtures.js'

// Swarm, P2P, Federation: the groups (one model name served by several
// LocalAI instances) with their routing and members, drain, a new group, a
// delete, and the instances with how this server reaches each one.

async function openFederation(page, options) {
  await mockAdmin(page)
  const state = await mockFederation(page, options)
  await page.goto('/app/p2p/federation')
  await expect(page.getByTestId('federation-page')).toBeVisible({ timeout: 15_000 })
  return state
}

test.describe('Federation', () => {
  test('shows a group with its members and the instances with their path', async ({ page }) => {
    await openFederation(page)
    await expect(page.getByTestId('p2p-tabs').getByRole('link', { name: 'Federation' })).toHaveAttribute('aria-current', 'page')
    const group = page.getByTestId('federation-group-shared-qwen')
    await expect(group).toContainText('3 in service')
    await expect(group.getByTestId('federation-policy-least_busy')).toHaveAttribute('aria-checked', 'true')
    await expect(group.getByTestId('federation-member-qwen3-8b')).toContainText('This instance')
    await expect(group.getByTestId('federation-member-branch-qwen3-8b')).toContainText('Branch LocalAI')
    await expect(group.getByTestId('federation-member-field-qwen3-8b')).toContainText('Small requests only')
    const instances = page.getByTestId('federation-instances')
    await expect(instances.getByTestId('federation-instance-local')).toContainText('This instance')
    await expect(instances.getByTestId('federation-instance-r-branch')).toContainText('Hole-punched')
    await expect(instances.getByTestId('federation-instance-r-field')).toContainText('Via a relay')
    await expect(instances.getByTestId('federation-instance-r-field')).toContainText('Limited')
    await expect(page.getByTestId('federation-add-instance')).toHaveAttribute('href', '/app/models?view=remotes')
  })

  test('changes the routing at once', async ({ page }) => {
    const state = await openFederation(page)
    await page.getByTestId('federation-policy-round_robin').click()
    await expect.poll(() => state.calls.filter(c => c.method === 'PATCH').map(c => c.body)).toEqual([{ policy: 'round_robin' }])
    await expect(page.getByTestId('federation-policy-round_robin')).toHaveAttribute('aria-checked', 'true')
  })

  test('drains a member and puts it back, and keeps the last one in service', async ({ page }) => {
    const state = await openFederation(page)
    await page.getByTestId('federation-drain-branch-qwen3-8b').click()
    await expect(page.getByTestId('federation-member-branch-qwen3-8b')).toContainText('Drained')
    await expect(page.getByTestId('federation-group-shared-qwen')).toContainText('2 in service')
    await page.getByTestId('federation-drain-branch-qwen3-8b').click()
    await expect(page.getByTestId('federation-member-branch-qwen3-8b')).toContainText('In use')
    expect(state.calls.map(c => c.body)).toEqual([{ model: 'branch-qwen3-8b', drain: true }, { model: 'branch-qwen3-8b', drain: false }])

    await page.getByTestId('federation-drain-qwen3-8b').click()
    await expect(page.getByTestId('federation-member-qwen3-8b')).toContainText('Drained')
    await page.getByTestId('federation-drain-field-qwen3-8b').click()
    await expect(page.getByTestId('federation-member-field-qwen3-8b')).toContainText('Drained')
    await expect(page.getByTestId('federation-drain-branch-qwen3-8b')).toBeDisabled()
  })

  test('makes a group from the same model on two instances', async ({ page }) => {
    const state = await openFederation(page, { groups: [] })
    const empty = page.getByTestId('federation-groups-empty')
    await expect(empty).toContainText('No groups yet')
    await expect(empty).toContainText('2 other instances')
    await empty.getByTestId('federation-new').click()
    const sheet = page.getByTestId('federation-sheet')
    await expect(sheet.getByTestId('federation-save')).toBeDisabled()
    await sheet.getByTestId('federation-pick-qwen3-8b').check()
    await expect(sheet.getByTestId('federation-name')).toHaveValue('qwen3-8b')
    await sheet.getByTestId('federation-name').fill('shared-qwen')
    await sheet.getByTestId('federation-pick-branch-qwen3-8b').check()
    await sheet.getByTestId('federation-sheet-policy-round_robin').click()
    await sheet.getByTestId('federation-save').click()
    await expect(sheet).toHaveCount(0)
    expect(state.calls[0]).toEqual({ method: 'POST', path: '/groups', body: { name: 'shared-qwen', policy: 'round_robin', members: ['qwen3-8b', 'branch-qwen3-8b'] } })
    await expect(page.getByTestId('federation-group-shared-qwen')).toBeVisible()
  })

  test('shows the reason when the server refuses a group', async ({ page }) => {
    await openFederation(page)
    await page.getByTestId('federation-new').click()
    const sheet = page.getByTestId('federation-sheet')
    await sheet.getByTestId('federation-pick-qwen3-8b').check()
    await sheet.getByTestId('federation-name').fill('shared-qwen')
    await sheet.getByTestId('federation-pick-field-qwen3-8b').check()
    await sheet.getByTestId('federation-save').click()
    await expect(sheet.getByTestId('federation-error')).toContainText('already exists')
  })

  test('deletes a group after a confirmation', async ({ page }) => {
    const state = await openFederation(page)
    await page.getByTestId('federation-group-delete').click()
    const dialog = page.getByTestId('federation-delete-dialog')
    await expect(dialog).toContainText('Delete the group shared-qwen?')
    await expect(dialog.getByRole('button', { name: 'Cancel' })).toBeFocused()
    await dialog.getByTestId('federation-delete-confirm').click()
    await expect(dialog).toHaveCount(0)
    expect(state.calls).toEqual([{ method: 'DELETE', path: '/groups/shared-qwen', body: undefined }])
    await expect(page.getByTestId('federation-groups-empty')).toBeVisible()
  })
})
