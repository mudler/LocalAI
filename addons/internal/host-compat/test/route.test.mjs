import test from 'node:test'
import assert from 'node:assert/strict'
import { paramsOf, parseAddress, resolveTarget } from '../src/route.js'

test('parseAddress splits path, query and hash', () => {
  assert.deepEqual(parseAddress('/app/studio/images?a=1&b=2#top'), { pathname: '/app/studio/images', search: 'a=1&b=2', hash: '#top' })
  assert.deepEqual(parseAddress('/app'), { pathname: '/app', search: '', hash: '' })
})

test('an address of the add-on itself becomes a subpath: the tab in the path, the model in the query', () => {
  assert.deepEqual(resolveTarget('/app/studio', 'studio'), { own: true, subpath: 'studio' })
  assert.deepEqual(resolveTarget('/app/studio/', 'studio'), { own: true, subpath: 'studio' })
  assert.deepEqual(resolveTarget('/app/studio/images', 'studio'), { own: true, subpath: 'studio/images' })
  assert.deepEqual(resolveTarget('/app/studio/images/stable%2Fdiffusion?work=w1', 'studio'), { own: true, subpath: 'studio/images?work=w1&model=stable%2Fdiffusion' })
  assert.deepEqual(resolveTarget('/app/studio?work=a b', 'studio'), { own: true, subpath: 'studio?work=a+b' })
  assert.deepEqual(resolveTarget('/app/talk', 'talk'), { own: true, subpath: 'talk' })
})

test('the other moved page is reached by its add-on address', () => {
  assert.deepEqual(resolveTarget('/app/talk', 'studio'), { own: false, address: '/app/addons/talk/talk' })
  assert.deepEqual(resolveTarget('/app/studio/tts/piper#x', 'talk'), { own: false, address: '/app/addons/studio/studio/tts?model=piper#x' })
})

test('any other address under /app is opened as it is, and an address outside it is refused', () => {
  assert.deepEqual(resolveTarget('/app/models?view=installed', 'studio'), { own: false, address: '/app/models?view=installed' })
  assert.deepEqual(resolveTarget('/app/model-editor/x%2Fy', 'talk'), { own: false, address: '/app/model-editor/x%2Fy' })
  assert.deepEqual(resolveTarget('/app', 'talk'), { own: false, address: '/app' })
  assert.equal(resolveTarget('/api/models', 'studio'), null)
  assert.equal(resolveTarget('https://example.com/app', 'studio'), null)
  assert.equal(resolveTarget('/appendix', 'studio'), null)
  assert.equal(resolveTarget('/app/studiox', 'studio').own, false)
})

test('params: the tab is the first segment, the model is ?model= or the second segment', () => {
  assert.deepEqual(paramsOf({ path: '', search: '' }), { tab: undefined, model: undefined })
  assert.deepEqual(paramsOf({ path: 'images', search: '' }), { tab: 'images', model: undefined })
  assert.deepEqual(paramsOf({ path: 'images', search: 'model=a%2Fb%3Ac' }), { tab: 'images', model: 'a/b:c' })
  assert.deepEqual(paramsOf({ path: 'images/by-hand', search: '' }), { tab: 'images', model: 'by-hand' })
  assert.deepEqual(paramsOf({ path: 'images/by-hand', search: 'model=wins' }), { tab: 'images', model: 'wins' })
})
