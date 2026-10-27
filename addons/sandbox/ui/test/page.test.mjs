import test from 'node:test'
import assert from 'node:assert/strict'
import { load, draw, React, renderToString } from './load.mjs'

const pageStatus = async () => (await import('../src/lib/model.js')).pageStatus

const status = (over = {}) => ({ ok: true, canRun: true, setting: 'auto', active: 'gvisor', hostNotice: false, admin: false, ...over })

test('page: the status is isolated, host, cannot-run or unknown, whoever asks', async () => {
  const f = await pageStatus()
  assert.deepEqual(f(status()), { state: 'isolated', backend: 'gvisor', reasons: [] })
  assert.equal(f(status({ active: 'kvm' })).state, 'isolated')
  assert.equal(f(status({ active: 'host', hostNotice: true })).state, 'host')
  assert.equal(f(status({ canRun: false, active: '' })).state, 'cannot')
  assert.equal(f(null).state, 'unknown')
  assert.equal(f({ ok: false, error: 'x' }).state, 'unknown')
})

test('page: the reasons of the backends come with the status of an administrator only', async () => {
  const f = await pageStatus()
  const backends = [{ name: 'kvm', usable: false, reason: 'no /dev/kvm' }, { name: 'gvisor', usable: true }]
  assert.deepEqual(f(status({ canRun: false, active: '', admin: true, backends })).reasons, [{ name: 'kvm', reason: 'no /dev/kvm' }])
  assert.deepEqual(f(status({ canRun: false, active: '', admin: false, backends })).reasons, [])
})

test('page: a user who is not an administrator sees the state and never the isolation claim on the host', async () => {
  const { mod, sdk } = await load({ viewer: { isAdmin: () => false, hasFeature: () => true } })
  const addon = await mod.createAddon(sdk)
  const View = addon.components.SandboxPageStatus
  const f = await pageStatus()
  const show = (s) => renderToString(React.createElement(View, { info: f(s) }))
  const host = show(status({ active: 'host', hostNotice: true }))
  assert.match(host, /data-testid="sandbox-page-host"/)
  assert.match(host, /role="alert"/)
  assert.doesNotMatch(host, /data-testid="sandbox-page-isolated"/)
  const iso = show(status())
  assert.match(iso, /data-testid="sandbox-page-isolated"/)
  assert.match(iso, /gvisor/)
  const cannot = show(status({ canRun: false, active: '' }))
  assert.match(cannot, /data-testid="sandbox-page-cannot"/)
  assert.doesNotMatch(cannot, /data-testid="sandbox-page-isolated"/)
})

test('page: the main page no longer claims isolation before it knows', async () => {
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk)
  const html = draw(addon, { page: 'sandbox', path: '' })
  assert.doesNotMatch(html, /isolated environment/)
  assert.match(html, /data-testid="sandbox-page-loading"/)
})

test('page: the reasons of an administrator are drawn as text', async () => {
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk)
  const View = addon.components.SandboxPageStatus
  const f = await pageStatus()
  const html = renderToString(React.createElement(View, { info: f(status({ canRun: false, active: '', admin: true, backends: [{ name: 'gvisor', usable: false, reason: '<b>x</b>' }] })) }))
  assert.ok(!html.includes('<b>x</b>'))
  assert.match(html, /data-testid="sandbox-page-reason"/)
})
