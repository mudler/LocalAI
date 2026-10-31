import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  SETTINGS_VIEW, addonSettingsAddress, legacySectionTarget, paneView, serverSettingsAddress, serverSettingsFor,
} from '../../src/utils/addonSettings.js'
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

test('add-on settings: the server settings of an add-on\'s feature link to their block on the Settings page', () => {
  const sandbox = serverSettingsFor('sandbox')
  assert.deepEqual(sandbox.map(e => e.id), ['network', 'bash'])
  assert.equal(serverSettingsAddress(sandbox[0]), '/app/settings?group=tools#settings-egress')
  assert.equal(serverSettingsAddress(serverSettingsFor('web')[0]), '/app/settings?group=tools#settings-builtin-tools')
  assert.deepEqual(serverSettingsFor('toString'), [])
  assert.deepEqual(serverSettingsFor('nope'), [])
  // The anchors exist on the blocks they name.
  const egress = readFileSync(join(src, 'components/settings/EgressSection.jsx'), 'utf8')
  const tools = readFileSync(join(src, 'components/settings/BuiltinToolsSection.jsx'), 'utf8')
  assert.match(egress, /id="settings-egress"/)
  assert.match(tools, /id="settings-builtin-tools"/)
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
