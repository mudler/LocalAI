import test from 'node:test'
import assert from 'node:assert/strict'
import { readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { here } from './load.mjs'

const dir = join(here, '../src/i18n')
const bundle = (l) => JSON.parse(readFileSync(join(dir, `calendar.${l}.json`), 'utf8'))

test('i18n: English and Italian ship, and every language has the keys of English', () => {
  const langs = readdirSync(dir).map((f) => /^calendar\.(.+)\.json$/.exec(f)?.[1]).filter(Boolean)
  assert.ok(langs.includes('en') && langs.includes('it'))
  const keys = Object.keys(bundle('en')).sort()
  for (const l of langs) assert.deepEqual(Object.keys(bundle(l)).sort(), keys, l)
})

test('i18n: every text the page asks for exists in English', () => {
  const en = bundle('en')
  const src = readFileSync(join(dir, '../index.jsx'), 'utf8') + readFileSync(join(dir, '../lib/calendar.js'), 'utf8')
  const used = new Set([...src.matchAll(/'(calendar\.[A-Za-z.]+)'/g)].map((m) => m[1]))
  for (const k of used) if (k !== 'calendar.ics') assert.ok(k in en, k)
  for (const f of ['daily', 'weekly', 'monthly', 'yearly']) assert.ok(`calendar.repeat.${f}` in en)
})

test('i18n: Italian is translated, not a copy of English', () => {
  const en = bundle('en'), it = bundle('it')
  const same = Object.keys(en).filter((k) => en[k] === it[k])
  assert.ok(same.length < 8, `too many untranslated: ${same.join(', ')}`)
})
