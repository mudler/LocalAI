// The copy of the UI speaks about what a person can do and see, not about the
// files, formats, routes and setting names behind it. This test reads every
// string of every locale and fails when one names an implementation detail,
// unless its key is in ui-copy-allowlist.json with the reason it may (an
// operator page that names a setting the docs name, an export format the
// person picks). The allow-list only shrinks: an entry whose string no longer
// leaks, or whose key is gone, fails too, so it cannot hide a later leak.
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = join(dirname(fileURLToPath(import.meta.url)), '..', '..')
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

test('ui copy: every allow-list entry has a reason and still needs it in English', () => {
  const en = new Map(strings('en'))
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
