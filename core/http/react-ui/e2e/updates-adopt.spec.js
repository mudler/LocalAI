import { test, expect } from './coverage-fixtures.js'
import { mockLedger } from './ledger-fixtures.js'
import { mockOperate } from './operate-fixtures.js'
import { minutesAgo, mockUpdates, modelItem, modelSnapshotOf, snapshotOf } from './updates-fixtures.js'

// Adopt: a model installed before LocalAI recorded where it came from shows as
// unknown (legacy-sidecar). The Updates page and the Models inspector offer
// Adopt; one gallery adopts at once, several open a choice, and every adoption
// is followed by a model check. The API is stubbed.

const rowOf = (page, name) => page.locator(`[data-testid="update-row"][data-name="${name}"]`)
const toast = (page) => page.getByTestId('model-adopt-toast')
const dialog = (page) => page.getByTestId('model-adopt-dialog')
const legacy = (name, extra = {}) => modelItem(name, 'unknown', { reason: 'legacy-sidecar', gallery: '', entryName: '', availableDigest: '', installedDigest: '', ...extra })

// After the adoption the next check knows the model.
const adopted = (name) => () => modelSnapshotOf([modelItem(name, 'up-to-date')])

async function setup(page, items, over = {}) {
  await mockOperate(page)
  return mockUpdates(page, { snapshot: snapshotOf([]), models: modelSnapshotOf(items), ...over })
}

test.describe('Updates page: adopt', () => {
  test('one gallery adopts at once, then the model is checked and the row moves', async ({ page }) => {
    const state = await setup(page, [legacy('qwen3-4b')], { onModelCheck: adopted('qwen3-4b') })
    await page.goto('/app/updates')
    const row = rowOf(page, 'qwen3-4b')
    await expect(row).toHaveAttribute('data-state', 'unknown')
    await expect(page.locator('[data-group="other"]')).toContainText('qwen3-4b')
    await expect(row).toContainText('LocalAI did not record where this model came from. Adopt it to get updates. Your config is not changed.')
    await row.getByRole('button', { name: 'Adopt qwen3-4b' }).click()
    await expect(toast(page)).toContainText('qwen3-4b adopted from gallery localai')
    expect(state.modelCalls.filter(c => c.action === 'adopt')).toEqual([{ action: 'adopt', name: 'qwen3-4b', body: {} }])
    expect(state.modelChecks).toBe(1)
    await expect(rowOf(page, 'qwen3-4b')).toHaveCount(0)
    await expect(page.locator('[data-group="current"]')).toContainText('qwen3-4b')
  })

  test('the menu has the same action, and the button shows it is working', async ({ page }) => {
    const state = await setup(page, [legacy('qwen3-4b')], { onModelCheck: adopted('qwen3-4b') })
    await page.route(/\/api\/models\/[^/]+\/adopt$/, async (route) => {
      await new Promise(resolve => setTimeout(resolve, 700))
      return route.fulfill({ json: { name: 'qwen3-4b', gallery: 'localai', entry: 'qwen3-4b', filesMatch: true } })
    })
    await page.goto('/app/updates')
    const row = rowOf(page, 'qwen3-4b')
    await row.getByRole('button', { name: 'More actions for qwen3-4b' }).click()
    await expect(page.getByRole('menuitem', { name: 'Adopt' })).toBeVisible()
    await page.keyboard.press('Escape')
    const button = row.getByTestId('model-adopt')
    await button.click()
    await expect(button).toBeDisabled()
    await expect(toast(page)).toContainText('adopted')
    expect(state.modelChecks).toBe(1)
  })

  test('more than one gallery opens a choice; the pick is sent and the model checked', async ({ page }) => {
    const state = await setup(page, [legacy('qwen3-4b')], {
      onModelCheck: adopted('qwen3-4b'),
      modelReply: (action, _name, body) => (action === 'adopt' && !body.gallery
        ? { status: 409, json: { error: 'more than one gallery lists an entry for this model; name the gallery: localai@qwen3-4b, community@qwen3-4b-instruct', candidates: ['localai@qwen3-4b', 'community@qwen3-4b-instruct'] } }
        : null),
    })
    await page.goto('/app/updates')
    await rowOf(page, 'qwen3-4b').getByTestId('model-adopt').click()
    await expect(dialog(page)).toBeVisible()
    await expect(dialog(page).getByTestId('model-adopt-option')).toHaveCount(2)
    await expect(dialog(page)).toContainText('community')
    const confirm = dialog(page).getByTestId('model-adopt-confirm')
    await expect(confirm).toBeDisabled()
    expect(state.modelChecks).toBe(0)
    await dialog(page).getByRole('radio').nth(1).check()
    await confirm.click()
    await expect(dialog(page)).toHaveCount(0)
    const calls = state.modelCalls.filter(c => c.action === 'adopt')
    expect(calls.map(c => c.body)).toEqual([{}, { gallery: 'community', entry: 'qwen3-4b-instruct' }])
    expect(state.modelChecks).toBe(1)
    await expect(toast(page)).toContainText('adopted from gallery community')
  })

  test('a refusal in the choice stays in the dialog', async ({ page }) => {
    await setup(page, [legacy('qwen3-4b')], {
      modelReply: (action, _name, body) => {
        if (action !== 'adopt') return null
        if (!body.gallery) return { status: 409, json: { error: 'name the gallery', candidates: ['localai@qwen3-4b', 'community@qwen3-4b'] } }
        return { status: 422, json: { error: 'the gallery and entry named do not list this model' } }
      },
    })
    await page.goto('/app/updates')
    await rowOf(page, 'qwen3-4b').getByTestId('model-adopt').click()
    await dialog(page).getByRole('radio').first().check()
    await dialog(page).getByTestId('model-adopt-confirm').click()
    await expect(page.getByTestId('model-adopt-error')).toContainText('Could not adopt: the gallery and entry named do not list this model')
    await expect(dialog(page)).toBeVisible()
  })

  test('no gallery lists the model: the error is shown and nothing is checked', async ({ page }) => {
    const state = await setup(page, [legacy('qwen3-4b')], {
      modelReply: (action) => (action === 'adopt'
        ? { status: 422, json: { error: 'no configured gallery lists an entry for this model; name the gallery and the entry' } }
        : null),
    })
    await page.goto('/app/updates')
    await rowOf(page, 'qwen3-4b').getByTestId('model-adopt').click()
    await expect(toast(page)).toContainText('qwen3-4b could not be adopted: no configured gallery lists an entry for this model')
    await expect(dialog(page)).toHaveCount(0)
    expect(state.modelChecks).toBe(0)
    await expect(rowOf(page, 'qwen3-4b').getByTestId('model-adopt')).toBeEnabled()
  })

  test('only a legacy unknown model has Adopt', async ({ page }) => {
    await setup(page, [
      legacy('a-legacy'),
      modelItem('b-unreadable', 'unknown', { reason: 'unreadable', availableDigest: '' }),
      modelItem('c-notchecked', 'unknown', { reason: 'not-checked', availableDigest: '' }),
      modelItem('d-update', 'update-available'),
      modelItem('e-held', 'held', { held: true }),
      modelItem('f-gone', 'gone'),
      modelItem('g-current', 'up-to-date'),
    ])
    await page.goto('/app/updates')
    await expect(rowOf(page, 'a-legacy').getByTestId('model-adopt')).toBeVisible()
    await page.getByTestId('updates-current-toggle').click()
    for (const name of ['b-unreadable', 'c-notchecked', 'd-update', 'e-held', 'f-gone', 'g-current']) {
      await expect(rowOf(page, name)).toBeVisible()
      await expect(rowOf(page, name).getByTestId('model-adopt')).toHaveCount(0)
    }
    await expect(page.getByTestId('model-adopt')).toHaveCount(1)
  })
})

test.describe('Models page: adopt', () => {
  async function open(page, items, over = {}) {
    await mockLedger(page)
    const calls = { adopt: [], checks: 0 }
    let snapshot = modelSnapshotOf(items)
    await page.route('**/api/models/updates', route => route.fulfill({ json: snapshot }))
    await page.route('**/api/models/updates/check', (route) => {
      calls.checks += 1
      snapshot = { checkedAt: minutesAgo(0), sources: snapshot.sources, items: items.map(it => modelItem(it.name, 'up-to-date')) }
      return route.fulfill({ json: snapshot })
    })
    await page.route(/\/api\/models\/[^/]+\/adopt$/, (route) => {
      calls.adopt.push(route.request().postDataJSON())
      if (over.refuse) return route.fulfill({ status: over.refuse.status, json: over.refuse.json })
      return route.fulfill({ json: { name: 'kokoro-82m', gallery: 'localai', entry: 'kokoro-82m', filesMatch: true } })
    })
    await page.goto('/app/models?view=installed')
    await expect(page.locator('[data-testid="installed-models-rail-item"]').first()).toBeVisible({ timeout: 15_000 })
    await page.locator('[data-entity="kokoro-82m"]').click()
    return calls
  }
  const card = (page) => page.locator('[data-testid="installed-models-pane"]').getByTestId('model-update-card')

  test('the inspector adopts, checks and says so', async ({ page }) => {
    const calls = await open(page, [legacy('kokoro-82m')])
    await expect(card(page)).toContainText('Adopt it to get updates. Your config is not changed.')
    await card(page).getByTestId('model-card-adopt').click()
    await expect(toast(page)).toContainText('kokoro-82m adopted from gallery localai')
    expect(calls.adopt).toEqual([{}])
    expect(calls.checks).toBe(1)
    await expect(card(page)).toHaveAttribute('data-state', 'current')
    await expect(card(page).getByTestId('model-card-adopt')).toHaveCount(0)
  })

  test('several galleries open the choice in the Models page too', async ({ page }) => {
    const calls = await open(page, [legacy('kokoro-82m')], {
      refuse: { status: 409, json: { error: 'name the gallery', candidates: ['localai@kokoro-82m', 'community@kokoro-82m'] } },
    })
    await card(page).getByTestId('model-card-adopt').click()
    await expect(dialog(page)).toBeVisible()
    await expect(dialog(page).getByTestId('model-adopt-option')).toHaveCount(2)
    expect(calls.checks).toBe(0)
  })

  test('no Adopt on a model that is not unknown', async ({ page }) => {
    await open(page, [modelItem('kokoro-82m', 'update-available')])
    await expect(card(page)).toBeVisible()
    await expect(card(page).getByTestId('model-card-adopt')).toHaveCount(0)
  })

  test('a person who is not an administrator sees no Adopt and no update request is made', async ({ page }) => {
    await page.route('**/api/auth/status', route => route.fulfill({ json: { authEnabled: true, staticApiKeyRequired: false, providers: ['local'], hasUsers: true, registrationMode: 'approval', user: { id: 'u2', email: 'b@x', name: 'bob', role: 'user', provider: 'local', permissions: { chat: true } } } }))
    await mockLedger(page)
    const seen = []
    page.on('request', req => { if (/\/api\/models\/(updates|[^/]+\/adopt)/.test(req.url())) seen.push(req.url()) })
    await page.goto('/app/models?view=installed')
    await expect(page.locator('.sidebar')).toBeVisible()
    await page.waitForTimeout(800)
    expect(seen).toEqual([])
    await expect(page.getByTestId('model-card-adopt')).toHaveCount(0)
    await expect(page.getByTestId('model-adopt')).toHaveCount(0)
  })
})
