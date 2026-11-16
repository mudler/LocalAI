// Production UI + durable runner with mock inference: explicit workbook controls.
import { createRequire } from 'node:module'
import { mkdirSync } from 'node:fs'
import assert from 'node:assert/strict'
const require = createRequire(new URL('../../../core/http/react-ui/package.json', import.meta.url))
const { chromium, expect } = require('@playwright/test')
const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_CHROMIUM_PATH, args: ['--no-sandbox'] })
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } })
const base = process.env.PIPELINE_TEST_URL || 'http://127.0.0.1:8097'
const errors = []
page.on('pageerror', error => errors.push(error.message))
const card = name => page.locator('.ap-node').filter({ has: page.getByRole('heading', { name, exact: true }) })
const description = () => card('Asset description').getByLabel('Description', { exact: true })
const saveButton = name => card(name).getByRole('button', { name: 'Save', exact: true })
const completed = name => page.locator('.ap-event-completed').filter({ has: page.locator('strong', { hasText: name }) }).last()
async function save(name) {
 await saveButton(name).click()
 await expect(page.getByTestId('configuration-save-state')).not.toHaveText('Saving changes…', { timeout: 15000 })
 await expect(saveButton(name)).toBeDisabled()
}
async function read(resource, branch) {
 resource ||= await page.getByLabel('Saved pipeline').inputValue()
 return (await (await page.request.post(`${base}/rpc`, { data: { method: 'inspect', resource, branch } })).json()).view
}
async function create(name) {
 const detail = page.locator('details').filter({ has: page.locator('summary', { hasText: /^Create a pipeline$/ }) })
 if (!(await detail.evaluate(el => el.open))) await detail.locator('summary').click()
 await page.getByLabel('New pipeline name').fill(name)
 await page.getByRole('button', { name: 'New pipeline', exact: true }).click()
 await expect(page.getByLabel('Saved pipeline').locator('option:checked')).toHaveText(name)
 return page.getByLabel('Saved pipeline').inputValue()
}
mkdirSync(new URL('./results/', import.meta.url), { recursive: true })
try {
 await page.goto(base)
 const resource = await create(`Workbook ${Date.now()}`)
 const initial = await read()
 await expect(card('Asset description').getByRole('button', { name: /^(Run|Rerun)$/ })).toHaveCount(0)
 await expect(saveButton('Asset description')).toBeDisabled()
 await description().fill('Explicitly saved tower')
 await card('Prompt review').getByRole('combobox', { name: /^Model/ }).selectOption('mock-review')
 await expect(saveButton('Asset description')).toBeEnabled()
 await expect(saveButton('Prompt review')).toBeEnabled()
 await page.waitForTimeout(1200)
 assert.equal((await read()).revision, initial.revision, 'editing must never autosave')
 await expect(page.getByRole('button', { name: 'Run all', exact: true })).toBeDisabled()
 await save('Asset description')
 assert.equal((await read()).current.graph.nodes[0].input.value.text, 'Explicitly saved tower')
 assert.equal((await read()).current.graph.nodes.find(n => n.id === 'review').model || '', '', 'Save affects only its card')
 await expect(saveButton('Prompt review')).toBeEnabled()
 await expect(card('Prompt review').getByRole('combobox', { name: /^Model/ })).toHaveValue('mock-review')
 assert.match((await read()).events.at(-1).message, /Explicitly saved tower/)
 await save('Prompt review')
 await card('Image generation').getByRole('combobox', { name: /^Model/ }).selectOption('mock-image')
 await card('Image generation').getByLabel('Primary output').check()
 await expect(page.getByRole('button', { name: 'Run all', exact: true })).toBeDisabled()
 await save('Image generation')
 assert.equal((await read()).current.graph.primary, 'image')
 await expect(page.locator('[data-connection]')).toHaveCount(2)
 await expect(card('Image generation').getByRole('button', { name: 'Run', exact: true })).toBeDisabled()

 // Run all starts fresh, pauses for decisions, and Continue retains predecessors.
 const beforeAll = await read()
 await page.getByRole('button', { name: 'Run all', exact: true }).click()
 await expect(completed('Prompt review').getByRole('button', { name: 'Keep original description' })).toBeVisible({ timeout: 20000 })
 const paused = await read()
 assert.equal(paused.branches.length, beforeAll.branches.length + 1)
 await completed('Prompt review').getByRole('button', { name: 'Keep original description' }).click()
 await page.getByRole('button', { name: 'Continue', exact: true }).click()
 await expect(completed('Image generation').getByRole('img')).toBeVisible({ timeout: 20000 })
 await expect.poll(async () => (await read()).current.nodes.image.status).not.toBe('running')
 assert.equal((await read()).current.nodes.review.attempt.id, paused.current.nodes.review.attempt.id)
 assert.equal((await read()).branch, paused.branch, 'Continue must not create a new run')
 const waitingImage = await read()
 await card('Image generation').getByRole('button', { name: 'Rerun', exact: true }).click()
 await expect.poll(async () => (await read()).current.nodes.image.attempt?.id).not.toBe(waitingImage.current.nodes.image.attempt.id)
 await expect.poll(async () => (await read()).current.nodes.image.status).toBe('waiting')
 assert.equal((await read()).current.nodes.review.attempt.id, paused.current.nodes.review.attempt.id, 'Regenerating candidates preserves their accepted input')
 // If default image pause is enabled, accept before asserting rerun readiness.
 const choose = completed('Image generation').getByRole('button', { name: 'Choose this output' })
 if (await choose.count()) await choose.click()
 const beforeRerun = await read()
 await card('Prompt review').getByRole('button', { name: 'Rerun', exact: true }).click()
 await expect.poll(async () => (await read()).branch).not.toBe(beforeRerun.branch)
 await expect.poll(async () => (await read()).current.nodes.review.attempt?.id).not.toBe(beforeRerun.current.nodes.review.attempt.id)
 await expect(completed('Prompt review').getByRole('button', { name: 'Keep original description' })).toBeVisible({ timeout: 20000 })
 await expect(card('Image generation').getByRole('button', { name: 'Run', exact: true })).toBeDisabled()
 await completed('Prompt review').getByRole('button', { name: 'Keep original description' }).click()
 await expect(card('Image generation').getByRole('button', { name: 'Run', exact: true })).toBeEnabled()
 await description().fill('Unsaved ancestor')
 await expect(card('Prompt review').getByRole('button', { name: /^(Run|Rerun)$/ })).toBeDisabled()
 await expect(card('Image generation').getByRole('button', { name: /^(Run|Rerun)$/ })).toBeDisabled()

 // Navigation never silently saves. Both keep and discard are explicit.
 await page.getByRole('button', { name: 'Rewind', exact: true }).first().click()
 await expect(page.getByRole('dialog')).toBeVisible()
 await page.getByRole('button', { name: 'Keep editing', exact: true }).click()
 await expect(description()).toHaveValue('Unsaved ancestor')
 await page.getByRole('button', { name: 'Rewind', exact: true }).first().click()
 await page.getByRole('button', { name: 'Discard changes', exact: true }).click()
 await expect(page.getByText(/Inspecting checkpoint from/)).toBeVisible()
 assert.equal((await read()).current.graph.nodes[0].input.value.text, 'Explicitly saved tower')
 await page.getByRole('button', { name: 'Return to latest', exact: false }).click()

 // Definitive save failure retains draft and retries only the requested card.
 await page.route('**/rpc', route => route.request().postDataJSON()?.command?.action === 'configure'
   ? route.fulfill({ status: 400, body: 'Deliberate save rejection' }) : route.continue())
 await description().fill('Recover this draft')
 await saveButton('Asset description').click()
 await expect(page.getByTestId('configuration-save-state')).toHaveText('Changes not saved')
 await expect(description()).toHaveValue('Recover this draft')
 await page.unroute('**/rpc')
 await page.getByRole('button', { name: 'Retry saving', exact: true }).click()
 await expect(page.getByTestId('configuration-save-state')).toHaveText('All changes saved')
 await expect(saveButton('Asset description')).toBeDisabled()
 assert.equal((await read()).current.graph.nodes[0].input.value.text, 'Recover this draft')

 // Explicit saves serialize edits; a later edit stays unsaved after completion.
 let delayed = false
 await page.route('**/rpc', async route => {
   if (route.request().postDataJSON()?.command?.action === 'configure' && !delayed) {
     delayed = true
     await new Promise(resolve => setTimeout(resolve, 900))
   }
   await route.continue()
 })
 await description().fill('First saved value')
 await saveButton('Asset description').click()
 await expect.poll(() => delayed).toBe(true)
 await expect(description()).toBeDisabled()
 await expect.poll(async () => (await read()).current.graph.nodes[0].input.value.text).toBe('First saved value')
 await expect(description()).toBeEnabled()
 await description().fill('Later unsaved value')
 await expect(description()).toHaveValue('Later unsaved value')
 await expect(saveButton('Asset description')).toBeEnabled()
 await page.unroute('**/rpc')
 await save('Asset description')

 // Lost response reconciliation must reuse the operation, not duplicate a save.
 let lost = false
 const operations = []
 await page.route('**/rpc', async route => {
   const data = route.request().postDataJSON()
   if (data?.command?.action === 'configure') {
     operations.push(data.operation)
     if (!lost) { lost = true; await route.fetch(); return route.abort('failed') }
   }
   return route.continue()
 })
 await description().fill('Reconcile this save')
 await saveButton('Asset description').click()
 await expect(page.getByRole('button', { name: 'Reconcile last action', exact: true })).toBeVisible()
 await page.getByRole('button', { name: 'Reconcile last action', exact: true }).click()
 await expect(page.getByRole('button', { name: 'Reconcile last action', exact: true })).toHaveCount(0)
 await expect(saveButton('Asset description')).toBeDisabled()
 assert.equal(operations.length, 2)
 assert.equal(operations[0], operations[1])
 await page.unroute('**/rpc')

 // Concurrent saved state is retained on its branch; recovery saves one card.
 const concurrent = await read()
 let started = false, release
 const gate = new Promise(resolve => { release = resolve })
 await page.route('**/rpc', async route => {
   if (route.request().postDataJSON()?.command?.action === 'configure' && !started) { started = true; await gate }
   await route.continue()
 })
 await description().fill('This session retained value')
 await saveButton('Asset description').click()
 await expect.poll(() => started).toBe(true)
 const graph = structuredClone(concurrent.current.graph)
 graph.nodes[0].input.value.text = 'Other session saved value'
 const response = await page.request.post(`${base}/rpc`, { data: { method: 'apply', resource, operation: crypto.randomUUID(), revision: concurrent.revision, command: { action: 'configure', branch: concurrent.branch, graph } } })
 assert.equal(response.ok(), true)
 release()
 await expect(page.getByRole('button', { name: 'Save edits in new branch', exact: true })).toBeVisible()
 await page.unroute('**/rpc')
 await page.getByRole('button', { name: 'Save edits in new branch', exact: true }).click()
 await expect(page.getByTestId('configuration-save-state')).toHaveText('All changes saved')
 await expect(saveButton('Asset description')).toBeDisabled()
 assert.equal((await read()).current.graph.nodes[0].input.value.text, 'This session retained value')
 assert.equal((await read(resource, concurrent.branch)).current.graph.nodes[0].input.value.text, 'Other session saved value')

 // Active-only connectors and keyboard/mobile visual geometry remain usable.
 await card('Image generation').getByRole('combobox', { name: /^Input/ }).selectOption('source')
 await save('Image generation')
 await expect(page.locator('[data-connection]')).toHaveCount(1)
 assert.deepEqual(await page.locator('[data-connection]').evaluateAll(els => els.map(el => el.dataset.connection)), ['source:image'])
 await expect(card('Prompt review').getByText('Inactive', { exact: true })).toBeVisible()

 // A committed Run all with a lost response reconciles the same branch/job.
 let lostRun = false
 const runOperations = []
 const beforeLostRun = await read()
 await page.route('**/rpc', async route => {
   const data = route.request().postDataJSON()
   if (data?.command?.action === 'run-all') {
     runOperations.push(data.operation)
     if (!lostRun) { lostRun = true; await route.fetch(); return route.abort('failed') }
   }
   return route.continue()
 })
 await page.getByRole('button', { name: 'Run all', exact: true }).click()
 await expect(page.getByRole('button', { name: 'Reconcile last action', exact: true })).toBeVisible()
 await expect(page.getByRole('button', { name: 'Run all', exact: true })).toBeDisabled()
 await page.getByRole('button', { name: 'Reconcile last action', exact: true }).click()
 await expect(page.getByRole('button', { name: 'Reconcile last action', exact: true })).toHaveCount(0)
 await expect.poll(async () => (await read()).current.nodes.image.status).not.toBe('running')
 assert.equal((await read()).branches.length, beforeLostRun.branches.length + 1)
 assert.equal(runOperations.length, 2)
 assert.equal(runOperations[0], runOperations[1])
 await page.unroute('**/rpc')

 // Lost historical fork response does not discard the card's uncommitted edit.
 const beforeHistorical = await read()
 let lostFork = false
 const forkOperations = []
 await page.route('**/rpc', async route => {
   const data = route.request().postDataJSON()
   if (data?.command?.action === 'fork') {
     forkOperations.push(data.operation)
     if (!lostFork) { lostFork = true; await route.fetch(); return route.abort('failed') }
   }
   return route.continue()
 })
 await page.getByRole('button', { name: 'Rewind', exact: true }).first().click()
 await description().fill('Retain historical card draft')
 await saveButton('Asset description').click()
 await page.getByRole('button', { name: 'Reconcile last action', exact: true }).click()
 await expect(saveButton('Asset description')).toBeEnabled()
 await expect(description()).toHaveValue('Retain historical card draft')
 assert.equal((await read()).branches.length, beforeHistorical.branches.length + 1)
 assert.equal(forkOperations.length, 2)
 assert.equal(forkOperations[0], forkOperations[1])
 await page.unroute('**/rpc')
 await save('Asset description')
 assert.equal((await read()).current.graph.nodes[0].input.value.text, 'Retain historical card draft')

 await page.emulateMedia({ reducedMotion: 'reduce' })
 if (await page.locator('.ap-next').count()) assert.equal(await page.locator('.ap-next').evaluate(el => getComputedStyle(el).animationName), 'none')
 await page.emulateMedia({ reducedMotion: 'no-preference' })
 async function geometry() {
   assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth), false)
   for (const path of await page.locator('[data-connection] > path:first-child').evaluateAll(els => els.map(el => el.getAttribute('d')))) {
     const values = path.match(/[\d.]+/g).map(Number)
     assert.equal(values.length, 5)
     assert.ok(values[2] > values[0] && values[3] > values[1] && values[4] < values[2])
   }
 }
 await geometry()
 await page.screenshot({ path: new URL('./results/workbook-desktop.png', import.meta.url).pathname, fullPage: true })
 await page.setViewportSize({ width: 390, height: 844 })
 await expect.poll(async () => page.locator('.ap-connections').evaluate(el => el.getBoundingClientRect().width)).toBeLessThan(390)
 await geometry()
 await page.screenshot({ path: new URL('./results/workbook-mobile.png', import.meta.url).pathname, fullPage: true })
 assert.deepEqual(errors, [])
 console.log('PASS: explicit per-card saving, draft isolation, Run/Rerun/Run all/Continue, navigation safeguards, save failures/serialization/reconciliation/conflicts, active-chain connectors and mobile/reduced-motion layout')
} catch (error) {
 await page.screenshot({ path: new URL('./results/workbook-failure.png', import.meta.url).pathname, fullPage: true })
 console.error((await page.locator('body').innerText()).slice(0, 14000))
 throw error
} finally { await browser.close() }
