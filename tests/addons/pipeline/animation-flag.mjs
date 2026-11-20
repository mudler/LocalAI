// SPDX-License-Identifier: MIT
// The experimental animation controls are an opt-in, non-destructive UI flag.
import { createRequire } from 'node:module'
import { mkdirSync } from 'node:fs'
import assert from 'node:assert/strict'
const require = createRequire(new URL('../../../core/http/react-ui/package.json', import.meta.url))
const { chromium, expect } = require('@playwright/test')
const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_CHROMIUM_PATH, args: ['--no-sandbox'] })
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })
const base = process.env.PIPELINE_TEST_URL || 'http://127.0.0.1:8097'
const errors = []
page.on('pageerror', error => errors.push(error.message))
mkdirSync(new URL('./results/', import.meta.url), { recursive: true })
try {
 await page.goto(base)
 const toggle = page.getByRole('switch', { name: 'Animation (WIP)' })
 await expect(toggle).not.toBeChecked()
 await page.getByText('Create a pipeline', { exact: true }).click()
 await page.getByLabel('New pipeline name').fill(`Animation flag ${Date.now()}`)
 await page.getByRole('button', { name: 'New pipeline', exact: true }).click()
 await expect(page.locator('.ap-node')).toHaveCount(6)
 await expect(page.locator('[data-node="remesh"]')).toBeVisible()
 await expect(page.locator('[data-node="rig"]')).toHaveCount(0)
 await toggle.focus()
 await page.keyboard.press('Space')
 await expect(toggle).toBeChecked()
 await expect(page.locator('.ap-node')).toHaveCount(12)
 const rig = page.locator('[data-node="rig"]')
 await rig.getByRole('radio').check()
 await expect(toggle).toBeDisabled()
 await rig.getByRole('button', { name: 'Save', exact: true }).click()
 await expect(toggle).toBeEnabled()
 const resource = await page.getByLabel('Saved pipeline').inputValue()
 const read = async () => (await (await page.request.post(`${base}/rpc`, { data: { method: 'inspect', resource } })).json()).view
 const before = await read()
 await toggle.uncheck()
 await expect(page.locator('[data-node="rig"]')).toHaveCount(0)
 await expect(page.getByRole('button', { name: 'Run all', exact: true })).toBeDisabled()
 await expect(page.getByText('Enable Animation (WIP) to run this pipeline, or choose a non-animation primary output.')).toBeVisible()
 assert.deepEqual((await read()).current.graph, before.current.graph)
 await page.reload()
 await expect(toggle).not.toBeChecked()
 await expect(page.locator('.ap-node')).toHaveCount(6)
 await expect(page.getByRole('button', { name: 'Run all', exact: true })).toBeDisabled()
 await toggle.check()
 await expect(rig.getByRole('radio')).toBeChecked()
 await page.reload()
 await expect(toggle).toBeChecked()
 await expect(page.locator('.ap-node')).toHaveCount(12)
 for (const theme of ['light', 'dark']) {
  await page.evaluate(theme => document.documentElement.dataset.theme = theme, theme)
  for (const width of [1280, 390]) {
   await page.setViewportSize({ width, height: 900 })
   await toggle.scrollIntoViewIfNeeded()
   await page.waitForTimeout(300)
   await page.screenshot({ path: new URL(`./results/animation-${theme}-${width}.png`, import.meta.url).pathname })
  }
 }
 assert.deepEqual(errors, [])
 console.log('PASS: animation defaults off, keyboard switch, draft protection, dependency guard, configuration preservation, and preference persistence')
} finally { await browser.close() }
