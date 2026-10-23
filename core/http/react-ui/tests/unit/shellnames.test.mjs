import test from 'node:test'
import assert from 'node:assert/strict'
import { displayVersion } from '../../src/utils/version.js'
import { addonDisplayName } from '../../src/components/addons/addonState.js'

test('displayVersion shows nothing for the empty version of a source build', () => {
  assert.equal(displayVersion(' ()'), '')
  assert.equal(displayVersion('()'), '')
  assert.equal(displayVersion(''), '')
  assert.equal(displayVersion(undefined), '')
  assert.equal(displayVersion(null), '')
})

test('displayVersion keeps a real version and drops only an empty commit', () => {
  assert.equal(displayVersion('v3.4.0 (abc123)'), 'v3.4.0 (abc123)')
  assert.equal(displayVersion('v3.4.0 ()'), 'v3.4.0')
  assert.equal(displayVersion(' v3.4.0 '), 'v3.4.0')
})

test('addonDisplayName capitalises the identifier of an add-on the list does not carry', () => {
  assert.equal(addonDisplayName('voice'), 'Voice')
  assert.equal(addonDisplayName('fine-tuning'), 'Fine-tuning')
  assert.equal(addonDisplayName(''), '')
  assert.equal(addonDisplayName(undefined), '')
})
