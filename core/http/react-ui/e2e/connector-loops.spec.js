import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { installStudioAddons } from './studio-addon.js'
import { connectorApi, CAPABILITIES, ROLES_KIND, openEdit, saveSheet } from './connector-fixtures.js'

// The two switches of a binding for what the agent may do later by itself:
// wait for a change (loops.wait) and run on a timer (loops.start). The host
// marks both binding_only: they sit apart from the matrix, a role never gets
// them, and saving keeps the stored capability names. IDs and names are
// invented.

const PAGE = '/app/addons/slack/apps'
const SHOTS = process.env.UI_SHOTS_DIR
const LOOPS = [
  { name: 'loops.start', title: 'Let the agent run on a timer', description: 'The agent may start a loop in the chat. Only a verified person can use it. A loop started this way ends after a fifth of the runs that the server allows a loop. It is off until you turn it on.', sensitive: true, usual: false, binding_only: true },
  { name: 'loops.wait', title: 'Let the agent wait for a change', description: 'The agent may wait for a page, a tool, a job or its sub-agents. A wait that runs a command needs a verified person.', sensitive: false, usual: true, binding_only: true },
]
const BINDING = {
  id: 'b1', name: 'Team app', kind: 'slack', agent_id: 'a1', enabled: true, accept_any: false, allow: ['U001'], has_token: true,
  caps: ['memory.write', 'loops.wait'], grants: [], channels: [], paired: [], approval: { mode: 'ask' },
}
// A kind that lists a loop capability for its roles: the listing grants nothing.
const KIND = { ...ROLES_KIND, permissions: [...ROLES_KIND.permissions, { name: 'loops.wait', sensitive: false, binding_only: true }] }

for (const width of [1280, 390]) {
  test.describe(`loop switches at ${width} px`, () => {
    test.beforeEach(async ({ page }) => {
      await page.setViewportSize({ width, height: 900 })
      await mockChat(page)
      await installStudioAddons(page, { ids: ['slack'] })
    })

    test('shows the wait and the timer as two switches, outside the matrix, and saves the same names', async ({ page }) => {
      const a = connectorApi(page, [BINDING], 'slack', [], undefined, { capabilities: [...CAPABILITIES, ...LOOPS], kindExtra: KIND })
      await a.install()
      await page.goto(PAGE)
      await openEdit(page)
      const block = page.getByTestId('connector-loops')
      await expect(block).toBeVisible()
      // The wait first, then the timer, whatever order the host sent.
      await expect(block.locator('li')).toHaveCount(2)
      await expect(block.locator('li').nth(0)).toHaveAttribute('data-testid', 'connector-loop-loops.wait')
      await expect(block.locator('li').nth(1)).toHaveAttribute('data-testid', 'connector-loop-loops.start')
      await expect(block.getByTestId('connector-loop-switch-loops.wait')).toHaveAttribute('aria-checked', 'true')
      await expect(block.getByTestId('connector-loop-switch-loops.start')).toHaveAttribute('aria-checked', 'false')
      await expect(block.getByTestId('connector-loop-sensitive-loops.start')).toBeVisible()
      await expect(block.getByTestId('connector-loop-sensitive-loops.wait')).toHaveCount(0)
      await expect(block.getByTestId('connector-loop-loops.wait')).toContainText('needs a verified person')
      if (SHOTS) await block.screenshot({ path: `${SHOTS}/connector-loops-${width}.png` })

      // A switch does not move the preset to Custom.
      const before = await page.getByTestId('connector-preset').locator('input:checked').getAttribute('data-testid')
      await block.getByTestId('connector-loop-switch-loops.start').click()
      await expect(block.getByTestId('connector-loop-switch-loops.start')).toHaveAttribute('aria-checked', 'true')
      await expect(page.getByTestId('connector-preset').locator('input:checked')).toHaveAttribute('data-testid', before)

      // In the matrix, no loop row and no role box for them.
      await page.getByTestId('connector-preset-custom').check()
      const mx = page.getByTestId('connector-matrix')
      await expect(mx).toBeVisible()
      await expect(mx.getByTestId('connector-matrix-row-loops.wait')).toHaveCount(0)
      await expect(mx.getByTestId('connector-matrix-row-loops.start')).toHaveCount(0)
      await expect(page.locator('[data-testid^="connector-grant-"][data-testid$="loops.wait"]')).toHaveCount(0)
      if (SHOTS) await page.getByTestId('connector-section-may').screenshot({ path: `${SHOTS}/connector-may-${width}.png` })

      await saveSheet(page)
      await expect.poll(() => a.s.calls.find(c => c.method === 'PUT')?.body?.caps).toEqual(expect.arrayContaining(['memory.write', 'loops.wait', 'loops.start']))
      const put = a.s.calls.find(c => c.method === 'PUT').body
      expect((put.grants || []).flatMap(g => g.caps)).not.toContain('loops.wait')
    })
  })
}
