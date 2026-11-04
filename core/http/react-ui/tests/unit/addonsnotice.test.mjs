import test from 'node:test'
import assert from 'node:assert/strict'
import { addonsNotice } from '../../src/utils/addonsNotice.js'

test('Home add-ons notice: add-ons back on drops the notice', () => {
  assert.deepEqual(addonsNotice('studio', { addons: true, addons_enabled: true }), { addon: '', kind: 'off' })
  assert.equal(addonsNotice('studio', {}).addon, '')
})
test('Home add-ons notice: switch off keeps the notice', () => {
  assert.deepEqual(addonsNotice('studio', { addons: false, addons_enabled: false }), { addon: 'studio', kind: 'off' })
  assert.deepEqual(addonsNotice('studio', { addons: false }), { addon: 'studio', kind: 'off' })
})
test('Home add-ons notice: switch on and owner silent says not answering', () => {
  assert.deepEqual(addonsNotice('studio', { addons: false, addons_enabled: true }), { addon: 'studio', kind: 'unreachable' })
})
test('Home add-ons notice: no state shows nothing, unreadable features keep it', () => {
  assert.equal(addonsNotice('', { addons: false }).addon, '')
  assert.deepEqual(addonsNotice('studio', null), { addon: 'studio', kind: 'off' })
})
