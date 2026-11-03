import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { SETTINGS_VIEW, addonSettingsAddress, legacySectionTarget, paneView } from '../../src/utils/addonSettings.js'
import {
  HOST_CONTROL_IDS, NETWORK_POLICY, NETWORK_POLICY_FIELDS, TOOL_PROVIDERS, appsWithControl, controlsOf, hasAddonSettings, hasControl, ownedByControl,
} from '../../src/addons/hostControls.js'
import { FIELD_BY_KEY, getValue, isChanged } from '../../src/utils/settingsSchema.js'
import { THEME_TOKENS } from '../../src/addons/sdk.js'
import { KIT } from '../../../../../addons/internal/uibuild/kitstub.mjs'

const src = join(dirname(fileURLToPath(import.meta.url)), '../../src')

test('add-on settings: the Settings tab of the details is the one address of an add-on\'s settings', () => {
  assert.equal(addonSettingsAddress('sandbox'), '/app/addons?detail=sandbox&view=settings')
  assert.equal(addonSettingsAddress('a b'), '/app/addons?detail=a%20b&view=settings')
  assert.equal(paneView(new URLSearchParams('detail=web&view=settings'), true), SETTINGS_VIEW)
  // No Settings tab to show: the overview, whatever the address says.
  assert.equal(paneView(new URLSearchParams('detail=web&view=settings'), false), 'overview')
  assert.equal(paneView(new URLSearchParams('detail=web'), true), 'overview')
})

test('add-on settings: the old anchor of a section at the end of the Settings page finds its add-on', () => {
  assert.equal(legacySectionTarget('#ad-section-sandbox-settings', ['sandbox', 'web']), 'sandbox')
  assert.equal(legacySectionTarget('#ad-section-web-settings', ['sandbox', 'web']), 'web')
  // The longest listed id wins, so a dash in an add-on id is kept.
  assert.equal(legacySectionTarget('#ad-section-web-tools-settings', ['web', 'web-tools']), 'web-tools')
  // Not listed yet: the id up to the last dash.
  assert.equal(legacySectionTarget('#ad-section-voice-settings', []), 'voice')
  assert.equal(legacySectionTarget('#settings-egress', ['sandbox']), '')
  assert.equal(legacySectionTarget('', ['sandbox']), '')
  assert.equal(legacySectionTarget('#ad-section-<x>', ['sandbox']), '')
})

test('add-on settings: the host draws the controls the apps list gives an add-on, whatever its ID', () => {
  const ui = (controls, extra = {}) => ({ entry: '/x.js', sdk: { min: 2, max: 2 }, controls, ...extra })
  const sandbox = { id: 'sandbox', enabled: true, state: 'running', ui: ui([NETWORK_POLICY, TOOL_PROVIDERS]) }
  const devbox = { id: 'devbox', enabled: true, state: 'running', ui: ui([NETWORK_POLICY, 'from-a-newer-server']) }
  const plain = { id: 'notes', enabled: true, state: 'running', ui: ui(undefined) }
  assert.deepEqual(HOST_CONTROL_IDS, ['network-policy', 'tool-providers'])
  assert.deepEqual(controlsOf(sandbox), ['network-policy', 'tool-providers'])
  // An ID this page does not know is not drawn.
  assert.deepEqual(controlsOf(devbox), ['network-policy'])
  assert.deepEqual(controlsOf(plain), [])
  assert.deepEqual(controlsOf(null), [])
  assert.equal(hasControl(devbox, NETWORK_POLICY), true)
  assert.equal(hasControl(devbox, TOOL_PROVIDERS), false)
  // A control gives the add-on a Settings tab, as its own section does.
  assert.equal(hasAddonSettings(devbox), true)
  assert.equal(hasAddonSettings(plain), false)
  assert.equal(hasAddonSettings({ ...plain, ui: ui(undefined, { slots: [{ id: 's', slot: 'settings.sections', component: 'S' }] }) }), true)
  // Only usable add-ons point the Settings page and the chat at their page.
  assert.deepEqual(appsWithControl([sandbox, devbox, { ...devbox, id: 'off', enabled: false }, plain], NETWORK_POLICY).map(a => a.id), ['sandbox', 'devbox'])
})

test('add-on settings: the network settings are drawn by their control, and the audit retention stays on the Settings page too', () => {
  for (const key of NETWORK_POLICY_FIELDS) assert.ok(FIELD_BY_KEY[key], key)
  for (const key of ['chats_egress_ceiling', 'chats_egress_default', 'chats_egress_ports', 'chats_egress_extra_registries', 'chats_egress_allowed_hosts', 'chats_egress_allow_private']) {
    assert.equal(ownedByControl(key), true, key)
  }
  assert.equal(ownedByControl('chats_egress_audit_days'), false)
  assert.ok(NETWORK_POLICY_FIELDS.includes('chats_egress_audit_days'))
  assert.equal(ownedByControl('chats_host_exec'), false)
  assert.equal(ownedByControl('chats_web_search'), false)
  // No host file names an add-on to place its settings.
  for (const f of ['utils/addonSettings.js', 'components/addons/AddonSettings.jsx', 'utils/chatEgress.js', 'components/chat/NetworkConsentCard.jsx', 'pages/Settings.jsx']) {
    const text = readFileSync(join(src, f), 'utf8')
    assert.doesNotMatch(text, /'sandbox'|"sandbox"|'web'|SERVER_SETTINGS|SANDBOX_NETWORK_SETTINGS/, f)
  }
})

test('add-on settings: a setting never set (0 or empty) reads as its default and is not changed, and the audit retention has its unit and bounds', () => {
  const f = FIELD_BY_KEY.chats_egress_audit_days
  assert.equal(getValue(f, { chats_egress_audit_days: 0 }), 30)
  assert.equal(getValue(f, {}), 30)
  assert.equal(getValue(f, { chats_egress_audit_days: 7 }), 7)
  assert.equal(isChanged(f, { chats_egress_audit_days: 0 }), false)
  assert.equal(isChanged(f, { chats_egress_audit_days: 7 }), true)
  assert.equal(f.unit, 'days')
  assert.equal(f.min, 1)
  assert.equal(f.max, 365)
  assert.ok(f.label && f.desc, 'a label and a help line')
  // The same for the other settings the server reads as their default when
  // empty: a select shows the default, a text field stays empty.
  const ceiling = FIELD_BY_KEY.chats_egress_ceiling
  assert.equal(getValue(ceiling, { chats_egress_ceiling: '' }), 'off')
  assert.equal(isChanged(ceiling, { chats_egress_ceiling: '' }), false)
  assert.equal(isChanged(ceiling, { chats_egress_ceiling: 'all' }), true)
  for (const key of ['chats_egress_ports', 'chats_web_ports']) {
    assert.equal(getValue(FIELD_BY_KEY[key], { [key]: '' }), '')
    assert.equal(isChanged(FIELD_BY_KEY[key], { [key]: '' }), false, key)
    assert.equal(isChanged(FIELD_BY_KEY[key], { [key]: '8080' }), true, key)
  }
})

test('add-on settings: the SDK settings kit is the host\'s, registered for every add-on, and its tokens exist', () => {
  const components = readFileSync(join(src, 'addons/hostComponents.jsx'), 'utf8')
  const services = readFileSync(join(src, 'addons/hostServices.js'), 'utf8')
  const shell = readFileSync(join(src, 'addons/shell.css'), 'utf8')
  for (const name of KIT) {
    assert.match(components, new RegExp(`export function ${name}\\(`), name)
    assert.match(services, new RegExp(`\\b${name}\\b`), name)
  }
  for (const cls of ['ad-kit-section', 'ad-kit-field', 'ad-kit-choice__option', 'ad-kit-row', 'ad-kit-code', 'ad-slot--block']) {
    assert.ok(shell.includes(`.${cls}`), cls)
  }
  // Every token an add-on gets is a custom property the theme defines.
  const css = ['theme.css', 'vendor/ui-kit/tokens.css', 'vendor/ui-kit/components.css', 'vendor/ui-kit/themes/sample-ink.css']
    .map(f => { try { return readFileSync(join(src, f), 'utf8') } catch { return '' } }).join('\n')
  for (const [name, value] of Object.entries(THEME_TOKENS)) {
    const prop = /var\((--[a-z0-9-]+)/.exec(value)[1]
    assert.ok(css.includes(`${prop}:`), `${name}: ${prop} is defined`)
  }
})

test('settings rows: no field is labelled with its raw snake_case key', async () => {
  // SettingField.jsx imports JSX, which node cannot load; its label rule is read from the source and run here.
  const text = readFileSync(join(src, 'components/settings/SettingField.jsx'), 'utf8')
  const rule = text.match(/const RAW_KEY = (\/.*\/)\n/)[1]
  const raw = new RegExp(rule.slice(1, -1))
  for (const key of NETWORK_POLICY_FIELDS) assert.ok(FIELD_BY_KEY[key], `${key} has a field`)
  for (const field of Object.values(FIELD_BY_KEY)) {
    assert.ok(field.label, `${field.key} has a label`)
    assert.ok(!raw.test(field.label), `${field.key} is labelled with a raw key: ${field.label}`)
  }
  assert.match(text, /export function labelOf/)
})
