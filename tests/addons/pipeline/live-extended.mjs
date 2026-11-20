// Explicit opt-in: real inference and uploads in a NEW isolated QA pipeline only.
import { createRequire } from 'node:module'
import { existsSync, mkdirSync } from 'node:fs'
import assert from 'node:assert/strict'
const require = createRequire(new URL('../../../core/http/react-ui/package.json', import.meta.url))
const { chromium, expect } = require('@playwright/test')
if (process.env.RUN_LIVE_PIPELINE_QA !== '1') throw new Error('Set RUN_LIVE_PIPELINE_QA=1 only after deployment and backend installation are ready.')
const meshPath = process.env.PIPELINE_TEST_MESH
if (!meshPath || !existsSync(meshPath)) throw new Error('PIPELINE_TEST_MESH must identify an unrigged, vertex-color GLB accepted by the remesher.')
const base = process.env.LOCALAI_URL || 'https://localai-dev.d.richiejp.com'
const models = { remesh: process.env.PIPELINE_REMESH_MODEL || 'mesh-remesher', rig: process.env.PIPELINE_SKIN_MODEL || 'skintokens-f16', motion: process.env.PIPELINE_MOTION_MODEL || 'kimodo', retarget: process.env.PIPELINE_SKIN_MODEL || 'skintokens-f16' }
const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_CHROMIUM_PATH, args: ['--no-sandbox', '--enable-unsafe-swiftshader'] })
const page = await browser.newPage({ viewport: { width: 1600, height: 1100 }, acceptDownloads: true })
const errors = []
page.on('pageerror', e => errors.push(e.message))
const card = id => page.locator(`[data-node="${id}"]`)
const completed = name => page.locator('.ap-event-completed').filter({ has: page.locator('strong', { hasText: name }) }).last()
const stamp = Date.now(), name = `Extended live QA ${stamp}`
const resultPath = suffix => new URL(`./results/live-${stamp}-${suffix}`, import.meta.url).pathname
mkdirSync(new URL('./results/', import.meta.url), { recursive: true })
async function save(node) {
 await node.getByRole('button', { name: 'Save', exact: true }).click()
 await expect(page.getByTestId('configuration-save-state')).not.toHaveText('Saving changes…', { timeout: 60000 })
 await expect(node.getByRole('button', { name: 'Save', exact: true })).toBeDisabled()
}
try {
 await page.goto(`${base}/app/addons/asset-pipeline/pipelines`)
 await page.getByRole('switch', { name: 'Animation (WIP)' }).check()
 await expect(page.getByLabel('Saved pipeline')).toBeVisible({ timeout: 60000 })
 const create = page.locator('details').filter({ has: page.locator('summary', { hasText: /^Create a pipeline$/ }) })
 if (!(await create.evaluate(el => el.open))) await create.locator('summary').click()
 await page.getByLabel('New pipeline name').fill(name)
 await page.getByRole('button', { name: 'New pipeline', exact: true }).click()
 await expect(page.getByLabel('Saved pipeline').locator('option:checked')).toHaveText(name)
 console.log('Created isolated QA pipeline', name, await page.getByLabel('Saved pipeline').inputValue())
 await expect(page.locator('.ap-node')).toHaveCount(12)
 await card('retarget').getByRole('radio').check()
 await card('remesh').getByRole('combobox', { name: 'Input', exact: true }).selectOption('direct')
 await card('remesh').getByLabel('Upload 3D asset', { exact: true }).setInputFiles(meshPath)
 await expect(card('remesh').getByRole('button', { name: 'Load 3D preview', exact: true })).toBeVisible({ timeout: 60000 })
 for (const [id, model] of Object.entries(models)) await card(id).getByRole('combobox', { name: /^Model/ }).selectOption(model)
 await card('remesh').getByLabel('Remeshing mode').selectOption(process.env.PIPELINE_REMESH_MODE || 'wrap_quad')
 await card('remesh').getByLabel('Target quads (density hint)').fill(process.env.PIPELINE_TARGET_QUADS || '5000')
 await card('remesh').getByLabel('Texture size', { exact: true }).fill('512')
 await card('motion').getByRole('combobox', { name: 'Input', exact: true }).selectOption('direct')
 await card('motion').getByLabel('Description', { exact: true }).fill('A person walks forward at a steady pace.')
 await card('motion').getByLabel('Frames (30 FPS)').fill('60')
 await card('motion').getByLabel('Sampling steps').fill(process.env.PIPELINE_MOTION_STEPS || '10')
 for (const node of await page.locator('.ap-node').all()) if (await node.getByRole('button', { name: 'Save', exact: true }).isEnabled()) await save(node)
 for (const [id, title] of [['remesh', 'Remeshing'], ['rig', 'Generate rig and weights'], ['motion', 'Motion generation'], ['retarget', 'Apply motion']]) {
   const started = Date.now()
   await expect(card(id).getByRole('button', { name: 'Run', exact: true })).toBeEnabled()
   await card(id).getByRole('button', { name: 'Run', exact: true }).click()
   await expect.poll(async () => {
     const badges = await card(id).locator('.ap-badges').innerText()
     if (/failed|interrupted/.test(badges)) throw new Error(`${title} failed: ${await page.locator('.ap-timeline').innerText().catch(() => page.locator('.ap-split').innerText())}`)
     return badges.includes('succeeded')
   }, { timeout: 20 * 60_000, intervals: [1000, 2000, 5000] }).toBe(true)
   console.log('Completed', id, Math.round((Date.now() - started) / 1000), 'seconds')
   const output = completed(title)
   await expect(output.getByLabel('Interactive 3D model preview')).toBeVisible({ timeout: 60000 })
   const canvas = output.locator('canvas')
   await canvas.scrollIntoViewIfNeeded()
   await page.waitForTimeout(500)
   await canvas.screenshot({ path: resultPath(`${id}.png`) })
   const downloading = page.waitForEvent('download')
   await output.getByRole('link', { name: 'Download asset' }).click()
   const download = await downloading
   assert.equal(download.suggestedFilename(), 'asset.glb')
   await download.saveAs(resultPath(`${id}.glb`))
   assert.equal(await download.failure(), null)
   if (id === 'motion' || id === 'retarget') {
     await output.getByRole('button', { name: 'Play animation', exact: true }).click()
     await expect.poll(async () => +(await output.getByLabel('Animation time').inputValue())).toBeGreaterThan(.1)
     await output.getByRole('button', { name: 'Pause animation', exact: true }).click()
     await output.getByLabel('Animation time').fill('1')
     await canvas.scrollIntoViewIfNeeded()
     await page.waitForTimeout(300)
     await canvas.screenshot({ path: resultPath(`${id}-pose1.png`) })
   }
 }
 const id = await page.getByLabel('Saved pipeline').inputValue()
 await page.reload()
 await page.getByLabel('Saved pipeline').selectOption(id)
 await expect(card('retarget').getByText('succeeded', { exact: true })).toBeVisible({ timeout: 30000 })
 assert.deepEqual(errors, [])
 console.log('PASS: live uploads, remesh/rig/motion/retarget inference, GLB downloads, playback and persisted reload', name)
} catch (error) {
 await page.screenshot({ path: resultPath('failure.png') }).catch(() => {})
 console.error('QA pipeline retained for inspection:', name)
 throw error
} finally { await browser.close() }
