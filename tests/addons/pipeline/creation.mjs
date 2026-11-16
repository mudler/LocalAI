// Check the expanded creation controls against the production stylesheet.
import { createRequire } from 'node:module'
import { mkdirSync } from 'node:fs'
import assert from 'node:assert/strict'
const require = createRequire(new URL('../../../core/http/react-ui/package.json', import.meta.url))
const { chromium, expect } = require('@playwright/test')
const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_CHROMIUM_PATH, args: ['--no-sandbox'] })
mkdirSync(new URL('./results/', import.meta.url), { recursive: true })
try {
 for (const width of [1440, 390, 320]) {
  const page = await browser.newPage({ viewport: { width, height: 900 } })
  await page.goto(process.env.PIPELINE_TEST_URL || 'http://127.0.0.1:8097')
  const form = page.locator('.ap-new')
  if (!(await form.evaluate(el => el.open))) await form.locator('summary').click()
  const input = page.getByLabel('New pipeline name')
  const button = page.getByRole('button', { name: 'New pipeline', exact: true })
  await expect(input).toBeVisible()
  await expect(button).toBeVisible()
  const field = await input.boundingBox()
  const action = await button.boundingBox()
  assert.ok(Math.abs(field.height - action.height) < 1, 'controls have matching heights')
  if (width === 1440) {
   assert.ok(Math.abs(field.y - action.y) < 1, 'desktop controls align at the top')
   assert.ok(action.x >= field.x + field.width, 'button follows the name field')
  }
  assert.ok(field.x >= 0 && field.x + field.width <= width, 'name field fits viewport')
  assert.ok(action.x >= 0 && action.x + action.width <= width, 'button fits viewport')
  await input.fill('')
  await expect(button).toBeDisabled()
  const name = `Creation layout ${width} ${Date.now()}`
  await input.fill(name)
  await page.screenshot({ path: new URL(`./results/creation-${width}.png`, import.meta.url).pathname })
  await button.click()
  await expect(page.getByLabel('Saved pipeline').locator('option:checked')).toHaveText(name)
  await page.close()
 }
 console.log('PASS: creation alignment, responsive fit and pipeline creation')
} finally {
 await browser.close()
}
