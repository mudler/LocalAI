import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { installStudioAddons } from './studio-addon.js'
import { connectorApi, previewFor, ROLES_KIND, openEdit, expandAll, openReadiness } from './connector-fixtures.js'

// The matrix "Who can do what" on the shared connection page (drawn here by
// the Slack add-on): the cells are what the preview of the host says, as an icon
// and a word. The page decides nothing. IDs and names are invented.

const PAGE = '/app/addons/slack/apps'
const SHOTS = process.env.UI_SHOTS_DIR
const BINDING = {
  id: 'b1', name: 'Team app', kind: 'slack', agent_id: 'a1', enabled: true, accept_any: false, allow: ['U001', 'C0123'], allow_trust: { C0123: 'act' }, has_token: true,
  caps: ['memory.write', 'triggers.propose', 'tools.write', 'talkback.new_channel'], grants: [], channels: [{ id: 'C0777', label: 'alerts', approved: true }], paired: [],
  approval: { mode: 'ask' },
}
// A preview with the extra data of the host: a note that waits for the owner, and the tools of an add-on.
const withExtras = (body) => {
  const v = previewFor(body, ROLES_KIND.roles)
  v.subjects = v.subjects.map((s) => ({ ...s, note_save: s.id === 'paired' ? 'now' : 'ask' }))
  v.addon_tools = [{ addon: 'tracker', capability: 'tools.write', tools: ['create-issue', 'comment-issue'], connections: [{ name: 'Work tracker', write_mode: 'propose' }] }]
  v.approvals = [{ tool: 'comment-issue', skip: true, runs_without_asking: ['owner', 'paired'] }]
  return v
}

for (const width of [1280, 375]) {
  test.describe(`matrix at ${width} px`, () => {
    test.beforeEach(async ({ page }) => {
      await page.setViewportSize({ width, height: 900 })
      await mockChat(page)
      await installStudioAddons(page, { ids: ['slack'] })
    })

    test('says yes, asks first, no, waiting and off, each as an icon and a word', async ({ page }) => {
      const a = connectorApi(page, [BINDING], 'slack', [], undefined, { kindExtra: ROLES_KIND, preview: withExtras })
      await a.install()
      await page.goto(PAGE)
      await openEdit(page)
      await expandAll(page)
      const mx = page.getByTestId('connector-matrix')
      await expect(mx).toBeVisible()
      await expect(page.getByTestId('connector-matrix-note')).toContainText('worked out from your settings')
      const cell = (row, col) => mx.getByTestId(`connector-matrix-cell-${row}-${col}`)
      if (width < 640) await mx.getByTestId('connector-matrix-sender').selectOption('paired')
      // On for this bot: the switch of the permission.
      await expect(mx.getByTestId('connector-cap-switch-memory.write')).toHaveAttribute('aria-checked', 'true')
      await expect(mx.getByTestId('connector-cap-switch-files.upload')).toHaveAttribute('aria-checked', 'false')
      // A verified person saves a note at once; a note of a listed person asks first (the host's note_save).
      await expect(cell('memory.write', 'paired')).toHaveAttribute('data-state', 'yes')
      await expect(cell('memory.write', 'paired')).toContainText('Yes')
      // The trigger approval mode of the form: ask.
      await expect(cell('triggers.propose', 'paired')).toHaveAttribute('data-state', 'asks')
      await expect(cell('triggers.propose', 'paired')).toContainText('Asks first')
      // Not ticked: the host says the connection does not allow it, which reads Off.
      await expect(cell('files.upload', 'paired')).toHaveAttribute('data-state', 'off')
      // The add-on tools, as sub-rows of the permission to change things in connected services.
      await expect(mx.getByTestId('connector-matrix-row-tools.write')).toContainText('Change things in connected services')
      await expect(mx.getByTestId('connector-matrix-row-tools.write:tracker:create-issue')).toContainText('tracker: create-issue')
      await expect(cell('tools.write:tracker:create-issue', 'paired')).toHaveAttribute('data-state', 'waiting')
      await expect(cell('tools.write:tracker:create-issue', 'paired')).toContainText('Waiting for a person')
      await expect(cell('tools.write:tracker:comment-issue', 'paired')).toHaveAttribute('data-state', 'yes', { timeout: 2000 })
      if (width >= 640) {
        await expect(cell('memory.write', 'listed')).toHaveAttribute('data-state', 'asks')
        await expect(cell('talkback.new_channel', 'listed')).toHaveAttribute('data-state', 'no')
        await expect(cell('talkback.new_channel', 'listed')).toContainText('No')
        await expect(cell('tools.write:tracker:comment-issue', 'listed')).toHaveAttribute('data-state', 'waiting')
        await expect(cell('memory.write', 'channel_acts')).toHaveAttribute('data-state', 'asks')
        await expect(mx.locator('th[data-col="anyone"]')).toHaveCount(0)
      }
      // Every cell has a word: none is colour alone.
      for (const c of await mx.locator('td[data-state]:visible').all()) await expect(c).not.toHaveText('')
      if (SHOTS) await mx.screenshot({ path: `${SHOTS}/matrix-${width}.png` })
    })

    test('selecting a No cell opens the message of the host, its fix and what was not checked, and the page changes nothing', async ({ page }) => {
      const a = connectorApi(page, [BINDING], 'slack', [], undefined, { kindExtra: ROLES_KIND, preview: withExtras })
      await a.install()
      await page.goto(PAGE)
      await openEdit(page)
      await expandAll(page)
      const mx = page.getByTestId('connector-matrix')
      await expect(mx.getByTestId('connector-matrix-detail')).toContainText('Choose a cell to read why')
      if (width < 640) await mx.getByTestId('connector-matrix-sender').selectOption('listed')
      await mx.getByTestId('connector-matrix-cell-talkback.new_channel-listed').click()
      const detail = mx.getByTestId('connector-matrix-detail')
      await expect(detail.getByTestId('connector-matrix-message')).toContainText('needs to be verified with this bot')
      await expect(detail.getByRole('button', { name: 'Verify a person' })).toBeVisible()
      await expect(mx.getByTestId('connector-matrix-cell-talkback.new_channel-listed')).toHaveAttribute('aria-pressed', 'true')
      // An Off cell opens its message too, with the checks that are left.
      await mx.getByTestId('connector-matrix-cell-files.upload-listed').click()
      await expect(detail.getByTestId('connector-matrix-message')).toContainText('does not allow the bot to receive files')
      expect(a.s.calls.filter((c) => c.method !== 'GET' && !c.url.includes('/preview'))).toEqual([])
      if (SHOTS) await mx.screenshot({ path: `${SHOTS}/matrix-detail-${width}.png` })
    })

    test('the switch is the ceiling and the role boxes are the grants: editing them changes the form and the preview, and nothing is saved', async ({ page }) => {
      const a = connectorApi(page, [BINDING], 'slack', [], undefined, { kindExtra: ROLES_KIND })
      await a.install()
      await page.goto(PAGE)
      await openEdit(page)
      await expandAll(page)
      const mx = page.getByTestId('connector-matrix')
      await expect(mx.getByTestId('connector-cap-switch-files.upload')).toHaveAttribute('aria-checked', 'false')
      await mx.getByTestId('connector-cap-switch-files.upload').click()
      await expect.poll(() => a.s.previews.at(-1)?.body.caps).toContain('files.upload')
      if (width >= 640) {
        await mx.getByTestId('connector-grant-workspace_admin-memory.write').check()
        await expect.poll(() => a.s.previews.at(-1)?.body.grants).toEqual([{ role: 'workspace_admin', caps: ['memory.write'] }])
        await expect(mx.getByTestId('connector-matrix-cell-memory.write-role:workspace_admin')).toHaveAttribute('data-state', 'yes')
        // A sensitive row has no box for a role.
        await expect(mx.getByTestId('connector-grant-workspace_admin-talkback.new_channel')).toHaveCount(0)
      } else {
        await mx.getByTestId('connector-matrix-sender').selectOption('role:workspace_admin')
        await expect(mx.getByTestId('connector-grant-workspace_admin-memory.write')).toBeVisible()
        await expect(mx.getByTestId('connector-grant-member-memory.write')).toBeHidden()
      }
      expect(a.s.calls.filter((c) => c.method !== 'GET' && !c.url.includes('/preview'))).toEqual([])
      if (SHOTS) await mx.screenshot({ path: `${SHOTS}/matrix-edit-${width}.png` })
    })

    test('a row opens a disclosure with its description, and the row for triggers holds the approval choices', async ({ page }) => {
      await connectorApi(page, [BINDING], 'slack', [], undefined, { kindExtra: ROLES_KIND }).install()
      await page.goto(PAGE)
      await openEdit(page)
      await expandAll(page)
      const mx = page.getByTestId('connector-matrix')
      await expect(mx.getByTestId('connector-approval')).toHaveCount(0)
      await mx.getByTestId('connector-matrix-more-memory.write').click()
      await expect(mx.getByTestId('connector-matrix-disclosure-memory.write')).toContainText('The agent may remember what a sender asks it to remember.')
      await mx.getByTestId('connector-matrix-more-triggers.propose').click()
      const d = mx.getByTestId('connector-matrix-disclosure-triggers.propose')
      await expect(d.getByTestId('connector-cap-effect-triggers.propose')).toHaveText('Proposals: asks first')
      await expect(d.getByTestId('connector-approval')).toBeVisible()
      await d.getByRole('radio', { name: /^Runs at once: it is set up/ }).check()
      await expect(d.getByTestId('connector-cap-effect-triggers.propose')).toHaveText('Proposals: runs at once')
      await mx.getByTestId('connector-matrix-more-triggers.propose').click()
      await expect(mx.getByTestId('connector-approval')).toHaveCount(0)
      if (SHOTS) await mx.screenshot({ path: `${SHOTS}/matrix-closed-${width}.png` })
    })

    test('shows the Anyone column only when Anyone is chosen, and sends the unsaved form to the preview', async ({ page }) => {
      const a = connectorApi(page, [BINDING], 'slack', [], undefined, { kindExtra: ROLES_KIND })
      await a.install()
      await page.goto(PAGE)
      await openEdit(page)
      await expandAll(page)
      await expect(page.getByTestId('connector-matrix')).toBeVisible()
      await expect.poll(() => a.s.previews.at(-1)?.body.channels).toEqual([{ id: 'C0777', label: 'alerts', approved: true }])
      expect(a.s.previews.at(-1).body.allow_trust).toEqual({ C0123: 'act' })
      expect(a.s.previews.at(-1).body.approval_mode).toBe('ask')
      // The form changes: a place is added and the approval mode is changed. The next preview carries both.
      await page.getByTestId('connector-matrix-more-triggers.propose').click()
      await page.getByRole('radio', { name: /^Runs at once: it is set up/ }).check()
      await expect.poll(() => a.s.previews.at(-1)?.body.approval_mode).toBe('auto')
      await page.getByRole('radio', { name: 'Anyone who can message it' }).check()
      await expect(page.getByTestId('connector-matrix').locator('th[data-col="anyone"]')).toHaveCount(1)
      await page.getByRole('radio', { name: 'Only people I choose' }).check()
      await expect(page.getByTestId('connector-matrix').locator('th[data-col="anyone"]')).toHaveCount(0)
    })

    test('the readiness list is a one-line status and a pill that counts what to fix, and still lists the rows that need attention', async ({ page }) => {
      const a = connectorApi(page, [], 'slack', [], undefined, { kindExtra: ROLES_KIND })
      await a.install()
      await page.goto(PAGE)
      await page.getByRole('button', { name: 'New Slack app' }).click()
      await expandAll(page)
      // A new connection starts with one quiet line, not the list.
      await expect(page.getByTestId('connector-readiness')).toHaveCount(0)
      await openReadiness(page)
      const ready = page.getByTestId('connector-readiness')
      await expect(ready.getByTestId('connector-ready-summary')).toHaveText('Not ready yet.')
      await expect(ready.getByTestId('connector-ready-pill')).toHaveText(/^\d+ things? to fix$/)
      await expect(ready.getByTestId('connector-ready-token')).toBeVisible()
      await expect(ready.getByTestId('connector-ready-agent')).toBeVisible()
      if (SHOTS) await ready.screenshot({ path: `${SHOTS}/readiness-${width}.png` })
    })
  })
}
