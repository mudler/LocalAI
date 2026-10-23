import test from 'node:test'
import assert from 'node:assert/strict'
import { EN } from '../src/i18n/en.js'
import { BUNDLES } from '../src/i18n/index.js'

const placeholders = (s) => [...String(s).matchAll(/\{\{\s*([A-Za-z0-9_]+)\s*\}\}/g)].map(m => m[1]).sort()

test('i18n: every language of the host has every key of the English bundle and nothing else', () => {
  assert.deepEqual(Object.keys(BUNDLES).sort(), ['de', 'en', 'es', 'id', 'it', 'ko', 'pt-BR', 'zh-CN'])
  const keys = Object.keys(EN).sort()
  for (const [lang, bundle] of Object.entries(BUNDLES)) {
    assert.deepEqual(Object.keys(bundle).sort(), keys, lang)
  }
})

test('i18n: a translation keeps the placeholders of the English text and carries no markup', () => {
  for (const [lang, bundle] of Object.entries(BUNDLES)) {
    for (const [key, text] of Object.entries(bundle)) {
      assert.equal(typeof text, 'string', `${lang} ${key}`)
      assert.notEqual(text.trim(), '', `${lang} ${key}`)
      assert.deepEqual(placeholders(text), placeholders(EN[key]), `${lang} ${key}`)
      assert.doesNotMatch(text, /[<>]/, `${lang} ${key}`)
    }
  }
})
