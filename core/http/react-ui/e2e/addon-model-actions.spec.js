import { test, expect } from './coverage-fixtures.js'
import { mockLedger, GALLERY } from './ledger-fixtures.js'
const model = 'org/model:雪?&'
const provider = (id = 'creative') => ({ id, title: 'Creative', generation: 3, enabled: true, state: 'running', ui: { sdk: {min:2,max:2}, entry: `/api/addons/${id}/assets/${'a'.repeat(64)}/ui.js`, pages:['canvas'], modelActions:[{capability:'FLAG_IMAGE',page:'canvas',path:'images'}] } })
async function setup(page, providers) {
 await mockLedger(page, { installed: [{id:model,backend:'diffusers',capabilities:['FLAG_IMAGE']}], gallery: [{...GALLERY[0], name:model, tags:['image']}] })
 await page.route('**/api/auth/status', r=>r.fulfill({json:{authEnabled:false}}))
 await page.route('**/api/features', r=>r.fulfill({json:{addons:true}}))
 const state = { providers, reads:0, assets:0 }
 await page.route('**/api/addons/apps', r=>{state.reads++;return r.fulfill({json:{apps:state.providers}})})
 await page.route('**/api/addons/*/assets/**', r=>{state.assets++;return r.fulfill({status:403,body:''})})
 return state
}
async function open(page, surface) {
 if (surface === 'detail') await page.goto(`/app/models/${encodeURIComponent(model)}`)
 else {
  await page.goto(surface === 'installed' ? '/app/models?view=installed' : '/app/models')
  await page.getByTestId('models-search').fill(model)
  await page.locator('[data-entity]').filter({hasText:model}).first().click()
 }
 return page.locator('[data-model-action="FLAG_IMAGE"]').first()
}
for (const surface of ['installed','gallery','detail']) {
 test(`model actions ${surface}: arbitrary single provider one click encodes model without eager load`, async ({page})=>{
  const state = await setup(page,[provider()]); const action = await open(page,surface)
  await expect(action).toHaveAttribute('data-action-status','ready')
  expect(state.assets).toBe(0); const reads = state.reads
  await action.getByRole('button').click()
  await expect(page).toHaveURL(/\/app\/addons\/creative\/canvas\/images\?model=/)
  expect(new URL(page.url()).searchParams.get('model')).toBe(model)
  expect(state.reads).toBeGreaterThan(reads)
 })
 test(`model actions ${surface}: named collision choices never choose first provider`, async ({page})=>{
  const state = await setup(page,[provider('zebra'),provider('arbitrary')]); const action = await open(page,surface)
  await expect(action).toHaveAttribute('data-action-status','ready')
  await action.getByRole('button').click()
  const menu = page.getByRole('menu')
  await expect(menu.getByRole('menuitem')).toHaveText(['Creative (arbitrary)','Creative (zebra)'])
  expect(state.assets).toBe(0)
  await menu.getByRole('menuitem',{name:'Creative (zebra)',exact:true}).click()
  await expect(page).toHaveURL(/\/app\/addons\/zebra\/canvas\/images\?model=/)
 })
 test(`model actions ${surface}: absence retains label and never promotes installation`, async ({page})=>{
  await setup(page,[]); const action = await open(page,surface)
  await expect(action).toHaveAttribute('data-action-status','absent')
  await expect(action).toContainText(/image/i)
  await expect(action.getByRole('button')).toHaveCount(0)
  await expect(action.getByRole('link')).toHaveCount(0)
 })
 test(`model actions ${surface}: changed entry refuses captured choice without retarget`, async ({page})=>{
  const state = await setup(page,[provider()]); const action = await open(page,surface)
  await expect(action).toHaveAttribute('data-action-status','ready')
  state.providers = [{...provider(),generation:4}]
  await action.getByRole('button').click()
  await expect(action).toHaveAttribute('data-action-status','stale')
  expect(new URL(page.url()).pathname).not.toContain('/app/addons/')
 })
}
test('removed addon addresses reach NotFound rather than addon or install promotion',async({page})=>{
 await setup(page,[])
 for (const path of ['studio','talk','image','video','3d','tts','sound','diarization','transform','face','voice','voice-library','fine-tune','quantize']) {
  await page.goto(`/app/${path}`)
  await expect(page.getByTestId('not-found')).toBeVisible()
  expect(new URL(page.url()).pathname).toBe(`/app/${path}`)
 }
})
