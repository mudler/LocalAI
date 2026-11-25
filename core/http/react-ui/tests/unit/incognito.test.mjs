import test from 'node:test'
import assert from 'node:assert/strict'
import {
  INCOGNITO_HEADER, defaultIncognitoLevel, forgetIncognitoChat, incognitoBlocks, incognitoHeaders, incognitoLevels,
  isIncognitoChat, offeredLevel, rememberIncognitoChat, setIncognitoMarker,
} from '../../src/utils/incognito.js'

test('the page offers incognito only when the server allows it, up to its maximum', () => {
  assert.deepEqual(incognitoLevels(undefined), [])
  assert.deepEqual(incognitoLevels({ policy: 'forbid', max_level: 'zero-retention' }), [])
  assert.deepEqual(incognitoLevels({ policy: 'allow', max_level: 'zero-retention' }), ['memory-off', 'zero-retention'])
  assert.deepEqual(incognitoLevels({ policy: 'allow', max_level: 'memory-off' }), ['memory-off'])
  assert.equal(defaultIncognitoLevel({ policy: 'allow', max_level: 'zero-retention' }), 'memory-off')
  assert.equal(defaultIncognitoLevel({ policy: 'forbid' }), '')
  assert.equal(offeredLevel({ policy: 'allow', max_level: 'memory-off' }, 'zero-retention'), 'memory-off')
})

test('the marker goes on every request while set, and only then', () => {
  setIncognitoMarker('')
  assert.deepEqual(incognitoHeaders({ 'Content-Type': 'application/json' }), { 'Content-Type': 'application/json' })
  setIncognitoMarker('zero-retention')
  const h = { 'Content-Type': 'application/json' }
  assert.deepEqual(incognitoHeaders(h), { 'Content-Type': 'application/json', [INCOGNITO_HEADER]: 'zero-retention' })
  assert.equal(INCOGNITO_HEADER in h, false, 'the caller\'s headers are not changed')
  setIncognitoMarker('everything')
  assert.deepEqual(incognitoHeaders(), {}, 'an unknown level clears the marker')
  setIncognitoMarker('')
})

test('the incognito chats of the tab are known by id and forgotten', () => {
  rememberIncognitoChat('c1', 'memory-off')
  rememberIncognitoChat('c2', 'normal')
  assert.equal(isIncognitoChat('c1'), true)
  assert.equal(isIncognitoChat('c2'), false)
  forgetIncognitoChat('c1')
  assert.equal(isIncognitoChat('c1'), false)
})

test('a zero-retention chat takes no attachment; no incognito chat is forked or moved', () => {
  assert.deepEqual(incognitoBlocks(''), { attach: false, fork: false, project: false })
  assert.deepEqual(incognitoBlocks('memory-off'), { attach: false, fork: true, project: true })
  assert.deepEqual(incognitoBlocks('zero-retention'), { attach: true, fork: true, project: true })
})

test('the module keeps nothing in the browser storage', async () => {
  const src = await import('node:fs').then(fs => fs.readFileSync(new URL('../../src/utils/incognito.js', import.meta.url), 'utf8'))
  const code = src.split('\n').filter(l => !l.trim().startsWith('//')).join('\n')
  assert.doesNotMatch(code, /localStorage|sessionStorage|indexedDB/)
})
