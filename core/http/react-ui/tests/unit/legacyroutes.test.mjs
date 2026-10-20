import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { STUDIO_TABS, studioRedirect, MIGRATION_ROUTES } from '../../src/utils/legacyRoutes.js'

const here = dirname(fileURLToPath(import.meta.url))
const router = readFileSync(join(here, '../../src/router.jsx'), 'utf8')

test('studioRedirect keeps the model, the query and the hash', () => {
  assert.equal(studioRedirect('image', 'stable/diffusion', '?x=1', '#top'), '/app/studio/images/stable%2Fdiffusion?x=1#top')
  assert.equal(studioRedirect('3d', undefined, '', ''), '/app/studio/threed')
  assert.equal(studioRedirect('tts', 'piper', '?voice=a', ''), '/app/studio/tts/piper?voice=a')
})

test('every standalone generator route maps to a Studio tab', () => {
  assert.deepEqual(Object.keys(STUDIO_TABS).sort(), ['3d', 'diarization', 'image', 'sound', 'transform', 'tts', 'video'])
})

test('every moved row targets what studioRedirect builds for its own generator', () => {
  const moved = MIGRATION_ROUTES.filter(r => r.kind === 'studio-tab')
  assert.equal(moved.length, 7)
  for (const r of moved) {
    const key = r.from.replace('/app/', '')
    assert.equal(r.to, studioRedirect(key), `${r.from} targets ${r.to}`)
  }
})

test('each moved route has its own redirect entry, plain and with a model, bound to its own generator', () => {
  const lines = router.split('\n')
  for (const r of MIGRATION_ROUTES.filter(x => x.kind === 'studio-tab')) {
    const key = r.from.replace('/app/', '')
    for (const path of [key, `${key}/:model`]) {
      const entry = lines.find(l => l.includes(`{ path: '${path}', element:`))
      assert.ok(entry, `router.jsx has no entry for path ${path}`)
      assert.ok(entry.includes(`<StudioTabRedirect legacy="${key}" />`), `entry for ${path} is not bound to legacy="${key}": ${entry.trim()}`)
    }
  }
})

test('Studio has a route that carries a model', () => {
  assert.ok(router.includes("path: 'studio/:tab/:model'"))
})

test('the same-path routes need no redirect', () => {
  for (const r of MIGRATION_ROUTES.filter(x => x.kind === 'same')) assert.equal(r.from, r.to)
})

test('moved biometric routes are wrapped in MovedPage in the router', () => {
  for (const r of MIGRATION_ROUTES.filter(x => x.kind === 'addon-page')) {
    const [, , , addon, page] = r.to.split('/')
    assert.ok(router.includes(`<MovedPage addon="${addon}" page="${page}">`) || router.includes(`<MovedPage addon="${addon}" page="${page}" withModel>`), `router.jsx does not wrap ${r.from}`)
  }
})

test('a moved route with a :model segment carries the model as ?model=, not in the path', () => {
  assert.ok(router.includes('withModel'), 'the model routes must use withModel')
  assert.ok(!/\/app\/addons\/\$\{addon\}\/\$\{page\}\/\$\{model\}/.test(router), 'the model must not be appended to the path')
})

test('the face route and the face model route both open the Face add-on, the model as ?model=', () => {
  assert.ok(MIGRATION_ROUTES.some(r => r.from === '/app/face' && r.to === '/app/addons/face/recognition' && r.kind === 'addon-page'))
  const lines = router.split('\n')
  const plain = lines.find(l => l.includes("{ path: 'face', element:"))
  const withModel = lines.find(l => l.includes("{ path: 'face/:model', element:"))
  assert.ok(plain?.includes('<MovedPage addon="face" page="recognition">'), 'face is not wrapped')
  assert.ok(withModel?.includes('<MovedPage addon="face" page="recognition" withModel>'), 'face/:model is not wrapped with withModel')
})

test('the Build hub has no Faces tab', async () => {
  const { buildHub } = await import('../../src/components/hub/hubConfig.js')
  assert.ok(!buildHub.tabs.some(t => t.id === 'faces' || t.id === 'voices'))
})

test('the fine-tune and quantize routes open their add-on pages, around the existing Feature and ToolOff', () => {
  const lines = router.split('\n')
  const rows = [
    { from: '/app/fine-tune', to: '/app/addons/fine-tuning/jobs', addon: 'fine-tuning', tool: 'fineTune', feature: 'fine_tuning', path: 'fine-tune' },
    { from: '/app/quantize', to: '/app/addons/quantization/jobs', addon: 'quantization', tool: 'quantize', feature: 'quantization', path: 'quantize' },
  ]
  for (const r of rows) {
    assert.ok(MIGRATION_ROUTES.some(m => m.from === r.from && m.to === r.to && m.kind === 'addon-page'), `no row for ${r.from}`)
    const entry = lines.find(l => l.includes(`{ path: '${r.path}', element:`))
    assert.ok(entry?.includes(`<MovedPage addon="${r.addon}" page="jobs">`), `${r.path} is not wrapped`)
    assert.ok(entry.includes(`<Feature feature="${r.feature}" disabled={<ToolOff tool="${r.tool}" />}>`), `${r.path} lost its Feature and ToolOff`)
  }
})

test('the Build hub has no fine-tune or quantize tab', async () => {
  const { buildHub } = await import('../../src/components/hub/hubConfig.js')
  assert.ok(!buildHub.tabs.some(t => t.id === 'fine-tune' || t.id === 'quantize'))
})

test('the Build overview keeps the fine-tune and quantize cards, linked to the old routes', async () => {
  const { buildOverviewTools } = await import('../../src/components/hub/hubConfig.js')
  assert.deepEqual(buildOverviewTools.map(t => [t.id, t.items[0].path]), [['fine-tune', '/app/fine-tune'], ['quantize', '/app/quantize']])
})
