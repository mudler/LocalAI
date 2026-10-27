import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { load, here, React, renderToString } from './load.mjs'

const model = async () => (await import('../src/lib/model.js')).settingsModel

const status = (over = {}) => ({
  ok: true, canRun: true, setting: 'auto', active: 'gvisor', hostNotice: false, admin: true, hostConfirmed: false,
  backends: [
    { name: 'kvm', usable: false, reason: '/dev/kvm: permission denied' },
    { name: 'gvisor', usable: true },
    { name: 'host', usable: true },
  ],
  ...over,
})

test('settings: only an administrator gets the settings', async () => {
  const settingsModel = await model()
  assert.equal(settingsModel(status({ admin: false })).hidden, true)
  assert.equal(settingsModel(null).hidden, true)
  assert.equal(settingsModel({ ok: false, error: 'x' }).hidden, true)
  assert.equal(settingsModel(status()).hidden, false)
})

test('settings: the choices are the five backends and the host needs its confirmation', async () => {
  const settingsModel = await model()
  const m = settingsModel(status())
  assert.deepEqual(m.options.map(o => o.value), ['auto', 'gvisor', 'kvm', 'off', 'host'])
  assert.equal(m.needsHostConfirmation, true)
  assert.equal(settingsModel(status({ hostConfirmed: true })).needsHostConfirmation, false)
  assert.equal(settingsModel(status({ setting: 'host', active: 'host', hostConfirmed: true, hostNotice: true })).needsHostConfirmation, false)
})

test('settings: the host notice stays while commands run on the host', async () => {
  const settingsModel = await model()
  assert.equal(settingsModel(status()).hostNotice, false)
  assert.equal(settingsModel(status({ setting: 'host', active: 'host', hostNotice: true, hostConfirmed: true })).hostNotice, true)
})

test('settings: each backend says what is wrong, as text', async () => {
  const settingsModel = await model()
  const m = settingsModel(status())
  const kvm = m.backends.find(b => b.name === 'kvm')
  assert.equal(kvm.usable, false)
  assert.equal(kvm.reason, '/dev/kvm: permission denied')
  assert.equal(m.canRun, true)
  assert.equal(settingsModel(status({ canRun: false, active: '' })).canRun, false)
})

test('settings: the view draws the reasons as text and the warning with the phrase field', async () => {
  const settingsModel = await model()
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk)
  const View = addon.components.SandboxSettingsView
  const m = settingsModel(status({ backends: [{ name: 'kvm', usable: false, reason: '<script>alert(1)</script>' }, { name: 'gvisor', usable: true }, { name: 'host', usable: true }] }))
  const html = renderToString(React.createElement(View, { model: m, draft: 'host', phrase: '', busy: false, error: '', onDraft() {}, onPhrase() {}, onSave() {} }))
  assert.ok(!html.includes('<script>'), html)
  assert.match(html, /data-testid="sandbox-settings"/)
  assert.match(html, /data-testid="sandbox-host-warning"/)
  assert.match(html, /data-testid="sandbox-host-phrase"/)
  const calm = renderToString(React.createElement(View, { model: m, draft: 'gvisor', phrase: '', busy: false, error: '', onDraft() {}, onPhrase() {}, onSave() {} }))
  assert.ok(!calm.includes('sandbox-host-warning'), 'the warning shows only when the host is picked')
})

test('settings: the notice is drawn while the host runs the code', async () => {
  const settingsModel = await model()
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk)
  const View = addon.components.SandboxSettingsView
  const m = settingsModel(status({ setting: 'host', active: 'host', hostNotice: true, hostConfirmed: true }))
  const html = renderToString(React.createElement(View, { model: m, draft: 'host', phrase: '', busy: false, error: '', onDraft() {}, onPhrase() {}, onSave() {} }))
  assert.match(html, /data-testid="sandbox-host-notice"/)
})

test('settings: the slot is declared for the settings page and the component is exported', async () => {
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk)
  assert.equal(typeof addon.components.SandboxSettings, 'function')
  const m = JSON.parse(readFileSync(join(here, '../../manifest.json'), 'utf8'))
  const slot = m.capabilities.ui.slots.find(s => s.slot === 'settings.sections')
  assert.equal(slot.component, 'SandboxSettings')
})
