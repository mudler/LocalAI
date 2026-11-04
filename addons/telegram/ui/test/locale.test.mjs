import test from 'node:test'
import assert from 'node:assert/strict'
import { clearLocales, registerLocales, translator } from '../../../internal/host-compat/src/i18n.js'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

// The shared connector page: its words are JSON bundles in every language of
// the web UI, read through the same translator (namespace "connector").

const connectorDir = join(dirname(fileURLToPath(import.meta.url)), '../../../internal/connector-ui/src/i18n')
const connector = (lng) => JSON.parse(readFileSync(join(connectorDir, `connector.${lng}.json`), 'utf8'))

test('connector words: Italian wins, plural forms follow the language, and a missing key reads in English', () => {
  const it = connector('it')
  const gap = 'ready.title'
  delete it[gap]
  clearLocales()
  registerLocales({ connector: { en: connector('en'), it, 'zh-CN': connector('zh-CN') }, slack: { en: { title: 'Slack' } } })
  const t = translator('it', ['slack', 'connector'])
  assert.equal(t('ready.who.listed', { count: 1 }), '1 persona o chat è consentita.')
  assert.equal(t('ready.who.listed', { count: 3 }), '3 persone o chat sono consentite.')
  assert.equal(t('can.title'), 'Cosa può fare questo bot')
  assert.equal(t(gap), 'Readiness', 'a key the Italian bundle lacks reads in English')
  assert.equal(t('title'), 'Slack', 'the namespace of the add-on is read first')
  const zh = translator('zh-CN', ['connector'])
  assert.ok(zh('ready.who.listed', { count: 1 }).includes('1'), 'a language with one plural form still gets the count')
  assert.equal(translator('sv', ['connector'])('can.title'), 'What this bot can do', 'a language without a bundle reads in English')
})
