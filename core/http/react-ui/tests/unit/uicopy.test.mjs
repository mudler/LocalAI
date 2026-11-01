// The copy of the UI speaks about what a person can do and see, not about the
// files, formats, routes and setting names behind it. This test reads every
// string of every locale, the English labels and descriptions of the Settings
// page (src/utils/settingsSchema.js) and the English text the add-ons carry,
// and fails when one names an implementation detail,
// unless its key is in ui-copy-allowlist.json with the reason it may (an
// operator page that names a setting the docs name, an export format the
// person picks). The allow-list only shrinks: an entry whose string no longer
// leaks, or whose key is gone, fails too, so it cannot hide a later leak.
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { FIELDS, GROUPS } from '../../src/utils/settingsSchema.js'

const root = join(dirname(fileURLToPath(import.meta.url)), '..', '..')
const ADDONS = join(root, '..', '..', '..', 'addons')
const LOCALES = join(root, 'public/locales')
const allow = JSON.parse(readFileSync(join(dirname(fileURLToPath(import.meta.url)), 'ui-copy-allowlist.json'), 'utf8'))

// What a person should not have to know. Each pattern names what it catches;
// the plain words (schema, endpoint) are checked in English only, as other
// languages use them for other things (a URL scheme in German, a diagram in
// Italian).
const DENY = [
  [/\.(json|jsonl|ya?ml)\b/i, 'a settings or data file name'],
  [/\bLocalAI\//, 'a path in the LocalAI settings folder'],
  [/\/api\//, 'an API route'],
  [/\b(GET|POST|PUT|PATCH|DELETE) \//, 'an HTTP method and route'],
  [/\bchats_[a-z_]+/, 'a setting key'],
  [/well-?known/i, 'an internal name'],
  [/\bJSON\b/, 'a data format'],
  [/\bYAML\b/, 'a data format'],
  [/\bschema\b/i, 'a data format word', 'en'],
  [/\bSKILL\.md\b/, 'a file name'],
  [/\bLOCALAI_[A-Z_]+/, 'an environment variable'],
  [/\bendpoint\b/i, 'an API word', 'en'],
  [/<code>[^<]*[/.][^<]*<\/code>/, 'a path shown as code'],
]

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
    const ns = f.slice(0, -5)
    for (const [k, v] of leaves(JSON.parse(readFileSync(join(LOCALES, locale, f), 'utf8')), '', [])) out.push([`${ns}:${k}`, v])
  }
  return out
}

const leak = (text, locale = 'en') => DENY.find(([re, , only]) => (!only || only === locale) && re.test(text))

// The English text of the Settings page, which is in its schema, not in the
// locale files: the label, description and note of each field and the label
// and hint of each group, as settings:<key>.<what>.
function settingsStrings() {
  const out = []
  for (const g of GROUPS) for (const what of ['label', 'hint']) if (typeof g[what] === 'string') out.push([`settings:group.${g.id}.${what}`, g[what]])
  for (const f of FIELDS) for (const what of ['label', 'desc', 'note']) if (typeof f[what] === 'string') out.push([`settings:${f.key}.${what}`, f[what]])
  return out
}

// The English text of the add-ons that carry their own (an en.js module or
// an English JSON bundle), as addon:<add-on>:<key>.
const ADDON_BUNDLES = [
  ['face', 'ui/src/i18n/en.js', 'default'],
  ['fine-tuning', 'ui/src/i18n/en.js', 'default'],
  ['quantization', 'ui/src/i18n/en.js', 'default'],
  ['tables', 'ui/src/i18n/en.js', 'EN'],
  ['voice', 'ui/src/i18n/en.js', 'default'],
  ['slack', 'ui/src/i18n/slack.en.json'],
  ['talk', 'ui/src/i18n/talk.en.json'],
  ['telegram', 'ui/src/i18n/telegram.en.json'],
  ['studio', 'ui/src/i18n/media.en.json'],
]

async function addonStrings() {
  const out = []
  for (const [id, file, name] of ADDON_BUNDLES) {
    const path = join(ADDONS, id, file)
    const bundle = name ? (await import(pathToFileURL(path).href))[name] : JSON.parse(readFileSync(path, 'utf8'))
    for (const [k, v] of leaves(bundle, '', [])) out.push([`addon:${id}:${k}`, v])
  }
  return out
}

function findLeaks(list) {
  const found = []
  for (const [key, text] of list) {
    if (allow[key]) continue
    const hit = leak(text)
    if (hit) found.push(`${key}: ${hit[1]}: ${JSON.stringify(text)}`)
  }
  return found
}

test('ui copy: no string of any locale names files, formats, routes or setting keys outside the allow-list', () => {
  const found = []
  for (const locale of readdirSync(LOCALES).sort()) {
    for (const [key, text] of strings(locale)) {
      if (allow[key]) continue
      const hit = leak(text, locale)
      if (hit) found.push(`${locale} ${key}: ${hit[1]}: ${JSON.stringify(text)}`)
    }
  }
  assert.deepEqual(found, [], 'say what the person can do or see instead, or add the key to ui-copy-allowlist.json with the reason an operator needs it')
})

test('ui copy: the labels and descriptions of the Settings page name no file, format, route or setting key outside the allow-list', () => {
  assert.deepEqual(findLeaks(settingsStrings()), [], 'say it in plain words in src/utils/settingsSchema.js, or add the key to ui-copy-allowlist.json with the reason an operator needs it')
})

test('ui copy: the English text of the add-ons names no file, format, route or setting key outside the allow-list', async () => {
  assert.deepEqual(findLeaks(await addonStrings()), [], 'say it in plain words in the add-on bundle, or add the key to ui-copy-allowlist.json with the reason')
})

test('ui copy: every allow-list entry has a reason and still needs it in English', async () => {
  const en = new Map([...strings('en'), ...settingsStrings(), ...(await addonStrings())])
  const stale = []
  for (const [key, reason] of Object.entries(allow)) {
    assert.ok(typeof reason === 'string' && reason.trim().length > 0, `${key} has no reason`)
    if (!en.has(key)) stale.push(`${key}: no such key`)
    else if (!leak(en.get(key))) stale.push(`${key}: no longer names an implementation detail`)
  }
  assert.deepEqual(stale, [], 'remove these entries from ui-copy-allowlist.json')
})

test('ui copy: the deny-list catches the leaks it was written for', () => {
  for (const s of [
    'Defined in <code>LocalAI/shortcuts.json</code>', 'Saved LocalAI/home.json.', 'POST /api/agent/tasks/x/execute',
    'Start with LOCALAI_ENABLE_TRACING=true', 'The raw file is checked against the schema', 'A folder with a SKILL.md',
    'Set chats_egress_allow_private', 'Edit the model YAML', 'Created the page in <code>pages/tasks/</code>',
  ]) assert.ok(leak(s), s)
  for (const s of ['Saved your shortcuts.', 'Created the page Tasks.', 'Some values are not valid. Check them and save again.']) assert.equal(leak(s), undefined, s)
})
