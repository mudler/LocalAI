import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { SHARE_KIND, shareAvailable, sharedOfKind } from '../../../internal/connector-ui/src/share.js'

test('offers sharing only when the host draws the dialog of a binding and the server shares', () => {
  const sdk = { ui: { ConnectionShareDialog: () => null } }
  assert.equal(shareAvailable(sdk, { sharing: true }), true)
  assert.equal(shareAvailable(sdk, { sharing: false }), false, 'local-owner mode shares nothing')
  assert.equal(shareAvailable({ ui: { ShareDialog: () => null } }, { sharing: true }), false, 'a host with only the generic dialog cannot share a binding for the page')
  assert.equal(shareAvailable({ ui: {} }, { sharing: true }), false, 'an older host has no dialog')
  assert.equal(shareAvailable(null, { sharing: true }), false)
  assert.equal(shareAvailable(sdk, null), false)
})

test('keeps the shared bindings of the page kind, and nothing else', () => {
  const listing = { shared: [{ id: 'a', kind: 'telegram', name: 'Team' }, { id: 'b', kind: 'slack', name: 'Other' }, null, { kind: 'telegram' }] }
  assert.deepEqual(sharedOfKind(listing, 'telegram').map((c) => c.id), ['a'])
  assert.deepEqual(sharedOfKind({}, 'telegram'), [])
  assert.equal(SHARE_KIND, 'connection')
})

test('never calls the sharing routes or sets the confirmation header from the page', () => {
  const dir = new URL('../../../internal/connector-ui/src/', import.meta.url)
  for (const name of ['share.js', 'api.js', 'ConnectorPage.jsx']) {
    const src = readFileSync(new URL(name, dir), 'utf8')
    assert.doesNotMatch(src, /\/api\/shares/, name)
    assert.doesNotMatch(src, /X-LocalAI-Confirm/i, name)
    assert.doesNotMatch(src, /connectors\/\$\{[^}]+\}\/share/, name)
  }
})
