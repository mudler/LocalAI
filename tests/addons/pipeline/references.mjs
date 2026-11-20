// Reference-image configuration and preview-first library selection.
import { createRequire } from 'node:module'
import { mkdirSync } from 'node:fs'
import assert from 'node:assert/strict'
const require = createRequire(new URL('../../../core/http/react-ui/package.json', import.meta.url))
const { chromium, expect } = require('@playwright/test')
const base = process.env.PIPELINE_TEST_URL || 'http://127.0.0.1:8097'
const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_CHROMIUM_PATH, args: ['--no-sandbox'] })
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })
const errors = []
page.on('pageerror', error => errors.push(error.message))
mkdirSync(new URL('./results/', import.meta.url), { recursive: true })
try {
 await page.goto(base)
 await page.evaluate(() => localStorage.setItem('localai_image_history', JSON.stringify([
  ...Array.from({length: 14}, (_, index) => ({ id: `fixture-${index}`, prompt: `Reference ${index}`, results: [{url:'/fixture.png'}] })),
  { id: 'missing', prompt: 'Missing image', results: [{url:'/missing-image.png'}] },
  { id: 'external', prompt: 'External image', results: [{url:'https://example.com/private.png'}] },
 ])))
 await page.locator('summary').filter({hasText:/^Create a pipeline$/}).click()
 await page.getByLabel('New pipeline name').fill(`References ${Date.now()}`)
 await page.getByRole('button',{name:'New pipeline',exact:true}).click()
 const card = page.locator('[data-node="image"]')
 await expect(card).toBeVisible()
 await card.getByRole('combobox',{name:'Input',exact:true}).selectOption('direct')
 await card.getByLabel('Description',{exact:true}).fill('Make the reference red')
 await card.getByRole('combobox',{name:/^Model/}).selectOption('mock-image')
 await card.getByRole('combobox',{name:/^Reference image/}).selectOption('direct')
 await card.getByRole('button',{name:'Choose reference from library'}).click()
 const dialog = page.getByRole('dialog',{name:'Choose an image'})
 await expect(dialog.getByRole('img')).toHaveCount(12)
 await expect(dialog.getByRole('button',{name:'Use selected image'})).toBeDisabled()
 await dialog.getByRole('button',{name:'Next',exact:true}).click()
 await expect(dialog.getByRole('radio')).toHaveCount(3)
 await dialog.getByLabel('Search images').fill('External')
 await expect(dialog.getByText('No images match your search.')).toBeVisible()
 await dialog.getByLabel('Search images').fill('Missing')
 await expect(dialog.getByText('Preview unavailable')).toBeVisible()
 await dialog.getByRole('radio').check()
 await dialog.getByRole('button',{name:'Use selected image'}).click()
 await expect(dialog.getByRole('alert')).toBeVisible()
 await dialog.getByLabel('Search images').fill('Reference 0')
 await expect(dialog.getByRole('img')).toBeVisible()
 await dialog.getByRole('radio').check()
 for (const theme of ['light','dark']) {
  await page.evaluate(theme => document.documentElement.dataset.theme = theme, theme)
  for (const width of [1280,390]) {
   await page.setViewportSize({width,height:900})
   await page.waitForTimeout(300)
   await page.screenshot({path:new URL(`./results/reference-picker-${theme}-${width}.png`,import.meta.url).pathname})
  }
 }
 await dialog.getByRole('button',{name:'Use selected image'}).click()
 await expect(dialog).toHaveCount(0)
 await expect(card.getByRole('img')).toBeVisible()
 await card.getByRole('button',{name:'Save',exact:true}).click()
 await expect(card.getByRole('button',{name:'Save',exact:true})).toBeDisabled()
 const resource = await page.getByLabel('Saved pipeline').inputValue()
 const read = async () => (await (await page.request.post(`${base}/rpc`,{data:{method:'inspect',resource}})).json()).view
 const saved = (await read()).current.graph.nodes.find(n => n.id === 'image').inputs.reference.value.artifact
 assert.ok(saved)
 await card.getByRole('button',{name:'Run',exact:true}).click()
 await expect(card.getByText('Waiting for your selection',{exact:true})).toBeVisible({timeout:15000})
 assert.equal((await read()).current.nodes.image.attempt.arguments.inputs.reference.artifact,saved)
 await page.reload()
 await expect(card.getByRole('img')).toBeVisible()
 await card.getByRole('combobox',{name:/^Reference image/}).selectOption('none')
 await card.getByRole('button',{name:'Save',exact:true}).click()
 await expect(card.getByRole('button',{name:'Save',exact:true})).toBeDisabled()
 assert.equal((await read()).current.graph.nodes.find(n => n.id === 'image').inputs?.reference,undefined)
 assert.deepEqual(errors,[])
 console.log('PASS: reference upload from preview picker, search, pagination, unavailable/external images, saved execution inputs, reload and removal')
} finally { await browser.close() }
