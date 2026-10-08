import { test, expect } from './coverage-fixtures.js'
import { mockSettings } from './settings-fixtures.js'

const row = (page, key) => page.locator(`[data-field="${key}"]`)
const group = (page, name) => page.getByRole('button', { name: new RegExp(`^${name}`) })
const sw = (page, key) => row(page, key).getByRole('switch')

test.describe('Settings: groups and search', () => {
  let state
  test.beforeEach(async ({ page }) => {
    state = await mockSettings(page)
    await page.goto('/app/settings')
    await expect(page.getByTestId('settings-page')).toBeVisible()
  })

  test('shows eight groups by intent and opens on memory and models', async ({ page }) => {
    const names = await page.locator('.st-group .st-group__label').allTextContents()
    expect(names).toEqual([
      'Memory and models', 'Speed and defaults', 'Backends and galleries', 'Access and security',
      'Debugging and traces', 'Agents and responses', 'Swarm and sharing', 'Look and feel',
    ])
    await expect(page.locator('.st-group[aria-current="true"]')).toHaveText(/Memory and models/)
    await expect(page.getByRole('heading', { level: 2, name: /Memory and models/ })).toBeVisible()
    await expect(row(page, 'watchdog_idle_timeout')).toBeVisible()
    await expect(row(page, 'debug')).toHaveCount(0)
  })

  test('every real field sits in exactly one group', async ({ page }) => {
    const seen = new Set()
    for (const name of await page.locator('.st-group .st-group__label').allTextContents()) {
      await group(page, name).click()
      for (const key of await page.locator('[data-field]').evaluateAll(els => els.map(e => e.getAttribute('data-field')))) {
        expect(seen.has(key), `${key} is listed twice`).toBe(false)
        seen.add(key)
      }
    }
    // 53 settings plus the three branding images.
    expect(seen.size).toBe(56)
    await expect(page.getByLabel(/Search all 56 settings/)).toBeVisible()
  })

  test('a group lists its settings, and the memory group shows memory use now', async ({ page }) => {
    await expect(page.getByTestId('settings-memory-now')).toContainText('GPU memory now: 17.1 GB of 22.4 GB')
    await expect(page.getByTestId('settings-memory-now')).toContainText('Eviction starts at 90%')
    await group(page, 'Debugging and traces').click()
    await expect(page.getByText('Record API traces')).toBeVisible()
    await expect(page.getByText('Enable Backend Logging')).toBeVisible()
    await expect(page.getByTestId('settings-memory-now')).toHaveCount(0)
  })

  test('search finds a setting by its name, its key and the section it used to be in', async ({ page }) => {
    const search = page.getByRole('searchbox')
    await search.fill('cors')
    await expect(page.getByTestId('settings-results-title')).toHaveText(/3 settings for “cors”/)
    await expect(row(page, 'cors_allow_origins')).toBeVisible()
    // Results come from every group and say where each one lives now.
    await expect(row(page, 'cors')).toContainText('Access and security')
    await expect(row(page, 'cors')).toContainText('was API & CORS')

    await search.fill('Watchdog')
    await expect(row(page, 'watchdog_busy_timeout')).toContainText('was Watchdog')
    await expect(row(page, 'size_aware_eviction')).toBeVisible()

    await search.fill('Memory Reclaimer')
    await expect(row(page, 'memory_reclaimer_threshold')).toBeVisible()

    await search.fill('lru_eviction_max')
    await expect(page.locator('[data-field]')).toHaveCount(1)

    await search.fill('zzzz')
    await expect(page.getByTestId('settings-results-title')).toHaveText('No settings found')
    await page.getByRole('button', { name: 'Clear search' }).click()
    await expect(search).toHaveValue('')
    await expect(row(page, 'watchdog_idle_timeout')).toBeVisible()
  })

  test('Escape clears the search and a group click leaves search mode', async ({ page }) => {
    const search = page.getByRole('searchbox')
    await search.fill('debug')
    await expect(page.getByTestId('settings-results')).toBeVisible()
    await search.press('Escape')
    await expect(search).toHaveValue('')
    await search.fill('debug')
    await group(page, 'Swarm and sharing').click()
    await expect(search).toHaveValue('')
    await expect(row(page, 'p2p_token')).toBeVisible()
  })
})

test.describe('Settings: defaults and hints', () => {
  test.beforeEach(async ({ page }) => {
    await mockSettings(page)
    await page.goto('/app/settings')
    await expect(page.getByTestId('settings-page')).toBeVisible()
  })

  test('marks a value as changed only when the built-in default is known', async ({ page }) => {
    // lru_eviction_max_retries is 50 against a default of 30.
    const retries = row(page, 'lru_eviction_max_retries')
    await expect(retries.locator('.st-tag', { hasText: 'Changed' })).toBeVisible()
    await expect(retries).toContainText('default 30')
    // The idle timeout equals its default in another spelling (15m0s and 15m).
    await expect(row(page, 'watchdog_idle_timeout').locator('.st-tag', { hasText: 'Changed' })).toHaveCount(0)
    // The memory threshold is 90% against 95%.
    await expect(row(page, 'memory_reclaimer_threshold')).toContainText('default 95%')

    // Threads has no default the code states, so no marker is drawn however
    // far the value is from anything.
    await group(page, 'Speed and defaults').click()
    await expect(row(page, 'threads').locator('.st-tag', { hasText: 'Changed' })).toHaveCount(0)
    await expect(row(page, 'threads')).not.toContainText('default')
    await expect(row(page, 'context_size')).not.toContainText('default')
    await expect(row(page, 'artifact_download_concurrency')).toContainText('default 1')
  })

  test('counts the settings that differ from their default', async ({ page }) => {
    const count = Number(await page.getByTestId('settings-changed-count').locator('.dk-mono').textContent())
    expect(count).toBeGreaterThan(0)
    await row(page, 'lru_eviction_max_retries').getByRole('button', { name: 'Reset' }).click()
    await expect(row(page, 'lru_eviction_max_retries').getByRole('spinbutton')).toHaveValue('30')
    await expect(page.getByTestId('settings-changed-count').locator('.dk-mono')).toHaveText(String(count - 1))
    await expect(page.getByTestId('settings-pending')).toContainText('1 pending')
  })

  test('says when a setting applies only where the server says so', async ({ page }) => {
    await expect(row(page, 'watchdog_idle_timeout')).toContainText('Applies now')
    await expect(row(page, 'max_active_backends')).toContainText('Applies now')
    await group(page, 'Agents and responses').click()
    await expect(row(page, 'agent_pool_enable_logs')).toContainText('Needs restart')
    await expect(row(page, 'agent_job_retention_days')).toContainText('Applies now')
    await group(page, 'Speed and defaults').click()
    await expect(row(page, 'threads')).not.toContainText('Applies now')
    await expect(row(page, 'threads')).not.toContainText('Needs restart')
  })

  test('CSRF protection reads the right way round', async ({ page }) => {
    // The wire field carries "disable CSRF", and the fixture has it false.
    await group(page, 'Access and security').click()
    await expect(sw(page, 'csrf')).toHaveAttribute('aria-checked', 'true')
  })

  test('a setting that needs another one is disabled while that one is off', async ({ page }) => {
    await expect(row(page, 'watchdog_busy_timeout').getByRole('textbox')).toBeDisabled()
    await sw(page, 'watchdog_busy_enabled').click()
    await expect(row(page, 'watchdog_busy_timeout').getByRole('textbox')).toBeEnabled()
  })
})

test.describe('Settings: pending, diff, apply, discard and undo', () => {
  let state
  test.beforeEach(async ({ page }) => {
    state = await mockSettings(page)
    await page.goto('/app/settings')
    await expect(page.getByTestId('settings-page')).toBeVisible()
  })

  test('shows no bar until something changes, then counts what is pending', async ({ page }) => {
    await expect(page.getByTestId('settings-pending')).toHaveCount(0)
    await row(page, 'watchdog_idle_timeout').getByRole('textbox').fill('30m')
    await expect(page.getByTestId('settings-pending')).toContainText('1 pending')
    await expect(row(page, 'watchdog_idle_timeout').locator('.st-tag', { hasText: 'Draft' })).toBeVisible()
    await expect(page.locator('.st-group[data-group="memory"] [title="Has a draft"]')).toBeVisible()
    await row(page, 'max_active_backends').getByRole('spinbutton').fill('3')
    await expect(page.getByTestId('settings-pending')).toContainText('2 pending')
  })

  test('the diff lists old and new values with the checks the browser can make', async ({ page }) => {
    await row(page, 'watchdog_idle_timeout').getByRole('textbox').fill('30m')
    await sw(page, 'force_eviction_when_busy').click()
    await page.getByRole('button', { name: 'Show diff' }).click()
    const diff = page.getByTestId('settings-diff')
    await expect(diff.locator('li.st-diff__row')).toHaveCount(2)
    await expect(diff).toContainText('watchdog_idle_timeout')
    await expect(diff.locator('del').first()).toHaveText('15m')
    await expect(diff).toContainText('30m')
    await expect(diff).toContainText('off')
    await expect(diff.getByLabel('Checks')).toContainText('30m is a valid duration for idle timeout')
    await expect(diff.getByLabel('Checks')).toContainText('Evicting while busy can interrupt requests in flight')
    await page.getByRole('button', { name: 'Hide diff' }).click()
    await expect(diff).toHaveCount(0)
  })

  test('an invalid duration is an error and stops Apply', async ({ page }) => {
    await row(page, 'watchdog_idle_timeout').getByRole('textbox').fill('soon')
    await page.getByRole('button', { name: 'Show diff' }).click()
    await expect(page.getByLabel('Checks')).toContainText('"soon" is not a duration')
    await expect(page.getByRole('button', { name: 'Apply' })).toBeDisabled()
    await expect(page.getByTestId('settings-pending')).toContainText('fix the errors to apply')
    await row(page, 'watchdog_idle_timeout').getByRole('textbox').fill('1h30m')
    await expect(page.getByRole('button', { name: 'Apply' })).toBeEnabled()
  })

  test('a restart-only change is counted and warned about', async ({ page }) => {
    await group(page, 'Agents and responses').click()
    await sw(page, 'agent_pool_enable_logs').click()
    await expect(page.getByTestId('settings-pending')).toContainText('1 pending')
    await expect(page.getByTestId('settings-pending')).toContainText('1 needs a restart')
    await page.getByRole('button', { name: 'Show diff' }).click()
    await expect(page.getByLabel('Checks')).toContainText('Restart LocalAI for Agent logs')
  })

  test('apply sends only the changed keys, then offers undo', async ({ page }) => {
    await row(page, 'watchdog_idle_timeout').getByRole('textbox').fill('30m')
    await page.getByRole('button', { name: 'Apply' }).click()
    await expect(page.getByText('Settings saved successfully')).toBeVisible()
    expect(state.posts).toEqual([{ watchdog_idle_timeout: '30m' }])
    await expect(page.getByTestId('settings-pending')).toHaveCount(0)
    const toast = page.getByTestId('settings-undo-toast')
    await expect(toast).toContainText('Applied 1 change. Undo saves the old values again.')

    await toast.getByRole('button', { name: 'Undo' }).click()
    await expect(page.getByText('Previous values saved again')).toBeVisible()
    // Undo is a second save with the old value, not a rollback.
    expect(state.posts).toEqual([{ watchdog_idle_timeout: '30m' }, { watchdog_idle_timeout: '15m' }])
    await expect(row(page, 'watchdog_idle_timeout').getByRole('textbox')).toHaveValue('15m')
    await expect(page.getByTestId('settings-pending')).toHaveCount(0)
  })

  test('the master watchdog switch sends the three flags it controls', async ({ page }) => {
    await sw(page, 'watchdog_enabled').click()
    await expect(page.getByTestId('settings-pending')).toContainText('1 pending')
    await page.getByRole('button', { name: 'Apply' }).click()
    await expect(page.getByTestId('settings-undo-toast')).toBeVisible()
    expect(state.posts[0]).toEqual({ watchdog_enabled: false, watchdog_idle_enabled: false, watchdog_busy_enabled: false })
  })

  test('discard drops every edit', async ({ page }) => {
    await row(page, 'watchdog_idle_timeout').getByRole('textbox').fill('30m')
    await sw(page, 'size_aware_eviction').click()
    await page.getByRole('button', { name: 'Discard' }).click()
    await expect(page.getByTestId('settings-pending')).toHaveCount(0)
    await expect(row(page, 'watchdog_idle_timeout').getByRole('textbox')).toHaveValue('15m')
    await expect(sw(page, 'size_aware_eviction')).toHaveAttribute('aria-checked', 'false')
    expect(state.posts).toEqual([])
  })

  test('a failed save keeps the edits and says why', async ({ page }) => {
    state.failNext = 'Invalid watchdog_idle_timeout format'
    await row(page, 'watchdog_idle_timeout').getByRole('textbox').fill('30m')
    await page.getByRole('button', { name: 'Apply' }).click()
    await expect(page.getByText(/Save failed/)).toBeVisible()
    await expect(page.getByTestId('settings-pending')).toContainText('1 pending')
    await expect(page.getByTestId('settings-undo-toast')).toHaveCount(0)
  })

  test('leaving with unapplied edits asks first', async ({ page }) => {
    await row(page, 'watchdog_idle_timeout').getByRole('textbox').fill('30m')
    await page.locator('.dk-hubtabs a[href="/app/traffic"]').click()
    await expect(page.getByRole('alertdialog')).toBeVisible()
  })
})

test.describe('Settings: fields with a different wire form', () => {
  let state
  test.beforeEach(async ({ page }) => {
    state = await mockSettings(page)
    await page.goto('/app/settings')
    await expect(page.getByTestId('settings-page')).toBeVisible()
  })

  test('the CSRF switch writes the inverse of what it shows', async ({ page }) => {
    await group(page, 'Access and security').click()
    await sw(page, 'csrf').click()
    await page.getByRole('button', { name: 'Show diff' }).click()
    await expect(page.getByLabel('Checks')).toContainText('forged cross-site browser requests are not blocked')
    await page.getByRole('button', { name: 'Apply' }).click()
    await expect(page.getByTestId('settings-undo-toast')).toBeVisible()
    expect(state.posts[0]).toEqual({ csrf: true })
  })

  test('shared API keys are sent as a list, and never shown in the diff or the history', async ({ page }) => {
    await group(page, 'Access and security').click()
    await row(page, 'api_keys').getByRole('textbox').fill('sk-shared-1\nsk-shared-2, sk-shared-3')
    await page.getByRole('button', { name: 'Show diff' }).click()
    await expect(page.getByTestId('settings-diff')).toContainText('Shared API keys')
    await expect(page.getByTestId('settings-diff')).not.toContainText('sk-shared-2')
    await page.getByRole('button', { name: 'Apply' }).click()
    await expect(page.getByTestId('settings-undo-toast')).toBeVisible()
    expect(state.posts[0]).toEqual({ api_keys: ['sk-shared-1', 'sk-shared-2', 'sk-shared-3'] })
    await page.getByRole('button', { name: 'History' }).click()
    const hist = page.getByTestId('settings-history')
    await expect(hist).toContainText('Shared API keys')
    await expect(hist).not.toContainText('sk-shared-2')
    await expect(hist.getByRole('button', { name: 'Revert' })).toHaveCount(0)
  })

  test('a gallery list is parsed, and bad JSON stops Apply', async ({ page }) => {
    await group(page, 'Backends and galleries').click()
    const box = row(page, 'galleries').getByRole('textbox')
    await box.fill('{oops')
    await page.getByRole('button', { name: 'Show diff' }).click()
    await expect(page.getByLabel('Checks')).toContainText('this must be a JSON list')
    await expect(page.getByRole('button', { name: 'Apply' })).toBeDisabled()
    await box.fill('[{"url":"https://example.org/models","name":"mine"}]')
    await page.getByRole('button', { name: 'Apply' }).click()
    await expect(page.getByTestId('settings-undo-toast')).toBeVisible()
    expect(state.posts[0]).toEqual({ galleries: [{ url: 'https://example.org/models', name: 'mine' }] })
  })

  test('the GPU memory budget is checked the way the server checks it', async ({ page }) => {
    const budget = row(page, 'vram_budget').getByRole('textbox')
    await budget.fill('150%')
    await page.getByRole('button', { name: 'Show diff' }).click()
    await expect(page.getByLabel('Checks')).toContainText('between 0 and 100')
    await expect(page.getByRole('button', { name: 'Apply' })).toBeDisabled()
    await budget.fill('12GB')
    await expect(page.getByLabel('Checks')).toContainText('12GB is a valid GPU memory budget')
    await expect(page.getByRole('button', { name: 'Apply' })).toBeEnabled()
  })

  test('a new P2P token is generated by the server, so the page sends 0', async ({ page }) => {
    await group(page, 'Swarm and sharing').click()
    await row(page, 'p2p_token').getByRole('button', { name: /Generate/ }).click()
    await page.getByRole('button', { name: 'Apply' }).click()
    await expect(page.getByTestId('settings-undo-toast')).toBeVisible()
    expect(state.posts[0]).toEqual({ p2p_token: '0' })
  })
})

test.describe('Settings: history', () => {
  test.beforeEach(async ({ page }) => {
    await mockSettings(page)
    await page.goto('/app/settings')
    await expect(page.getByTestId('settings-page')).toBeVisible()
  })

  test('starts empty and says what it is', async ({ page }) => {
    await page.getByRole('button', { name: 'History' }).click()
    const sheet = page.getByTestId('settings-history')
    await expect(sheet).toContainText('LocalAI keeps no settings log')
    await expect(sheet).toContainText('Nothing applied yet')
    await page.keyboard.press('Escape')
    await expect(sheet).toHaveCount(0)
  })

  test('lists applied changes, newest first, and revert stages the old value as an edit', async ({ page }) => {
    await row(page, 'watchdog_idle_timeout').getByRole('textbox').fill('30m')
    await page.getByRole('button', { name: 'Apply' }).click()
    await expect(page.getByTestId('settings-undo-toast')).toBeVisible()
    await sw(page, 'size_aware_eviction').click()
    await page.getByRole('button', { name: 'Apply' }).click()
    await expect(page.getByTestId('settings-pending')).toHaveCount(0)

    await page.getByRole('button', { name: 'History' }).click()
    const rows = page.getByTestId('history-row')
    await expect(rows).toHaveCount(2)
    await expect(rows.first()).toContainText('Evict the largest model first')
    await expect(rows.first()).toContainText('off → on')
    await expect(rows.nth(1)).toContainText('15m → 30m')

    await rows.nth(1).getByRole('button', { name: 'Revert' }).click()
    await expect(page.getByTestId('settings-history')).toHaveCount(0)
    await expect(row(page, 'watchdog_idle_timeout').getByRole('textbox')).toHaveValue('15m')
    await expect(page.getByTestId('settings-pending')).toContainText('1 pending')
  })

  test('survives a reload and can be cleared', async ({ page }) => {
    await row(page, 'watchdog_idle_timeout').getByRole('textbox').fill('30m')
    await page.getByRole('button', { name: 'Apply' }).click()
    await expect(page.getByTestId('settings-undo-toast')).toBeVisible()
    await page.reload()
    await expect(page.getByTestId('settings-page')).toBeVisible()
    await page.getByRole('button', { name: 'History' }).click()
    await expect(page.getByTestId('history-row')).toHaveCount(1)
    await page.getByRole('button', { name: 'Clear this list' }).click()
    await expect(page.getByTestId('settings-history')).toContainText('Nothing applied yet')
  })
})

test.describe('Settings: phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('stacks the groups above the settings, keeps labels wide and does not overflow', async ({ page }) => {
    await mockSettings(page)
    await page.goto('/app/settings')
    await expect(page.getByTestId('settings-page')).toBeVisible()
    const groups = await page.locator('.st-groups').boundingBox()
    const content = await page.locator('.st-content').boundingBox()
    expect(content.y).toBeGreaterThanOrEqual(groups.y + groups.height - 1)
    const label = await page.locator('.st-row__main').first().boundingBox()
    expect(label.width).toBeGreaterThan(150)
    expect(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)).toBe(false)

    await row(page, 'watchdog_idle_timeout').getByRole('textbox').fill('30m')
    const bar = page.getByTestId('settings-pending')
    await expect(bar).toBeInViewport()
    await page.getByRole('button', { name: 'Show diff' }).click()
    await expect(page.getByTestId('settings-diff')).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)).toBe(false)
  })
})
