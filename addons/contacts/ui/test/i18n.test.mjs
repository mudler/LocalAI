import test from 'node:test'
import assert from 'node:assert/strict'
import { readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { here } from './load.mjs'

const dir = join(here, '../src/i18n')
const bundle = (l) => JSON.parse(readFileSync(join(dir, `contacts.${l}.json`), 'utf8'))

test('i18n: English and Italian ship, and every language has the keys of English', () => {
  const langs = readdirSync(dir).map((f) => /^contacts\.(.+)\.json$/.exec(f)?.[1]).filter(Boolean)
  assert.ok(langs.includes('en') && langs.includes('it'))
  const keys = Object.keys(bundle('en')).sort()
  for (const l of langs) assert.deepEqual(Object.keys(bundle(l)).sort(), keys, l)
})

test('i18n: every text the page asks for exists in English', () => {
  const en = bundle('en')
  const src = readFileSync(join(dir, '../index.jsx'), 'utf8') + readFileSync(join(dir, '../lib/contacts.js'), 'utf8')
  const used = new Set([...src.matchAll(/'(contacts\.[A-Za-z.]+)'/g)].map((m) => m[1]))
  for (const k of used) if (k !== 'contacts.vcf') assert.ok(k in en, k)
  // Keys built from a name: contacts.form.<phones|emails|phone|email>, contacts.detail.<key>.
  for (const k of ['phones', 'emails', 'phone', 'email']) assert.ok(`contacts.form.${k}` in en, k)
  for (const k of ['organisation', 'phones', 'emails', 'birthday', 'address', 'notes']) assert.ok(`contacts.detail.${k}` in en, k)
})

test('i18n: every key of English is used by the page', () => {
  const en = bundle('en')
  const src = readFileSync(join(dir, '../index.jsx'), 'utf8') + readFileSync(join(dir, '../lib/contacts.js'), 'utf8')
  for (const k of Object.keys(en)) {
    const short = k.replace(/^contacts\./, '')
    const dynamic = /^(form\.(phones|emails|phone|email)|detail\.(organisation|phones|emails|birthday|address|notes))$/.test(short)
    assert.ok(dynamic || src.includes(`'${k}'`), `${k} is unused`)
  }
})

test('i18n: Italian is translated, not a copy of English', () => {
  const en = bundle('en'), it = bundle('it')
  const same = Object.keys(en).filter((k) => en[k] === it[k])
  assert.ok(same.length < 8, `too many untranslated: ${same.join(', ')}`)
})
