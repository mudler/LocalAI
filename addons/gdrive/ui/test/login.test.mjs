import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { loginPlan } from '../../../internal/connector-ui/src/login.js'

const here = new URL('../src/', import.meta.url)
const read = (f) => readFileSync(new URL(f, here), 'utf8')
const host = { ui: { ConnectionLogin: () => null } }
const kind = { addon: 'gdrive', kind: 'gdrive', title: 'gdrive' }

test('login: the page creates connections itself; it no longer says they are added elsewhere', () => {
  const src = read('addon.jsx')
  assert.match(src, /messaging=\{false\} login\b/)
  assert.doesNotMatch(src, /canAdd=\{false\}/)
  const en = JSON.parse(read('i18n/gdrive.en.json'))
  assert.equal(en.connect, 'Connect with Google')
  assert.ok(en.defaultName)
  for (const k of ['limits', 'empty']) assert.doesNotMatch(en[k], /Connectors page/, k)
  assert.match(en.empty, /Connect with Google/)
})

test('login: every language has the Connect button, the default name and the new first lines', () => {
  for (const l of ['de', 'es', 'fr', 'id', 'it', 'ko', 'pt-BR', 'zh-CN']) {
    const d = JSON.parse(read(`i18n/gdrive.${l}.json`))
    assert.ok(d.connect && d.defaultName, l)
    assert.ok(d.empty.includes(d.connect), `${l}: the empty text names the button`)
  }
})

test('login: the host login shows once the kind is known, with no token sheet or New button', () => {
  assert.deepEqual(loginPlan({ login: true, canAdd: true, sdk: host, kindInfo: kind, kindsReady: true }), { show: 'host', sheetAdd: false })
  assert.deepEqual(loginPlan({ login: true, canAdd: true, sdk: host, kindInfo: null, kindsReady: false }), { show: 'waiting', sheetAdd: false })
})

test('login: a host without the login says so instead of showing a dead page', () => {
  assert.deepEqual(loginPlan({ login: true, canAdd: true, sdk: { ui: {} }, kindInfo: kind, kindsReady: true }), { show: 'oldHost', sheetAdd: false })
  assert.deepEqual(loginPlan({ login: true, canAdd: true, sdk: null, kindInfo: null, kindsReady: false }), { show: 'waiting', sheetAdd: false })
})

test('login: a token kind keeps its New sheet, and a page that cannot add draws no login', () => {
  assert.deepEqual(loginPlan({ login: false, canAdd: true, sdk: host, kindInfo: kind, kindsReady: true }), { show: 'none', sheetAdd: true })
  assert.deepEqual(loginPlan({ login: true, canAdd: false, sdk: host, kindInfo: kind, kindsReady: true }), { show: 'none', sheetAdd: false })
})
