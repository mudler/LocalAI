import { test, expect } from '@playwright/test'

// Runs against a REAL local-ai binary with NO route stubbing.
//
// Every other spec here stubs /api/operations. That is how a payload the server
// could not actually emit (isDeletion:true on a live operation) stayed green
// through an entire review while the UI rendered a removal as an install. These
// assertions are only worth anything because the data came from the real handler.
//
//   make build
//   ./local-ai run --address 127.0.0.1:8089 --models-path /tmp/lai-e2e
//   cd core/http/react-ui
//   LOCALAI_REAL_BINARY=1 PLAYWRIGHT_EXTERNAL_SERVER=1 PW_WORKERS=1 \
//     npx playwright test e2e/real-binary-activity.spec.js
//
// Skipped by default so CI, which runs the stub server, is unaffected.
test.skip(!process.env.LOCALAI_REAL_BINARY, 'needs a real local-ai on 127.0.0.1:8089')
test.describe.configure({ mode: 'serial' })

const MISSING = 'definitely-not-a-real-model-xyz'

test('a real failed install lands in Needs attention with the real error and a Retry', async ({ page, request }) => {
  // Self-contained: the gallery resolver rejects an unknown name, so this fails
  // fast without downloading anything.
  await request.post(`/api/models/install/${MISSING}`)

  await page.goto('/app/activity')
  await expect(page.locator('.page-title')).toBeVisible()

  const card = page.locator('.operation-card--error').filter({ hasText: MISSING })
  await expect(card).toBeVisible({ timeout: 20_000 })
  await expect(card).toContainText('no model found with name')
  // Retry is offered for a failed install; it is gated off for a failed removal.
  await expect(card.locator('.operation-card__retry')).toBeVisible()
})

test('the strip shows the real failure on one line and does not widen the page', async ({ page }) => {
  await page.goto('/app/activity')

  const strip = page.locator('.operations-strip')
  await expect(strip).toHaveCount(1)
  await expect(strip).toHaveClass(/operations-strip--error/)
  await expect(strip.locator('.operations-strip__name')).toHaveText(MISSING)

  const box = await page.evaluate(() => ({
    scroll: document.documentElement.scrollWidth,
    client: document.documentElement.clientWidth,
  }))
  expect(box.scroll).toBeLessThanOrEqual(box.client)
})

test('dismissing moves the failure into the record instead of destroying it', async ({ page }) => {
  await page.goto('/app/activity')

  const card = page.locator('.operation-card--error').filter({ hasText: MISSING })
  await expect(card).toBeVisible()
  await card.locator('.operation-card__hide').click()

  await expect(page.locator('.operation-card--error')).toHaveCount(0, { timeout: 15_000 })
  const row = page.locator('.activity-row').filter({ hasText: MISSING })
  await expect(row).toBeVisible({ timeout: 15_000 })
  await expect(row).toContainText('failed')
})

test('Clear history empties the record against the real store', async ({ page }) => {
  await page.goto('/app/activity')

  await expect(page.locator('.activity-row').first()).toBeVisible()
  await page.getByRole('button', { name: /clear history/i }).click()

  await expect(page.locator('.activity-row')).toHaveCount(0, { timeout: 15_000 })
  await expect(page.locator('.activity-empty')).toBeVisible()
})
