import { test, expect } from './coverage-fixtures.js'
import { installProjectLinks, SECRET } from './project-links-fixtures.js'

// The Connections and Automations tabs of a project.

const conn = (page, name) => page.getByTestId('project-connection').filter({ hasText: name })
const trig = (page, name) => page.getByTestId('project-trigger').filter({ hasText: name })
const loop = (page, name) => page.getByTestId('project-loop').filter({ hasText: name })

test.describe('Project connections', () => {
  test('lists the connections with kind, agent and state, and never draws a secret', async ({ page }) => {
    await installProjectLinks(page)
    await page.goto('/app/projects/p1?tab=connections')
    await expect(page.getByTestId('project-connection')).toHaveCount(3)
    const slack = conn(page, 'Design team channel')
    await expect(slack.getByTestId('project-connection-kind')).toHaveText('Slack')
    await expect(slack.getByTestId('project-connection-agent')).toHaveText('Answers as Copy editor')
    await expect(slack.getByTestId('project-connection-state')).toHaveText('On')
    await expect(slack.getByTestId('project-connection-open')).toHaveAttribute('href', '/app/addons/slack/connections?binding=b1')
    // A kind no running add-on serves shows its id and opens the gallery.
    const matrix = conn(page, 'Studio room')
    await expect(matrix.getByTestId('project-connection-kind')).toHaveText('matrix')
    await expect(matrix.getByTestId('project-connection-state')).toHaveText('Off')
    await expect(matrix.getByTestId('project-connection-open')).toHaveAttribute('href', '/app/addons?detail=matrix')
    await expect(page.locator('body')).not.toContainText(SECRET)
    expect(await page.content()).not.toContain(SECRET)
    // Tab 5 of 9 is the open one.
    await expect(page.getByRole('tab')).toHaveCount(9)
    await expect(page.getByTestId('project-tab-connections')).toHaveAttribute('aria-selected', 'true')
  })

  test('Connect lists the messaging kinds and goes to the setup of the kind with the only agent', async ({ page }) => {
    await installProjectLinks(page)
    await page.goto('/app/projects/p5?tab=connections')
    await expect(page.getByTestId('project-connections-empty')).toContainText('No messaging connections yet')
    await page.getByTestId('project-connect').click()
    const menu = page.getByRole('menu', { name: 'Connect a messaging app' })
    await expect(menu.getByRole('menuitem')).toHaveText(['Slack', 'Telegram'])
    await menu.getByRole('menuitem', { name: 'Slack' }).click()
    await expect(page).toHaveURL(/\/app\/addons\/slack\/connections\?new=1&agent=ap5$/)
  })

  test('with several agents Connect asks which one answers, then goes', async ({ page }) => {
    await installProjectLinks(page)
    await page.goto('/app/projects/p1?tab=connections')
    await page.getByTestId('project-connect').click()
    await page.getByRole('menuitem', { name: 'Telegram' }).click()
    const choose = page.getByTestId('project-connect-choose')
    await expect(choose).toBeVisible()
    // The unavailable agent is not offered.
    await expect(choose.getByRole('option')).toHaveText(['Copy editor', 'Asset checker'])
    await choose.getByTestId('project-connect-agent').selectOption({ label: 'Asset checker' })
    await choose.getByTestId('project-connect-go').click()
    await expect(page).toHaveURL(/\/app\/addons\/telegram\/connections\?new=1&agent=ap2$/)
  })

  test('with no kinds loaded Connect is disabled and nothing is reported', async ({ page }) => {
    const s = await installProjectLinks(page)
    s.links.kindsFail = true
    await page.goto('/app/projects/p1?tab=connections')
    await expect(page.getByTestId('project-connection')).toHaveCount(3)
    await expect(page.getByTestId('project-connect')).toBeDisabled()
    await expect(page.getByRole('alert')).toHaveCount(0)
    await expect(page.getByTestId('project-connections-hint')).toHaveCount(0)
  })

  test('with no messaging add-on installed Connect is disabled and says why', async ({ page }) => {
    await installProjectLinks(page, { kinds: [] })
    await page.goto('/app/projects/p1?tab=connections')
    await expect(page.getByTestId('project-connection')).toHaveCount(3)
    await expect(page.getByTestId('project-connect')).toBeDisabled()
    await expect(page.getByTestId('project-connections-hint')).toHaveText('No messaging add-ons are installed.')
  })

  test('the Connect button tells whether its menu is open and which element it opens', async ({ page }) => {
    await installProjectLinks(page)
    await page.goto('/app/projects/p5?tab=connections')
    const button = page.getByTestId('project-connect')
    await expect(button).toHaveAttribute('aria-expanded', 'false')
    await expect(button).not.toHaveAttribute('aria-controls', /./)
    await button.click()
    await expect(button).toHaveAttribute('aria-expanded', 'true')
    const menu = page.getByRole('menu', { name: 'Connect a messaging app' })
    await expect(menu).toBeVisible()
    const id = await button.getAttribute('aria-controls')
    expect(id).toBeTruthy()
    await expect(page.locator(`[id="${id}"]`)).toHaveAttribute('role', 'menu')
    await page.keyboard.press('Escape')
    await expect(button).toHaveAttribute('aria-expanded', 'false')
  })

  test('a late answer for the project before is not drawn on the project that was opened', async ({ page }) => {
    const s = await installProjectLinks(page)
    s.links.delay.connections = { p1: 2500 }
    await page.goto('/app/projects/p1?tab=connections')
    await expect.poll(() => s.links.calls.filter(c => c.name === 'connections' && c.id === 'p1').length).toBe(1)
    // Open another project in the same page, as a link in the app does.
    await page.evaluate(() => {
      window.history.pushState({}, '', '/app/projects/p5?tab=connections')
      window.dispatchEvent(new PopStateEvent('popstate'))
    })
    await expect(page.getByTestId('project-connections-empty')).toBeVisible()
    await page.waitForTimeout(3000)
    await expect(page.getByTestId('project-connection')).toHaveCount(0)
    await expect(page.getByTestId('project-connections-empty')).toBeVisible()
  })

  test('a poll tick is skipped while the page is hidden, and the page reads again when it shows', async ({ page }) => {
    const s = await installProjectLinks(page)
    await page.addInitScript(() => {
      Object.defineProperty(document, 'hidden', { configurable: true, get: () => window.__hidden === true })
    })
    await page.clock.install()
    await page.goto('/app/projects/p1?tab=connections')
    await expect(page.getByTestId('project-connection')).toHaveCount(3)
    const reads = () => s.links.calls.filter(c => c.name === 'connections').length
    const before = reads()
    await page.evaluate(() => { window.__hidden = true })
    await page.clock.runFor(31000)
    expect(reads()).toBe(before)
    await page.evaluate(() => { window.__hidden = false; document.dispatchEvent(new Event('visibilitychange')) })
    await expect.poll(reads).toBe(before + 1)
  })

  test('a project with no agents asks for one first, with one primary New agent', async ({ page }) => {
    await installProjectLinks(page)
    await page.goto('/app/projects/p2?tab=connections')
    const empty = page.getByTestId('project-connections-noagent')
    await expect(empty).toContainText('A project agent answers these messages. Create one first.')
    await expect(page.getByTestId('project-connect')).toHaveCount(0)
    await page.getByTestId('project-connections-new-agent').click()
    await expect(page).toHaveURL(/\/app\/agents\/new\?project=p2$/)
  })

  test('an archived project disables Connect and says why', async ({ page }) => {
    await installProjectLinks(page)
    await page.goto('/app/projects/p3?tab=connections')
    await expect(page.getByTestId('project-connect')).toBeDisabled()
    await expect(page.getByTestId('project-connections-hint')).toContainText('Restore it')
    await expect(page.getByTestId('project-connection')).toHaveCount(2)
  })

  test('an archived project shows an on connection as Paused and keeps one that was switched off as Off', async ({ page }) => {
    await installProjectLinks(page)
    await page.goto('/app/projects/p3?tab=connections')
    const on = conn(page, 'Podcast channel')
    await expect(on.getByTestId('project-connection-state')).toHaveText('Paused')
    await expect(on.getByTestId('project-connection-why')).toHaveText('Paused while the project is archived')
    const off = conn(page, 'Old bot')
    await expect(off.getByTestId('project-connection-state')).toHaveText('Off')
    await expect(off.getByTestId('project-connection-why')).toHaveCount(0)
  })

  test('a failed list shows an error, never an empty list', async ({ page }) => {
    const s = await installProjectLinks(page)
    s.links.fail.add('connections')
    await page.goto('/app/projects/p1?tab=connections')
    await expect(page.getByText('Could not load the connections of this project.')).toBeVisible()
    await expect(page.getByTestId('project-connections-empty')).toHaveCount(0)
    await expect(page.getByTestId('project-connection')).toHaveCount(0)
  })

  test('a failed poll keeps the rows and says the list may be out of date', async ({ page }) => {
    const s = await installProjectLinks(page)
    await page.clock.install()
    await page.goto('/app/projects/p1?tab=connections')
    await expect(page.getByTestId('project-connection')).toHaveCount(3)
    s.links.fail.add('connections')
    await page.clock.runFor(16000)
    await expect(page.getByText('Could not refresh the connections.')).toBeVisible()
    await expect(page.getByTestId('project-connection')).toHaveCount(3)
  })
})

test.describe('Project automations', () => {
  test('lists the triggers and the loops', async ({ page }) => {
    await installProjectLinks(page)
    await page.goto('/app/projects/p1?tab=automations')
    await expect(page.getByTestId('project-trigger')).toHaveCount(4)
    const morning = trig(page, 'Morning copy review')
    await expect(morning.getByTestId('project-trigger-kind')).toHaveText('Schedule')
    await expect(morning.getByTestId('project-trigger-state')).toHaveText('Active')
    await expect(morning.getByTestId('project-trigger-when')).toHaveText('daily 08:00')
    await expect(morning.getByTestId('project-trigger-agent')).toHaveText('Runs Copy editor')
    await expect(morning.getByTestId('project-trigger-next')).toContainText('Next run:')
    await expect(morning.getByTestId('project-trigger-open')).toHaveAttribute('href', '/app/triggers/t1')
    const files = trig(page, 'Check new images')
    await expect(files.getByTestId('project-trigger-kind')).toHaveText('Watches a folder')
    await expect(files.getByTestId('project-trigger-when')).toHaveText('/Projects/launch-site/files/incoming')
    await expect(trig(page, 'Weekly open questions').getByTestId('project-trigger-state')).toHaveText('Off')
    await expect(trig(page, 'Weekly open questions').getByTestId('project-trigger-next')).toHaveText('Next run: none')
    const hook = trig(page, 'Build failed hook')
    await expect(hook.getByTestId('project-trigger-kind')).toHaveText('Event')
    await expect(hook.getByTestId('project-trigger-kind')).not.toHaveText('Schedule')
    await expect(hook.getByTestId('project-trigger-when')).toHaveText('When an event arrives')
    await expect(hook.getByTestId('project-trigger-state')).toHaveText('Active')
    await expect(hook.getByTestId('project-trigger-next')).toHaveCount(0)
    await expect(hook).not.toContainText('Next run')
    await expect(page.getByTestId('project-loop')).toHaveCount(2)
    await expect(page.getByTestId('project-loops')).toContainText("Loops in chats of this project's agents.")
    const hero = loop(page, 'Homepage hero rewrite')
    await expect(hero.getByRole('link', { name: 'Homepage hero rewrite' })).toHaveAttribute('href', '/app/chats/c1')
    await expect(hero.getByTestId('project-loop-prompt')).toContainText('Review the hero copy')
    await expect(hero.getByTestId('project-loop-state')).toHaveText('Active')
    await expect(loop(page, 'Pricing page review').getByTestId('project-loop-state')).toHaveText('Ended')
    await expect(page.getByTestId('project-automations-archived')).toHaveCount(0)
  })

  test('an archived project pauses what is active and keeps what is off', async ({ page }) => {
    await installProjectLinks(page)
    await page.goto('/app/projects/p3?tab=automations')
    const active = trig(page, 'Transcript cleanup')
    await expect(active.getByTestId('project-trigger-state')).toHaveText('Paused')
    await expect(active.getByTestId('project-trigger-next')).toHaveText('Paused while the project is archived')
    await expect(trig(page, 'Old reminder').getByTestId('project-trigger-state')).toHaveText('Off')
    const l = loop(page, 'Episode outline')
    await expect(l.getByTestId('project-loop-state')).toHaveText('Paused')
    await expect(l.getByTestId('project-loop-why')).toHaveText('Paused while the project is archived')
    // One callout on this tab, with the restore action.
    await expect(page.getByTestId('project-archived')).toHaveCount(0)
    await expect(page.getByTestId('project-automations-archived')).toContainText('This project is archived.')
  })

  test('Restore project calls the restore endpoint and resumes the rows', async ({ page }) => {
    const s = await installProjectLinks(page)
    await page.goto('/app/projects/p3?tab=automations')
    await page.getByTestId('project-automations-restore').click()
    await expect(page.getByTestId('project-automations-archived')).toHaveCount(0)
    expect(s.projects.calls.some(c => c.method === 'POST' && c.path === '/api/chats/projects/p3/restore')).toBe(true)
    await expect(trig(page, 'Transcript cleanup').getByTestId('project-trigger-state')).toHaveText('Active')
  })

  test('empty lists say so', async ({ page }) => {
    await installProjectLinks(page)
    await page.goto('/app/projects/p2?tab=automations')
    await expect(page.getByTestId('project-triggers-empty')).toContainText('No triggers yet')
    await expect(page.getByTestId('project-loops-empty')).toContainText('No loops yet')
  })

  test('a failed read shows an error, not an empty list', async ({ page }) => {
    const s = await installProjectLinks(page)
    s.links.fail.add('triggers')
    await page.goto('/app/projects/p1?tab=automations')
    await expect(page.getByText('Could not load the automations of this project.')).toBeVisible()
    await expect(page.getByTestId('project-triggers-empty')).toHaveCount(0)
  })
})

test.describe('Project tabs on a phone', () => {
  test.use({ viewport: { width: 390, height: 800 } })
  test('the strip keeps the open tab in view', async ({ page }) => {
    await installProjectLinks(page)
    await page.goto('/app/projects/p1?tab=automations')
    const tab = page.getByTestId('project-tab-automations')
    await expect(tab).toBeInViewport({ ratio: 0.99 })
    await page.getByTestId('project-tab-tools').click()
    await expect(page.getByTestId('project-tab-tools')).toBeInViewport({ ratio: 0.99 })
    await page.getByTestId('project-tab-tools').press('ArrowLeft')
    await expect(page.getByTestId('project-tab-memory')).toBeInViewport({ ratio: 0.99 })
  })
})
