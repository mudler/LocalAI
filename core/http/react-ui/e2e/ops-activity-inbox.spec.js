import { test, expect } from './coverage-fixtures.js'
import { inboxButton, makeInbox, stubInbox } from './inbox-fixtures.js'
import { MEMBER, statusFixture } from './access-fixtures.js'

// Operations in the notification inbox: the Activity tab of the panel, the
// ring on the entry and the bell, and what an administrator and everyone else
// see. The API, the inbox and the stream are stubbed. The full-width strip
// these replace is gone.

const GB = 1024 ** 3
const ago = (min) => new Date(Date.now() - min * 60_000).toISOString()

const op = (id, over = {}) => ({
  id, name: id, fullName: id, jobID: `job-${id}`, progress: 40, taskType: 'installation',
  isDeletion: false, isBackend: false, isQueued: false, cancellable: true, phase: 'downloading', ...over,
})

const record = (id, over = {}) => ({
  id, name: id, jobID: `rec-${id}`, isBackend: false, taskType: 'installation', outcome: 'completed',
  startedAt: ago(8), finishedAt: ago(3), ...over,
})

// The state the stubs answer from; a spec changes it and the next poll shows it.
function makeOps({ operations = [], history = [] } = {}) {
  return { operations, history, calls: [] }
}

async function stubOps(page, ops) {
  await page.route('**/api/operations', (route) => route.fulfill({ json: { operations: ops.operations } }))
  await page.route('**/api/operations/history', (route) => route.fulfill({ json: { operations: ops.history } }))
  await page.route('**/api/operations/*/*', (route) => {
    ops.calls.push(`${route.request().method()} ${new URL(route.request().url()).pathname}`)
    return route.fulfill({ json: {} })
  })
}

const panelOf = (page) => page.getByRole('dialog', { name: 'Notifications' })

const unreadInbox = () => {
  const inbox = makeInbox(0)
  inbox.rows = [
    { id: 2, kind: 'needs_attention', status: 'ok', source: 'system', title: 'Two updates are ready', body: '', link: '/app/models', read: false, created: ago(30) },
    { id: 1, kind: 'run_ended', status: 'ok', source: 'trigger', title: 'Old run', body: '', link: '/app/chats/r1', read: true, created: ago(60 * 30) },
  ]
  return inbox
}

const busy = () => makeOps({
  operations: [
    op('qwen3-14b', { progress: 48, currentBytes: 1.2 * GB, totalBytes: 1.9 * GB, bytesPerSecond: 48 * 1024 * 1024 }),
    op('llama-cpp', {
      isBackend: true, taskType: 'upgrade', progress: 20, phase: 'downloading',
      nodes: ['success', 'success', 'downloading', 'queued', 'queued'].map((status, i) => ({ node_id: `n${i}`, node_name: `node-${i}`, status })),
    }),
    op('whisper', { isQueued: true, progress: 0, phase: 'queued', message: 'queued' }),
    op('sherpa-onnx', { isBackend: true, fullName: 'localai@sherpa-onnx', progress: 0, cancellable: false, error: 'no space left on device' }),
  ],
  history: [
    record('gemma-3-4b', { finishedAt: ago(3) }),
    record('vllm', { isBackend: true, taskType: 'upgrade', finishedAt: ago(12) }),
    record('piper', { taskType: 'deletion', finishedAt: ago(40) }),
    record('cancelled-one', { outcome: 'cancelled', finishedAt: ago(1) }),
    record('failed-one', { outcome: 'failed', error: 'boom', finishedAt: ago(2) }),
  ],
})

async function openPanel(page) {
  await page.goto('/app')
  await inboxButton(page).click()
  await panelOf(page).waitFor()
}

test.describe('Operations in the inbox: the strip is gone', () => {
  test('no strip on any page while operations run, and the entry carries them', async ({ page }) => {
    await stubInbox(page, unreadInbox())
    await stubOps(page, busy())
    await page.goto('/app/models')
    await expect(page.getByTestId('inbox-ring')).toBeVisible()
    await expect(page.locator('.operations-strip')).toHaveCount(0)
    // The page content starts at the top: nothing is stacked above the header.
    const top = await page.locator('main h1').first().boundingBox()
    expect(top.y).toBeLessThan(120)
  })
})

test.describe('Operations in the inbox: the Activity tab', () => {
  test('an administrator opens on Activity with failed, running, queued and recent rows', async ({ page }) => {
    await stubInbox(page, unreadInbox())
    await stubOps(page, busy())
    await openPanel(page)
    const panel = panelOf(page)

    await expect(panel.getByRole('tab', { name: /^Activity/ })).toHaveAttribute('aria-selected', 'true')
    await expect(panel.getByRole('tab', { name: 'Notifications' })).toHaveAttribute('aria-selected', 'false')

    // Needs attention: the failure with its reason, Retry and Dismiss.
    const failed = panel.getByTestId('inbox-op-failed')
    await expect(failed).toHaveCount(1)
    await expect(failed).toContainText("Couldn't install sherpa-onnx")
    await expect(failed).toContainText('no space left on device')
    await expect(failed.getByRole('button', { name: 'Retry sherpa-onnx' })).toBeVisible()
    await expect(failed.getByRole('button', { name: 'Dismiss sherpa-onnx' })).toBeVisible()

    // In progress: one row per running or queued operation.
    const rows = panel.getByTestId('inbox-op')
    await expect(rows).toHaveCount(3)
    const install = rows.filter({ hasText: 'Installing qwen3-14b' })
    await expect(install).toContainText('Downloading · 1.2 GB / 1.9 GB · 48 MB/s')
    await expect(install.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '48')
    await expect(install).toContainText('48%')
    const upgrade = rows.filter({ hasText: 'Updating llama-cpp' })
    await expect(upgrade).toContainText('Node 3 of 5 · Downloading')
    await expect(rows.filter({ hasText: 'whisper' })).toContainText('Waiting')
    await expect(rows.filter({ hasText: 'whisper' }).getByRole('progressbar')).toHaveCount(0)

    // Recent: what completed, newest first, none of the cancelled or failed.
    const recent = panel.getByTestId('inbox-op-recent')
    await expect(recent).toHaveCount(3)
    await expect(recent.nth(0)).toContainText('Installed gemma-3-4b')
    await expect(recent.nth(1)).toContainText('Updated vllm')
    await expect(recent.nth(2)).toContainText('Removed piper')
    await expect(panel).not.toContainText('cancelled-one')
    await expect(panel).not.toContainText('failed-one')

    // The way to the full page.
    await expect(panel.getByTestId('inbox-see-all')).toHaveAttribute('href', /\/app\/activity$/)
  })

  test('Recent keeps at most five', async ({ page }) => {
    await stubInbox(page, unreadInbox())
    await stubOps(page, makeOps({ history: Array.from({ length: 8 }, (_, i) => record(`m${i}`, { finishedAt: ago(i + 1) })) }))
    await openPanel(page)
    await panelOf(page).getByTestId('inbox-tab-activity').click()
    await expect(panelOf(page).getByTestId('inbox-op-recent')).toHaveCount(5)
  })

  test('rows are compact and the tab scrolls inside the panel', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 })
    await stubInbox(page, unreadInbox())
    const ops = busy()
    ops.operations.push(...Array.from({ length: 10 }, (_, i) => op(`extra-${i}`, { progress: 10 + i })))
    await stubOps(page, ops)
    await openPanel(page)
    const panel = panelOf(page)
    const row = panel.getByTestId('inbox-op').first()
    const box = await row.boundingBox()
    expect(box.height).toBeLessThanOrEqual(72)
    // The panel fits the window and its body scrolls.
    const pbox = await panel.boundingBox()
    expect(pbox.y).toBeGreaterThanOrEqual(0)
    expect(pbox.y + pbox.height).toBeLessThanOrEqual(800)
    const body = panel.getByTestId('inbox-pane-activity')
    expect(await body.evaluate(el => el.scrollHeight > el.clientHeight)).toBe(true)
    expect(await body.evaluate(el => getComputedStyle(el).overflowY)).toBe('auto')
  })

  test('the panel opens on Notifications when nothing runs, and the tab can be picked', async ({ page }) => {
    await stubInbox(page, unreadInbox())
    await stubOps(page, makeOps())
    await openPanel(page)
    const panel = panelOf(page)
    await expect(panel.getByRole('tab', { name: 'Notifications' })).toHaveAttribute('aria-selected', 'true')
    await expect(panel.getByTestId('inbox-item')).toHaveCount(2)
    await panel.getByRole('tab', { name: 'Activity' }).click()
    await expect(panel.getByTestId('inbox-activity-empty')).toBeVisible()
    await expect(panel.getByTestId('inbox-item')).toHaveCount(0)
  })

  test('a tab picked by hand holds while operations come and go', async ({ page }) => {
    await stubInbox(page, unreadInbox())
    const ops = makeOps()
    await stubOps(page, ops)
    await openPanel(page)
    const panel = panelOf(page)
    await expect(panel.getByRole('tab', { name: 'Notifications' })).toHaveAttribute('aria-selected', 'true')
    ops.operations = [op('qwen3-14b')]
    await expect(page.getByTestId('inbox-ring')).toBeVisible({ timeout: 10_000 })
    // Something runs now, but the person is reading notifications.
    await expect(panel.getByRole('tab', { name: 'Notifications' })).toHaveAttribute('aria-selected', 'true')
    await panel.getByRole('tab', { name: /^Activity/ }).click()
    ops.operations = []
    await expect(page.getByTestId('inbox-ring')).toHaveCount(0, { timeout: 10_000 })
    await expect(panel.getByRole('tab', { name: 'Activity' })).toHaveAttribute('aria-selected', 'true')
  })

  test('the panel opens on Activity when an operation failed, with nothing running', async ({ page }) => {
    await stubInbox(page, unreadInbox())
    await stubOps(page, makeOps({ operations: [op('sherpa-onnx', { error: 'no space left on device', cancellable: false })] }))
    await openPanel(page)
    await expect(panelOf(page).getByRole('tab', { name: /^Activity/ })).toHaveAttribute('aria-selected', 'true')
  })

  test('finished operations are in Recent and write nothing to the message list', async ({ page }) => {
    const inbox = unreadInbox()
    await stubInbox(page, inbox)
    await stubOps(page, makeOps({ history: [record('gemma-3-4b')] }))
    await openPanel(page)
    const panel = panelOf(page)
    await panel.getByRole('tab', { name: 'Notifications' }).click()
    await expect(panel.getByTestId('inbox-item')).toHaveCount(2)
    await expect(panel.getByTestId('inbox-item').filter({ hasText: 'gemma-3-4b' })).toHaveCount(0)
    await panel.getByRole('tab', { name: 'Activity' }).click()
    await expect(panel.getByTestId('inbox-op-recent')).toContainText('Installed gemma-3-4b')
  })

  test('the tabs follow the keyboard: arrows move, one is in the tab order', async ({ page }) => {
    await stubInbox(page, unreadInbox())
    await stubOps(page, makeOps())
    await openPanel(page)
    const panel = panelOf(page)
    const notifications = panel.getByRole('tab', { name: 'Notifications' })
    await notifications.focus()
    await expect(panel.getByRole('tab', { name: 'Activity' })).toHaveAttribute('tabindex', '-1')
    await page.keyboard.press('ArrowRight')
    await expect(panel.getByRole('tab', { name: 'Activity' })).toBeFocused()
    await expect(panel.getByRole('tab', { name: 'Activity' })).toHaveAttribute('aria-selected', 'true')
    await expect(panel.getByRole('tabpanel')).toHaveAttribute('aria-labelledby', /tab-activity$/)
    await page.keyboard.press('ArrowLeft')
    await expect(notifications).toBeFocused()
    await expect(panel.getByRole('tabpanel')).toHaveAttribute('aria-labelledby', /tab-notifications$/)
  })

  test('the bar is a progressbar and a poll does not rewrite a live region', async ({ page }) => {
    await stubInbox(page, unreadInbox())
    const ops = makeOps({ operations: [op('qwen3-14b', { progress: 10 })] })
    await stubOps(page, ops)
    await openPanel(page)
    const panel = panelOf(page)
    await expect(panel.getByRole('progressbar', { name: 'Progress for qwen3-14b' })).toHaveAttribute('aria-valuenow', '10')
    // The panel has no live region that carries a percentage.
    expect(await panel.evaluate(el => el.querySelectorAll('[aria-live], [role="status"], [role="alert"]').length)).toBe(0)
    ops.operations = [op('qwen3-14b', { progress: 55 })]
    await expect(panel.getByRole('progressbar', { name: 'Progress for qwen3-14b' })).toHaveAttribute('aria-valuenow', '55', { timeout: 10_000 })
  })

  test('See all activity goes to the Activity page and closes the panel', async ({ page }) => {
    await stubInbox(page, unreadInbox())
    await stubOps(page, busy())
    await openPanel(page)
    await panelOf(page).getByTestId('inbox-see-all').click()
    await expect(page).toHaveURL(/\/app\/activity$/)
    await expect(panelOf(page)).toBeHidden()
    await expect(page.locator('.operation-card').first()).toBeVisible()
  })
})

test.describe('Operations in the inbox: the entry', () => {
  test('a ring shows the mean progress, an error dot shows a failure, and the badge stays the messages', async ({ page }) => {
    await stubInbox(page, unreadInbox())
    const ops = makeOps({ operations: [op('a', { progress: 20 }), op('b', { progress: 60 })] })
    await stubOps(page, ops)
    await page.goto('/app')
    const entry = page.getByTestId('nav-inbox')
    await expect(page.getByTestId('inbox-ring')).toHaveAttribute('data-progress', '40')
    await expect(page.getByTestId('inbox-ring-dot')).toHaveCount(0)
    await expect(entry).toHaveAccessibleName('Notifications, 1 unread, 2 operations in progress')
    // The badge is the one unread message.
    await expect(page.getByTestId('inbox-count')).toHaveText('1')

    // A failure: the ring is the red one with a dot. Still one unread message.
    ops.operations = [op('a', { progress: 20 }), op('c', { error: 'boom', cancellable: false })]
    await expect(page.getByTestId('inbox-ring-dot')).toBeVisible({ timeout: 10_000 })
    await expect(page.getByTestId('inbox-ring')).toHaveAttribute('data-failed', 'true')
    await expect(page.getByTestId('inbox-count')).toHaveText('1')
    await expect(page.getByTestId('inbox-count')).not.toHaveClass(/nav-badge--error/)
    await expect(entry).toHaveAccessibleName(/1 operation failed/)
  })

  test('with nothing running the entry is the plain icon, as before', async ({ page }) => {
    await stubInbox(page, unreadInbox())
    await stubOps(page, makeOps())
    await page.goto('/app')
    await expect(page.getByTestId('nav-inbox')).toBeVisible()
    await expect(page.getByTestId('inbox-ring')).toHaveCount(0)
    await expect(page.getByTestId('nav-inbox')).toHaveAccessibleName('Notifications, 1 unread')
    await expect(page.getByTestId('nav-inbox').locator('.ops-ring')).toHaveCount(0)
  })

  test('operations alone never make a badge', async ({ page }) => {
    await stubInbox(page, makeInbox(0))
    await stubOps(page, busy())
    await page.goto('/app')
    await expect(page.getByTestId('inbox-ring')).toBeVisible()
    await expect(page.getByTestId('inbox-count')).toHaveCount(0)
  })

  test('a queue alone turns the ring instead of filling it', async ({ page }) => {
    await stubInbox(page, makeInbox(0))
    await stubOps(page, makeOps({ operations: [op('whisper', { isQueued: true, progress: 0 })] }))
    await page.goto('/app')
    await expect(page.getByTestId('inbox-ring')).toBeVisible()
    await expect(page.getByTestId('inbox-ring')).not.toHaveAttribute('data-progress', /.+/)
    await expect(page.getByTestId('inbox-ring').locator('.ops-ring--wait')).toHaveCount(1)
  })

  test('the failure is announced once, politely', async ({ page }) => {
    await stubInbox(page, makeInbox(0))
    const ops = makeOps()
    await stubOps(page, ops)
    await page.goto('/app')
    await expect(page.getByTestId('nav-inbox')).toBeVisible()
    const live = page.getByTestId('inbox-live')
    await expect(live).toHaveAttribute('aria-live', 'polite')
    ops.operations = [op('c', { error: 'boom', cancellable: false })]
    await expect(live).toHaveText('An operation failed. See the Activity tab in Notifications.', { timeout: 10_000 })
    // More polls with the same failure do not touch the region.
    await page.evaluate(() => {
      window.__liveWrites = 0
      new MutationObserver((list) => { window.__liveWrites += list.length })
        .observe(document.querySelector('[data-testid="inbox-live"]'), { childList: true, characterData: true, subtree: true })
    })
    await page.waitForTimeout(3500)
    expect(await page.evaluate(() => window.__liveWrites)).toBe(0)
  })

  test('collapsed sidebar: the ring shows on the rail', async ({ page }) => {
    await stubInbox(page, unreadInbox())
    await stubOps(page, busy())
    await page.goto('/app')
    await page.getByRole('button', { name: /Collapse sidebar/i }).click()
    await expect(page.locator('.sidebar.collapsed')).toBeVisible()
    await expect(page.getByTestId('inbox-ring')).toBeVisible()
    await expect(page.getByTestId('inbox-ring-dot')).toBeVisible()
  })
})

test.describe('Operations in the inbox: who sees it', () => {
  async function asMember(page) {
    await page.route('**/api/auth/status', route => route.fulfill({
      json: statusFixture('member', { user: { ...MEMBER, provider: 'local', permissions: { chat: true, chats: true } } }),
    }))
  }

  test('someone who is not an administrator sees today\'s panel: no tabs, and operations are not polled', async ({ page }) => {
    const inbox = unreadInbox()
    await stubInbox(page, inbox)
    const asked = []
    page.on('request', (req) => { if (/\/api\/operations/.test(req.url())) asked.push(req.url()) })
    await asMember(page)
    await page.goto('/app')
    await page.getByTestId('nav-inbox').click()
    const panel = panelOf(page)
    await expect(panel.getByTestId('inbox-item')).toHaveCount(2)
    await expect(panel.getByRole('tab')).toHaveCount(0)
    await expect(panel.getByRole('tablist')).toHaveCount(0)
    await expect(panel.getByTestId('inbox-activity')).toHaveCount(0)
    await expect(page.getByTestId('inbox-ring')).toHaveCount(0)
    // At most the one read while the session is still being resolved; none
    // once the person is known not to be an administrator.
    const settled = asked.length
    expect(settled).toBeLessThanOrEqual(1)
    await page.waitForTimeout(2500)
    expect(asked.length).toBe(settled)
  })

  test('with authentication off everyone is an administrator and sees Activity', async ({ page }) => {
    await stubInbox(page, unreadInbox())
    await stubOps(page, busy())
    await page.route('**/api/auth/status', route => route.fulfill({ json: { authEnabled: false, staticApiKeyRequired: false, providers: [], hasUsers: false, registrationMode: '', user: null } }))
    await openPanel(page)
    await expect(panelOf(page).getByRole('tab', { name: /^Activity/ })).toBeVisible()
    await expect(panelOf(page).getByTestId('inbox-op-failed')).toHaveCount(1)
  })
})

test.describe('Operations in the inbox: Retry, Dismiss and Cancel', () => {
  test('Retry on a failed model install dismisses the job, then installs it again', async ({ page }) => {
    await stubInbox(page, unreadInbox())
    const ops = makeOps({ operations: [op('qwen3-14b', { fullName: 'localai@qwen3-14b', error: 'checksum mismatch', cancellable: false })] })
    await stubOps(page, ops)
    await page.route('**/api/models/install/**', (route) => {
      ops.calls.push(`${route.request().method()} ${new URL(route.request().url()).pathname}`)
      return route.fulfill({ json: {} })
    })
    await openPanel(page)
    await panelOf(page).getByRole('button', { name: 'Retry qwen3-14b' }).click()
    await expect.poll(() => ops.calls).toEqual(['POST /api/operations/job-qwen3-14b/dismiss', 'POST /api/models/install/localai@qwen3-14b'])
  })

  test('Retry on a failed backend upgrade calls the upgrade, not the install', async ({ page }) => {
    await stubInbox(page, unreadInbox())
    const ops = makeOps({ operations: [op('llama-cpp', { isBackend: true, taskType: 'upgrade', fullName: 'llama-cpp', error: 'download interrupted', cancellable: false })] })
    await stubOps(page, ops)
    await page.route('**/api/backends/upgrade/**', (route) => {
      ops.calls.push(`${route.request().method()} ${new URL(route.request().url()).pathname}`)
      return route.fulfill({ json: {} })
    })
    await page.route('**/api/backends/install/**', (route) => {
      ops.calls.push('WRONG install')
      return route.fulfill({ json: {} })
    })
    await openPanel(page)
    await expect(panelOf(page).getByTestId('inbox-op-failed')).toContainText("Couldn't update llama-cpp")
    await panelOf(page).getByRole('button', { name: 'Retry llama-cpp' }).click()
    await expect.poll(() => ops.calls).toEqual(['POST /api/operations/job-llama-cpp/dismiss', 'POST /api/backends/upgrade/llama-cpp'])
  })

  test('Retry on a node-scoped upgrade calls the node upgrade', async ({ page }) => {
    await stubInbox(page, unreadInbox())
    const ops = makeOps({ operations: [op('llama-cpp', { isBackend: true, taskType: 'upgrade', fullName: 'llama-cpp', nodeID: 'node-7', error: 'worker gone', cancellable: false })] })
    await stubOps(page, ops)
    let body = null
    await page.route('**/api/nodes/node-7/backends/upgrade', (route) => {
      body = route.request().postDataJSON()
      ops.calls.push(`${route.request().method()} ${new URL(route.request().url()).pathname}`)
      return route.fulfill({ json: {} })
    })
    await openPanel(page)
    await panelOf(page).getByRole('button', { name: 'Retry llama-cpp' }).click()
    await expect.poll(() => ops.calls).toEqual(['POST /api/operations/job-llama-cpp/dismiss', 'POST /api/nodes/node-7/backends/upgrade'])
    expect(body).toEqual({ backend: 'llama-cpp' })
  })

  test('a failed removal and a failed staging job offer Dismiss only', async ({ page }) => {
    await stubInbox(page, unreadInbox())
    await stubOps(page, makeOps({
      operations: [
        op('piper', { isDeletion: true, taskType: 'deletion', error: 'in use', cancellable: false }),
        op('qwen3-14b', { taskType: 'staging', jobID: 'staging:qwen3-14b', error: 'node unreachable', cancellable: false }),
      ],
    }))
    await openPanel(page)
    const failed = panelOf(page).getByTestId('inbox-op-failed')
    await expect(failed).toHaveCount(2)
    await expect(failed.nth(0)).toContainText("Couldn't remove piper")
    await expect(failed.nth(1)).toContainText("Couldn't stage qwen3-14b")
    await expect(panelOf(page).getByTestId('inbox-op-retry')).toHaveCount(0)
    await expect(panelOf(page).getByTestId('inbox-op-dismiss')).toHaveCount(2)
  })

  test('Dismiss calls the dismiss endpoint for that job', async ({ page }) => {
    await stubInbox(page, unreadInbox())
    const ops = makeOps({ operations: [op('sherpa-onnx', { error: 'no space left on device', cancellable: false })] })
    await stubOps(page, ops)
    await openPanel(page)
    await panelOf(page).getByRole('button', { name: 'Dismiss sherpa-onnx' }).click()
    await expect.poll(() => ops.calls).toEqual(['POST /api/operations/job-sherpa-onnx/dismiss'])
  })

  test('Cancel waits for the undo window like the Activity page, then calls the cancel endpoint', async ({ page }) => {
    await stubInbox(page, unreadInbox())
    const ops = makeOps({ operations: [op('qwen3-14b')] })
    await stubOps(page, ops)
    await openPanel(page)
    const row = panelOf(page).getByTestId('inbox-op')
    await row.getByRole('button', { name: 'Cancel qwen3-14b' }).click()
    await expect(page.getByTestId('activity-undo-toast')).toContainText('Cancelling qwen3-14b')
    await expect(row).toContainText('Cancelling unless you undo')
    expect(ops.calls).toEqual([])
    // Pressing Undo in the toast is outside the panel and must not close it.
    await expect(panelOf(page)).toBeVisible()
    await expect.poll(() => ops.calls, { timeout: 15_000 }).toEqual(['POST /api/operations/job-qwen3-14b/cancel'])
  })

  test('Undo keeps the operation and never calls the server; the panel stays open', async ({ page }) => {
    await stubInbox(page, unreadInbox())
    const ops = makeOps({ operations: [op('qwen3-14b')] })
    await stubOps(page, ops)
    await openPanel(page)
    const row = panelOf(page).getByTestId('inbox-op')
    await row.getByRole('button', { name: 'Cancel qwen3-14b' }).click()
    await page.getByTestId('activity-undo-toast').getByRole('button', { name: 'Undo' }).click()
    await expect(page.getByTestId('activity-undo-toast')).toHaveCount(0)
    await expect(panelOf(page)).toBeVisible()
    await expect(row.getByRole('button', { name: 'Cancel qwen3-14b' })).toBeVisible()
    await page.waitForTimeout(9_000)
    expect(ops.calls).toEqual([])
  })

  test('a Cancel waiting for its undo is not made final by a click on the other tab', async ({ page }) => {
    await stubInbox(page, unreadInbox())
    const ops = makeOps({ operations: [op('qwen3-14b')] })
    await stubOps(page, ops)
    await openPanel(page)
    await panelOf(page).getByRole('button', { name: 'Cancel qwen3-14b' }).click()
    await panelOf(page).getByRole('tab', { name: 'Notifications' }).click()
    await page.waitForTimeout(500)
    expect(ops.calls).toEqual([])
  })
})

test.describe('Operations in the inbox: a phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('the sheet has the same tabs, rows do not wrap and nothing scrolls sideways', async ({ page }) => {
    await stubInbox(page, unreadInbox())
    const ops = busy()
    ops.operations[0].message = 'x'
    ops.operations[0].nodes = ['success', 'success', 'downloading', 'queued', 'queued'].map((status, i) => ({ node_id: `n${i}`, status }))
    await stubOps(page, ops)
    await page.goto('/app')
    await expect(page.getByTestId('mobile-inbox-ring')).toBeVisible()
    await inboxButton(page).click()
    const panel = panelOf(page)
    await expect(panel.getByRole('tab', { name: /^Activity/ })).toHaveAttribute('aria-selected', 'true')
    const sheet = await panel.boundingBox()
    expect(sheet.x).toBe(0)
    expect(sheet.width).toBe(390)
    // Every detail line is one line, cut with an ellipsis when it is too long.
    const detail = panel.getByTestId('inbox-op').first().locator('.nx-op__detail')
    const lines = await detail.evaluate(el => Math.round(el.getBoundingClientRect().height / parseFloat(getComputedStyle(el).lineHeight)))
    expect(lines).toBe(1)
    expect(await panel.evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true)
    // Cancel and Retry are real touch targets.
    const cancel = await panel.getByRole('button', { name: 'Cancel qwen3-14b' }).boundingBox()
    expect(cancel.height).toBeGreaterThanOrEqual(40)
    const retry = await panel.getByRole('button', { name: 'Retry sherpa-onnx' }).boundingBox()
    expect(retry.height).toBeGreaterThanOrEqual(40)
    await panel.getByRole('tab', { name: 'Notifications' }).click()
    await expect(panel.getByTestId('inbox-item')).toHaveCount(2)
  })
})

test.describe('Operations in the inbox: chats off', () => {
  // With the chats off there is no inbox, but an administrator still has the
  // operations: the entry, the ring and the Activity pane, and no request to the
  // chats for notifications or changes.
  const chatsOff = (page) => page.route('**/api/features', route => route.fulfill({ json: { chats: false } }))
  const chatCalls = (page) => {
    const seen = []
    page.on('request', (req) => { if (/\/api\/chats\/(notifications\?.*limit=50|changes)/.test(req.url())) seen.push(req.url()) })
    return seen
  }

  test('an administrator with a running operation has the entry, the ring and an Activity panel', async ({ page }) => {
    await stubInbox(page, unreadInbox())
    await chatsOff(page)
    await stubOps(page, busy())
    const calls = chatCalls(page)
    await page.goto('/app')
    const entry = page.getByTestId('nav-inbox')
    await expect(entry).toBeVisible()
    await expect(entry).toContainText('Activity')
    await expect(page.getByTestId('inbox-ring')).toBeVisible()
    await expect(page.getByTestId('inbox-ring-dot')).toBeVisible()
    // No messages, so no badge.
    await expect(page.getByTestId('inbox-count')).toHaveCount(0)
    await entry.click()
    const panel = page.getByRole('dialog', { name: 'Activity' })
    await expect(panel).toBeVisible()
    await expect(panel.getByTestId('inbox-op')).toHaveCount(3)
    await expect(panel.getByTestId('inbox-op-failed')).toHaveCount(1)
    // The Notifications tab, its filters, its menu and its push switch are not there.
    await expect(panel.getByRole('tablist')).toHaveCount(0)
    await expect(panel.getByTestId('inbox-menu')).toHaveCount(0)
    await expect(panel.getByTestId('inbox-filter-all')).toHaveCount(0)
    await expect(panel.getByTestId('inbox-item')).toHaveCount(0)
    await expect(panel.getByTestId('inbox-see-all')).toBeVisible()
    await page.waitForTimeout(1500)
    expect(calls).toEqual([])
  })

  test('the panel stays after the work ends, for the rest of the visit', async ({ page }) => {
    await stubInbox(page, unreadInbox())
    await chatsOff(page)
    const ops = makeOps({ operations: [op('qwen3-14b')] })
    await stubOps(page, ops)
    await page.goto('/app')
    await expect(page.getByTestId('inbox-ring')).toBeVisible()
    await page.getByTestId('nav-inbox').click()
    ops.operations = []
    ops.history = [record('qwen3-14b', { finishedAt: ago(0.1) })]
    await expect(page.getByRole('dialog', { name: 'Activity' }).getByTestId('inbox-op-recent')).toContainText('Installed qwen3-14b', { timeout: 15_000 })
    await expect(page.getByTestId('nav-inbox')).toBeVisible()
  })

  test('an administrator whose operation finished a moment ago still has the entry', async ({ page }) => {
    await stubInbox(page, unreadInbox())
    await chatsOff(page)
    await stubOps(page, makeOps({ history: [record('gemma-3-4b')] }))
    await page.goto('/app')
    await expect(page.getByTestId('nav-inbox')).toBeVisible()
    await expect(page.getByTestId('inbox-ring')).toHaveCount(0)
  })

  test('an administrator with nothing running and nothing recent has no entry', async ({ page }) => {
    await stubInbox(page, unreadInbox())
    await chatsOff(page)
    await stubOps(page, makeOps())
    const calls = chatCalls(page)
    await page.goto('/app')
    await expect(page.locator('.sidebar-nav').first()).toBeVisible()
    await page.waitForTimeout(1500)
    await expect(page.getByTestId('nav-inbox')).toHaveCount(0)
    await expect(page.getByTestId('mobile-inbox')).toHaveCount(0)
    expect(calls).toEqual([])
  })

  test('someone who is not an administrator has no entry, whatever runs', async ({ page }) => {
    await stubInbox(page, unreadInbox())
    await chatsOff(page)
    await stubOps(page, busy())
    await page.route('**/api/auth/status', route => route.fulfill({
      json: statusFixture('member', { user: { ...MEMBER, provider: 'local', permissions: { chat: true } } }),
    }))
    await page.goto('/app')
    await expect(page.locator('.sidebar-nav').first()).toBeVisible()
    await page.waitForTimeout(1500)
    await expect(page.getByTestId('nav-inbox')).toHaveCount(0)
    await expect(page.getByTestId('inbox-ring')).toHaveCount(0)
  })

  test('a phone has the bell with the ring and the Activity sheet', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await stubInbox(page, unreadInbox())
    await chatsOff(page)
    await stubOps(page, busy())
    await page.goto('/app')
    await expect(page.getByTestId('mobile-inbox-ring')).toBeVisible()
    await inboxButton(page).click()
    await expect(page.getByRole('dialog', { name: 'Activity' }).getByTestId('inbox-op')).toHaveCount(3)
  })
})
