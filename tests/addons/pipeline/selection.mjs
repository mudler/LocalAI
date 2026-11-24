// SPDX-License-Identifier: MIT
import { createRequire } from 'node:module'
const require = createRequire(new URL('../../../core/http/react-ui/package.json', import.meta.url))
const { chromium, expect } = require('@playwright/test')
const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_CHROMIUM_PATH, args: ['--no-sandbox'] })
const page = await browser.newPage()
const base = process.env.PIPELINE_TEST_URL || 'http://127.0.0.1:8097'
const errors = []
page.on('pageerror', error => errors.push(error.message))
const selected = page.getByLabel('Saved pipeline')
async function create(name) {
 const form = page.locator('.ap-new')
 if (!await form.evaluate(el => el.open)) await form.locator('summary').click()
 await page.getByLabel('New pipeline name').fill(name)
 await page.getByRole('button', { name: 'New pipeline', exact: true }).click()
 await expect(selected.locator('option:checked')).toHaveText(name)
 return selected.inputValue()
}
try {
 await page.goto(base)
 const first = await create(`Selection A ${Date.now()}`)
 const second = await create(`Selection B ${Date.now()}`)
 await page.reload()
 await expect(selected).toHaveValue(second)
 await selected.selectOption(first)
 await expect(page.locator('[data-node="source"]')).toBeVisible()
 await page.reload()
 await expect(selected).toHaveValue(first)
 await page.goto('about:blank')
 await page.goto(base)
 await expect(selected).toHaveValue(first)
 // Cancelled navigation must not overwrite the remembered selection.
 await page.locator('[data-node="source"]').getByLabel('Description', { exact: true }).fill('Unsaved draft')
 await selected.selectOption(second)
 await page.getByRole('button', { name: 'Keep editing', exact: true }).click()
 await expect(selected).toHaveValue(first)
 await page.reload()
 await expect(selected).toHaveValue(first)
 // Restore only an accessible resource, never blindly read a stale ID.
 await page.evaluate(() => localStorage.setItem('localai.asset-pipeline.selected-resource', 'missing-resource'))
 await page.reload()
 await expect.poll(() => selected.inputValue()).not.toBe('')
 const fallback = await selected.inputValue()
 expect(fallback).not.toBe('missing-resource')
 await page.reload()
 await expect(selected).toHaveValue(fallback)
 await page.addInitScript(() => {
  Object.defineProperty(window, 'localStorage', { get() { throw new Error('Storage blocked') } })
 })
 await page.reload()
 await expect.poll(() => selected.inputValue()).not.toBe('')
 await selected.selectOption(second)
 await expect(selected).toHaveValue(second)
 expect(errors).toEqual([])
 console.log('PASS: creation, explicit selection, refresh, navigation, cancelled navigation, unavailable resource and blocked storage')
} finally { await browser.close() }
