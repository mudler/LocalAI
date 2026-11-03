// Core names no add-on that provides a generic role. The words a person reads
// about running commands, a messaging platform or a document tool come from the
// provider's declaration (its title) or are generic. This test reads the add-ons
// under addons/ at run time and fails when a string of any locale, or a label of
// the Settings page, names one of them, unless the key is in
// provider-strings-allowlist.json with the reason it may.
//
// An add-on provides a generic role when its manifest shadows a built-in tool or
// skill, serves as a connector, or declares the execution provider. The other
// add-ons (voice, face, tables, studio, talk and the two engine add-ons) are
// not checked: their names are the plain words of the model features that core
// owns. The allow-list only shrinks: an entry that no longer matches fails.
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { FIELDS, GROUPS } from '../../src/utils/settingsSchema.js'

const here = dirname(fileURLToPath(import.meta.url))
const root = join(here, '..', '..')
const ADDONS = join(root, '..', '..', '..', 'addons')
const LOCALES = join(root, 'public/locales')
const allow = JSON.parse(readFileSync(join(here, 'provider-strings-allowlist.json'), 'utf8'))

const esc = s => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')

function providerNames() {
  const names = new Set()
  for (const d of readdirSync(ADDONS, { withFileTypes: true })) {
    if (!d.isDirectory()) continue
    let m
    try { m = JSON.parse(readFileSync(join(ADDONS, d.name, 'manifest.json'), 'utf8')) } catch { continue }
    const c = m.capabilities || {}
    const role = (c.toolDefinitions || []).some(x => x.shadows) || (c.skillDefinitions || []).some(x => x.shadows) || Boolean(c.connector) || Boolean(c.executionProvider)
    if (!role || !m.id) continue
    // The id in capitals matches the title in a sentence; the lower-case id only
    // when it cannot be an ordinary word (long or hyphenated).
    names.add(m.id[0].toUpperCase() + m.id.slice(1))
    if (m.id.length >= 7 || m.id.includes('-')) names.add(m.id)
    const t = c.ui?.title
    for (const v of typeof t === 'string' ? [t] : Object.values(t || {})) if (typeof v === 'string' && v) names.add(v)
  }
  return [...names]
}

function matcher(names) {
  const latin = names.filter(n => /^[\x20-\x7e]+$/.test(n))
  const other = names.filter(n => !/^[\x20-\x7e]+$/.test(n))
  const re = latin.length ? new RegExp(`(?<![\\w-])(${latin.map(esc).join('|')})(?![\\w-])`) : null
  return text => re?.exec(text)?.[1] || other.find(n => text.includes(n)) || ''
}

function leaves(value, prefix, out) {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    for (const [k, v] of Object.entries(value)) leaves(v, prefix ? `${prefix}.${k}` : k, out)
  } else if (typeof value === 'string') {
    out.push([prefix, value])
  }
  return out
}

function strings(locale) {
  const out = []
  for (const f of readdirSync(join(LOCALES, locale)).filter(n => n.endsWith('.json')).sort()) {
    for (const [k, v] of leaves(JSON.parse(readFileSync(join(LOCALES, locale, f), 'utf8')), '', [])) out.push([`${f.slice(0, -5)}:${k}`, v])
  }
  return out
}

function settingsStrings() {
  const out = []
  for (const g of GROUPS) for (const what of ['label', 'hint']) if (typeof g[what] === 'string') out.push([`settings:group.${g.id}.${what}`, g[what]])
  for (const f of FIELDS) for (const what of ['label', 'desc', 'note', 'placeholder']) if (typeof f[what] === 'string') out.push([`settings:${f.key}.${what}`, f[what]])
  return out
}

test('provider strings: the shipped add-ons that provide a role are found', () => {
  const names = providerNames()
  assert.ok(names.length >= 4, `only ${JSON.stringify(names)}`)
  for (const n of names) assert.ok(typeof n === 'string' && n.length > 1)
})

test('provider strings: no locale string or Settings label names a provider add-on outside the allow-list', () => {
  const find = matcher(providerNames())
  const found = []
  for (const locale of readdirSync(LOCALES).sort()) {
    for (const [key, text] of strings(locale)) {
      if (allow[key]) continue
      const hit = find(text)
      if (hit) found.push(`${locale} ${key}: ${hit}: ${JSON.stringify(text)}`)
    }
  }
  for (const [key, text] of settingsStrings()) {
    if (allow[key]) continue
    const hit = find(text)
    if (hit) found.push(`${key}: ${hit}: ${JSON.stringify(text)}`)
  }
  assert.deepEqual(found, [], 'build the text from the provider title ({{name}} from the add-on list) or word it for any provider, or add the key to provider-strings-allowlist.json with the reason')
})

test('provider strings: every allow-list entry has a reason and still matches', () => {
  const find = matcher(providerNames())
  const hits = new Set()
  for (const locale of readdirSync(LOCALES)) for (const [key, text] of strings(locale)) if (find(text)) hits.add(key)
  for (const [key, text] of settingsStrings()) if (find(text)) hits.add(key)
  const stale = []
  for (const [key, reason] of Object.entries(allow)) {
    assert.ok(typeof reason === 'string' && reason.trim().length > 0, `${key} has no reason`)
    if (!hits.has(key)) stale.push(key)
  }
  assert.deepEqual(stale, [], 'remove these entries from provider-strings-allowlist.json')
})

test('provider strings: the matcher catches a title, a capitalised id and a translated title', () => {
  const find = matcher(['Sandbox', 'Slack', 'Simple Office', 'fine-tuning', '数据表'])
  assert.equal(find('Install the Sandbox to isolate them'), 'Sandbox')
  assert.equal(find('Open Simple Office'), 'Simple Office')
  assert.equal(find('Bots for Slack'), 'Slack')
  assert.equal(find('打开数据表'), '数据表')
  assert.equal(find('commands run in isolation'), '')
  assert.equal(find('Sandboxed frame'), '')
})
