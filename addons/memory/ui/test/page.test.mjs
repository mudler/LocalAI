import test from 'node:test'
import assert from 'node:assert/strict'
import { load, draw } from './load.mjs'

test('page: it says how the notes work and offers to open them', async () => {
  const { mod, sdk } = await load()
  const addon = mod.createAddon(sdk)
  const html = draw(addon, { page: 'memory' })
  assert.match(html, /data-testid="memory-page"/)
  assert.match(html, /Short memory/)
  assert.match(html, /saved only after you confirm it/)
  assert.match(html, /data-testid="memory-open"/)
})

test('page: it draws nothing for a page it does not own', async () => {
  const { mod, sdk } = await load()
  assert.equal(draw(mod.createAddon(sdk), { page: 'other' }), '')
})

test('page: the button opens the Memory tab of the account', async () => {
  const { mod, sdk } = await load()
  const opened = []
  const addon = mod.createAddon({ ...sdk, navigation: { ...sdk.navigation, openApp: (a) => opened.push(a) } })
  const tree = addon.Component({ page: 'memory' })
  const button = JSON.stringify(tree, (k, v) => (typeof v === 'function' ? 'fn' : v))
  assert.match(button, /memory-open/)
  // The handler is the one prop of type function on the button.
  const find = (node) => {
    if (!node || typeof node !== 'object') return null
    if (node.props && node.props['data-testid'] === 'memory-open') return node
    const kids = node.props ? [].concat(node.props.children || []) : []
    for (const k of kids) { const r = find(k); if (r) return r }
    return null
  }
  find(tree).props.onClick()
  assert.deepEqual(opened, [mod.NOTES_ADDRESS || '/app/account?tab=memory'])
})
