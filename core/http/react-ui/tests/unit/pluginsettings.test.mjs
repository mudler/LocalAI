import test from 'node:test'
import assert from 'node:assert/strict'
import { FIELD_BY_KEY, getValue, isChanged } from '../../src/utils/settingsSchema.js'
import { payloadFor } from '../../src/utils/settingsPending.js'

test('plugin settings: personal installs and the size limit are in Settings > Tools, live', () => {
  const users = FIELD_BY_KEY.plugins_user_installs
  assert.ok(users, 'plugins_user_installs is in the schema')
  assert.equal(users.kind, 'bool')
  assert.equal(users.group, 'tools')
  assert.equal(users.apply, 'live')
  assert.equal(getValue(users, {}), true)
  assert.equal(isChanged(users, { plugins_user_installs: false }), true)
  assert.deepEqual(payloadFor([{ field: users }], { plugins_user_installs: false }), { plugins_user_installs: false })

  const size = FIELD_BY_KEY.plugins_max_bytes
  assert.ok(size, 'plugins_max_bytes is in the schema')
  assert.equal(size.kind, 'int')
  assert.equal(size.default, 20 << 20)
  assert.equal(size.min, 1 << 20)
  assert.equal(size.max, 200 << 20)
  assert.equal(size.unit, 'bytes')
})
