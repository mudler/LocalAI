import { test, expect } from './coverage-fixtures.js'
import { mockStudio, sampleHistory, seedHistory, stubMedia } from './studio-fixtures.js'

// The lineage view: a board of what a result was made from and what came out of
// it, a dock to act on the selection, and hand-off to the workspaces.

const node = (page, id) => page.locator(`[data-testid="lineage-node"][data-id="${id}"]`)
const dock = (page) => page.locator('[data-testid="studio-dock"]')

async function open(page, work, { types } = {}) {
  await mockStudio(page, { types })
  await stubMedia(page)
  await seedHistory(page, sampleHistory())
  await page.goto(`/app/studio?work=${work}`)
  await expect(page.locator('[data-testid="studio-lineage"]')).toBeVisible()
}

test.describe('Lineage view', () => {
  test('opening a stack shows its prompt, its three results and the labelled edges', async ({ page }) => {
    await open(page, 'vid-push')
    await expect(page.locator('[data-testid="studio-lineage"]')).toHaveAttribute('data-count', '3')
    await expect(page.locator('[data-testid="lineage-node"]')).toHaveCount(3)
    await expect(page.locator('[data-testid="lineage-source"]')).toHaveCount(1)
    await expect(page.locator('.studio-edge-label')).toHaveText(['take', 'animate'])
    await expect(page.locator('.studio-lineage__sub')).toContainText('3 results that came from each other')
  })

  test('a tile opens the view, Your work and Esc close it, and the address says where you are', async ({ page }) => {
    await mockStudio(page)
    await stubMedia(page)
    await seedHistory(page, sampleHistory())
    await page.goto('/app/studio')
    await page.locator('[data-testid="work-project"] button').first().click()
    await expect(page).toHaveURL(/\/app\/studio\?work=vid-push/)
    await expect(node(page, 'vid-push')).toHaveAttribute('data-selected', 'true')
    await page.keyboard.press('Escape')
    await expect(page.locator('[data-testid="studio-lineage"]')).toHaveCount(0)
    await page.locator('[data-testid="work-tile"][data-id="img-forest"] button').first().click()
    await expect(page.locator('[data-testid="lineage-node"]')).toHaveCount(1)
    await page.locator('[data-testid="studio-back"]').click()
    await expect(page.locator('[data-testid="studio-work"]')).toBeVisible()
  })

  test('a single result opens with its prompt and itself', async ({ page }) => {
    await open(page, 'img-bowls')
    await expect(page.locator('[data-testid="lineage-source"]')).toContainText('three ceramic bowls')
    await expect(page.locator('[data-testid="lineage-node"]')).toHaveCount(1)
    await expect(page.locator('.studio-lineage__sub')).toContainText('One result')
  })

  test('a result that is not in this browser says so and offers the way back', async ({ page }) => {
    await open(page, 'nope')
    await expect(page.locator('.studio-empty')).toContainText('no longer in this browser')
    await page.locator('.studio-lineage button', { hasText: 'Your work' }).click()
    await expect(page.locator('[data-testid="studio-work"]')).toBeVisible()
  })

  test('selecting a node moves the dock and redraws the heavy path', async ({ page }) => {
    await open(page, 'vid-push')
    // The dashed suggestion is not part of the lineage, so it is not counted.
    const heavy = () => page.locator('.studio-edges path[data-chain="true"]:not([data-ghost])').count()
    expect(await heavy()).toBe(2)
    await node(page, 'img-harbour-2').click()
    await expect(node(page, 'img-harbour-2')).toHaveAttribute('data-selected', 'true')
    await expect(dock(page)).toContainText('fishing harbour')
    await expect(node(page, 'vid-push')).toHaveAttribute('data-rel', '0')
    // Root to the take: the prompt edge and the take edge. The clip's edge is thin.
    await expect.poll(heavy).toBe(1)
    await node(page, 'vid-push').click()
    await expect(dock(page)).toContainText('Slow push-in')
    await expect.poll(heavy).toBe(2)
  })

  test('arrow keys walk the board', async ({ page }) => {
    await open(page, 'vid-push')
    await page.keyboard.press('ArrowLeft')
    await expect(node(page, 'img-harbour')).toHaveAttribute('data-selected', 'true')
    await page.keyboard.press('ArrowDown')
    await expect(node(page, 'img-harbour-2')).toHaveAttribute('data-selected', 'true')
    await page.keyboard.press('ArrowUp')
    await page.keyboard.press('ArrowRight')
    await expect(node(page, 'vid-push')).toHaveAttribute('data-selected', 'true')
  })

  test('the dock opens to a large preview, the prompt and what can come next', async ({ page }) => {
    await open(page, 'img-harbour')
    await page.locator('[data-testid="studio-dock-toggle"]').click()
    const detail = page.locator('[data-testid="studio-detail"]')
    await expect(detail.locator('img')).toBeVisible()
    await expect(detail).toContainText('A fishing harbour at first light')
    await expect(detail).toContainText('1216x832')
    for (const edge of ['animate', 'to-3d', 'variation']) await expect(page.locator(`[data-testid="step-${edge}"]`)).toHaveAttribute('data-state', 'ready')
    await page.keyboard.press('Escape')
    await expect(detail).toHaveCount(0)
    await expect(page.locator('[data-testid="studio-lineage"]')).toBeVisible()
  })

  test('a suggested next step hangs off the selection, from the installed models', async ({ page }) => {
    await open(page, 'img-harbour', { types: ['images', 'threed'] })
    const ghost = page.locator('[data-testid="lineage-ghost"]')
    await expect(ghost).toContainText('Make it 3D')
    await expect(ghost).toContainText('trellis-image-to-3d')
    await expect(ghost).toContainText('Suggested')
  })

  test('with nothing installed the suggestion is the first step, marked as needing a model', async ({ page }) => {
    await open(page, 'img-bowls', { types: [] })
    const ghost = page.locator('[data-testid="lineage-ghost"]')
    await expect(ghost).toContainText('Animate')
    await expect(ghost).toContainText('Needs a Video model')
  })
})

test.describe('Lineage view: new take and branch', () => {
  test('Run as a new take opens the same workspace with the words, model and size and the result as parent', async ({ page }) => {
    await open(page, 'img-harbour')
    await page.locator('[data-testid="studio-take"]').click()
    await expect(page).toHaveURL(/\/app\/studio\/images\?/)
    await expect(page).toHaveURL(/from=img-harbour/)
    await expect(page).toHaveURL(/edge=take/)
    await expect(page.locator('[data-testid="ws-compose"] textarea').first()).toHaveValue(/fishing harbour at first light/)
    await expect(page.locator('[data-testid="ws-compose"]')).toContainText('flux.1-schnell')
  })

  test('Branch from here opens a draft with the suggested step, editable words and Open in', async ({ page }) => {
    await open(page, 'img-harbour')
    await page.locator('[data-testid="studio-branch"]').click()
    const draft = page.locator('[data-testid="studio-draft"]')
    await expect(draft).toBeVisible()
    await expect(page.locator('[data-testid="lineage-ghost"]')).toContainText('Draft')
    const words = draft.getByRole('textbox')
    await expect(words).toHaveValue('Slow push-in, small natural motion')
    await words.fill('Gulls crossing the frame')
    await page.locator('[data-testid="studio-draft-open"]').click()
    await expect(page).toHaveURL(/\/app\/studio\/video\?/)
    await expect(page).toHaveURL(/from=img-harbour/)
    await expect(page).toHaveURL(/edge=animate/)
    await expect(page.locator('[data-testid="ws-compose"] textarea').first()).toHaveValue('Gulls crossing the frame')
    await expect(page.locator('[data-testid="studio-handoff"]')).toHaveAttribute('data-status', 'ready')
  })

  test('B branches, Esc cancels the draft first and then closes the dock', async ({ page }) => {
    await open(page, 'img-harbour')
    await page.keyboard.press('b')
    await expect(page.locator('[data-testid="studio-draft"]')).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(page.locator('[data-testid="studio-draft"]')).toHaveCount(0)
    await expect(page.locator('[data-testid="studio-detail"]')).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(page.locator('[data-testid="studio-detail"]')).toHaveCount(0)
    await page.keyboard.press('Escape')
    await expect(page.locator('[data-testid="studio-lineage"]')).toHaveCount(0)
  })

  test('a draft whose model is missing shows what it needs and cannot open', async ({ page }) => {
    await open(page, 'img-harbour', { types: ['images'] })
    await page.locator('[data-testid="studio-branch"]').click()
    await page.locator('[data-testid="draft-target-animate"]').click()
    await expect(page.locator('[data-testid="studio-install-note"]')).toContainText('No Video model installed')
    await expect(page.locator('[data-testid="studio-draft-open"]')).toBeDisabled()
    await page.locator('[data-testid="draft-target-variation"]').click()
    await expect(page.locator('[data-testid="studio-install-note"]')).toHaveCount(0)
    await expect(page.locator('[data-testid="studio-draft-open"]')).toBeEnabled()
  })

  test('a draft needs words when the destination starts from words', async ({ page }) => {
    await open(page, 'img-harbour')
    await page.locator('[data-testid="studio-branch"]').click()
    await page.locator('[data-testid="studio-draft"]').getByRole('textbox').fill('')
    await expect(page.locator('[data-testid="studio-draft-open"]')).toBeDisabled()
  })

  test('Cancel drops the draft', async ({ page }) => {
    await open(page, 'img-harbour')
    await page.locator('[data-testid="studio-branch"]').click()
    await page.locator('[data-testid="studio-draft-cancel"]').click()
    await expect(page.locator('[data-testid="lineage-ghost"]')).not.toContainText('Draft')
    await expect(page.locator('[data-testid="studio-branch"]')).toBeVisible()
  })

  test('audio can be branched into Transform and Diarization from its page', async ({ page }) => {
    await open(page, 'tts-welcome')
    await page.locator('[data-testid="studio-branch"]').click()
    await expect(page.locator('[data-testid="draft-target-diarize"]')).toBeEnabled()
    await page.locator('[data-testid="draft-target-diarize"]').click()
    await page.locator('[data-testid="studio-draft-open"]').click()
    await expect(page).toHaveURL(/\/app\/studio\/diarization\?.*from=tts-welcome.*edge=diarize/)
    await expect(page.locator('[data-testid="studio-handoff"]')).toHaveAttribute('data-status', 'ready')
  })
})

test.describe('Lineage view: what a page cannot take yet', () => {
  test('a clip cannot be branched: the action is disabled and says why', async ({ page }) => {
    await open(page, 'vid-push')
    await expect(page.locator('[data-testid="studio-branch"]')).toBeDisabled()
    await expect(page.locator('[data-testid="studio-dock-hint"]')).toContainText('Nothing accepts a Video as a starting point yet')
    await page.locator('[data-testid="studio-dock-toggle"]').click()
    const step = page.locator('[data-testid="step-soundtrack"]')
    await expect(step).toBeDisabled()
    await expect(step).toContainText('The Sound page cannot start from a video yet')
    // A clip can still be repeated: its prompt and model are kept.
    await expect(page.locator('[data-testid="studio-take"]')).toBeEnabled()
  })

  test('a 3D object cannot be repeated or branched, and the dock says so', async ({ page }) => {
    await mockStudio(page)
    await page.goto('/app/studio')
    const { seedThreeD } = await import('./studio-fixtures.js')
    await seedThreeD(page, [{ id: 'm1', createdAt: Date.now() - 5000, name: 'vase.glb', label: 'Ribbed vase', model: 'trellis-image-to-3d' }])
    await page.goto('/app/studio?work=m1')
    await expect(page.locator('[data-testid="studio-take"]')).toBeDisabled()
    await expect(page.locator('[data-testid="studio-branch"]')).toBeDisabled()
    await expect(page.locator('[data-testid="studio-dock-hint"]')).toContainText('original input was not kept')
  })

  test('a diarization run cannot be repeated because the recording is not kept', async ({ page }) => {
    await open(page, 'dia-sync')
    await expect(page.locator('[data-testid="studio-take"]')).toBeDisabled()
    await expect(page.locator('[data-testid="lineage-source"]')).toContainText('File')
  })

  test('a transform can be repeated from its stored input', async ({ page }) => {
    await open(page, 'trf-clean')
    await page.locator('[data-testid="studio-take"]').click()
    await expect(page).toHaveURL(/\/app\/studio\/transform\?.*from=trf-clean.*edge=take/)
    await expect(page.locator('[data-testid="studio-handoff"]')).toHaveAttribute('data-status', 'ready')
  })
})

test.describe('Lineage view: phone', () => {
  test('the board scrolls sideways and the dock stays inside the screen', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await open(page, 'vid-push')
    const board = page.locator('[data-testid="studio-board"]')
    const dims = await board.evaluate(el => ({ scroll: el.scrollWidth, client: el.clientWidth }))
    expect(dims.scroll).toBeGreaterThan(dims.client)
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    expect(overflow).toBeLessThanOrEqual(0)
    for (const id of ['studio-take', 'studio-branch']) {
      const box = await page.locator(`[data-testid="${id}"]`).boundingBox()
      expect(box.x).toBeGreaterThanOrEqual(0)
      expect(box.x + box.width).toBeLessThanOrEqual(390)
    }
  })
})
