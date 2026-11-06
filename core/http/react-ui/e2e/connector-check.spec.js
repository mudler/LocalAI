import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { installStudioAddons } from './studio-addon.js'
import { connectorApi, ROLES_KIND, clippedOptions, openEdit, expandAll } from './connector-fixtures.js'

// "Check it" on the shared connection page (drawn here by the Slack add-on):
// Try it asks the preview of the host what a kind of sender may do and says
// it in one sentence, and sends nothing. The IDs and names are invented.

const PAGE = '/app/addons/slack/apps'
const BINDING = {
  id: 'b1', name: 'Team app', kind: 'slack', agent_id: 'a1', enabled: true, accept_any: false, allow: ['U001', 'U002', 'C0123'], allow_trust: { C0123: 'answer' }, has_token: true,
  caps: ['memory.write', 'files.upload', 'talkback.new_channel'], grants: [], channels: [],
  paired: [{ sender_id: 'U001', paired_at: '2026-11-01T10:00:00Z', expires_at: '2027-01-30T10:00:00Z' }],
}
const SENDERS = [
  { id: 'U001', name: 'Ada Example', count: 4, allowed: true, last: '2026-11-03T09:30:00Z', chat: 'D0AAA' },
  { id: 'U002', name: 'Grace Sample', count: 1, allowed: true, last: '2026-11-03T09:10:00Z', chat: 'C0123', chat_name: 'ops' },
]
const ACCESS = { binding: 'b1', subjects: [{ id: 'listed', title: 'A listed person', decisions: [] }], recent: [] }

test.describe('Check it', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
    await installStudioAddons(page, { ids: ['slack'] })
  })

  async function open(page, binding = BINDING) {
    const a = connectorApi(page, [binding], 'slack', SENDERS, undefined, { kindExtra: ROLES_KIND, access: ACCESS })
    await a.install()
    await page.goto(PAGE)
    await openEdit(page)
    await expandAll(page)
    await expect(page.getByTestId('connector-try')).toBeVisible()
    return a
  }

  test('Try it lists the people, the channels and the actions of the form, and says what the host answers in one sentence', async ({ page }) => {
    const a = await open(page)
    const who = page.getByTestId('connector-try-who')
    await expect(who.locator('option')).toHaveText(['Ada Example', 'Grace Sample', 'Someone in ops', 'Someone with the role Workspace admin', 'Someone with the role Channel member'])
    await expect(page.getByTestId('connector-try-place').locator('option')).toHaveText(['Direct message', 'ops'])
    await expect(page.getByTestId('connector-try-action').locator('option').first()).toHaveText('Receive files')
    const answer = page.getByTestId('connector-try-answer')
    // A verified person may save to memory.
    await page.getByTestId('connector-try-action').selectOption('memory.write')
    await expect(answer).toHaveAttribute('data-verdict', 'allowed')
    await expect(answer).toHaveText('Allowed. Ada Example is a verified person, writing in a direct message.')
    // A person who is only listed is refused a sensitive action, with the host sentence and its fix.
    await who.selectOption('person:U002')
    await page.getByTestId('connector-try-action').selectOption('talkback.new_channel')
    await expect(answer).toHaveAttribute('data-verdict', 'refused')
    await expect(answer).toContainText('Refused. Grace Sample is a listed person, writing in a direct message.')
    await expect(answer).toContainText('needs to be verified')
    await expect(answer.getByTestId('connector-fix')).toBeVisible()
    // Someone in a channel set to answer only is refused, and the place follows the channel.
    await who.selectOption('channel:C0123')
    await page.getByTestId('connector-try-action').selectOption('files.upload')
    await expect(page.getByTestId('connector-try-place')).toBeDisabled()
    await expect(page.getByTestId('connector-try-place')).toHaveValue('channel:C0123')
    await expect(answer).toHaveAttribute('data-verdict', 'refused')
    await expect(answer).toContainText('Someone in ops, where people only get answers.')
    await expect(page.getByTestId('connector-try-note')).toHaveText('Nothing is sent to the platform and nothing is saved.')
    // Nothing was saved and no message was sent: the calls are the reads and the previews.
    expect(a.s.calls.filter((c) => !['GET', 'POST'].includes(c.method))).toEqual([])
    expect(a.s.calls.filter((c) => c.method === 'POST' && !/\/access\/preview$/.test(c.url))).toEqual([])
  })

  test('Try it names the links of the check that were not looked at', async ({ page }) => {
    await open(page, { ...BINDING, agent_id: 'a3', caps: ['talkback.new_channel'] })
    await page.getByTestId('connector-try-who').selectOption('person:U002')
    await page.getByTestId('connector-try-action').selectOption('talkback.new_channel')
    await expect(page.getByTestId('connector-try-answer')).toHaveAttribute('data-verdict', 'refused')
    await expect(page.getByTestId('connector-try-then')).toContainText('Not checked yet:')
  })

  test('the answer follows the form without a save: setting the channel to let everyone act changes it', async ({ page }) => {
    await open(page)
    await page.getByTestId('connector-try-who').selectOption('channel:C0123')
    await page.getByTestId('connector-try-action').selectOption('memory.write')
    const answer = page.getByTestId('connector-try-answer')
    await expect(answer).toHaveAttribute('data-verdict', 'refused')
    await page.getByTestId('connector-trust-level').selectOption('act')
    await expect(answer).toHaveAttribute('data-verdict', 'allowed')
    await expect(answer).toContainText('Someone in ops, where everyone here can ask for actions.')
  })

  test('anyone appears only when the bot accepts anyone, and a bot with nobody listed says to add a person', async ({ page }) => {
    await open(page, { ...BINDING, accept_any: true })
    await expect(page.getByTestId('connector-try-who').locator('option')).toContainText(['Anyone'])
    await page.getByTestId('connector-try-who').selectOption('anyone')
    await expect(page.getByTestId('connector-try-answer')).toContainText('Anyone who can message the bot is let in.')
  })

  test('with nobody listed Try it says to add a person first', async ({ page }) => {
    await open(page, { ...BINDING, allow: [], paired: [] })
    await expect(page.getByTestId('connector-try-none')).toHaveText('Add a person first. Then you can try what they may do.')
  })
  // A select that cuts its option to "Post to a new ch" tells the reader the
  // wrong thing. Every option must fit, and the selected one first of all.
  for (const width of [1280, 375]) {
    test(`the selects of Try it show their whole option at ${width} wide`, async ({ page }) => {
      await page.setViewportSize({ width, height: 900 })
      await open(page)
      await page.getByTestId('connector-try-action').selectOption('talkback.new_channel')
      // The titles of the actions are the add-on's own words and all must fit.
      // The names of people, places and roles come from the platform and can be
      // any length, so only the one shown is held to it.
      for (const id of ['who', 'place', 'action']) {
        const select = page.getByTestId(`connector-try-${id}`)
        const m = await clippedOptions(select, { all: id === 'action' })
        expect(m.scrollWidth, `${id} scrolls its text`).toBeLessThanOrEqual(m.clientWidth)
        expect(m.out, `${id}: an option is cut when selected`).toEqual([])
      }
      if (process.env.UI_SHOTS_DIR) await page.getByTestId('connector-try').screenshot({ path: `${process.env.UI_SHOTS_DIR}/try-it-${width}.png` })
    })
  }
})
