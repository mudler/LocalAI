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
  assert.equal(t('ready.who.listed', { count: 1 }), '1 persona o chat è in elenco.')
  assert.equal(t('ready.who.listed', { count: 3 }), '3 persone o chat sono in elenco.')
  assert.equal(t('matrix.title'), 'Chi può fare cosa')
  assert.equal(t(gap), 'Status', 'a key the Italian bundle lacks reads in English')
  assert.equal(t('title'), 'Slack', 'the namespace of the add-on is read first')
  const zh = translator('zh-CN', ['connector'])
  assert.ok(zh('ready.who.listed', { count: 1 }).includes('1'), 'a language with one plural form still gets the count')
  assert.equal(translator('sv', ['connector'])('matrix.title'), 'Who can do what', 'a language without a bundle reads in English')
})

const LANGS = ['en', 'it', 'es', 'de', 'fr', 'zh-CN', 'id', 'ko', 'pt-BR']

test('connector words: the quiet line of a new connection counts the things to set, in every language', () => {
  for (const lng of LANGS) {
    const b = connector(lng)
    for (const key of ['ready.quiet_one', 'ready.quiet_other', 'ready.quietDone']) assert.ok(typeof b[key] === 'string' && b[key].trim() !== '', `${lng} has ${key}`)
    assert.ok(b['ready.quiet_other'].includes('{{count}}'), `${lng} keeps the count`)
  }
})

test('connector words: Answer only does not claim that the bot does nothing else, and the note says where the agent side is set', () => {
  const en = connector('en')
  // The facts: a connection with no permission ticked still lets a verified sender use the
  // write tools of the add-on connections the agent holds. The words must not say otherwise.
  assert.doesNotMatch(en['conn.preset.answerHelp'], /nothing else|only/i)
  assert.match(en['conn.preset.answerHelp'], /no permission of its own/)
  assert.match(en['conn.preset.note'], /add-on connections/)
  assert.match(en['conn.preset.note'], /verified people can still use their write tools/)
  assert.match(en['conn.preset.note'], /"What it can use"/, 'the note names the place where the agent side is changed')
  for (const lng of LANGS) {
    const b = connector(lng)
    for (const key of ['conn.preset.answerHelp', 'conn.preset.note']) assert.ok(typeof b[key] === 'string' && b[key].trim() !== '', `${lng} has ${key}`)
    if (lng !== 'en') assert.notEqual(b['conn.preset.answerHelp'], en['conn.preset.answerHelp'], `${lng} translates the Answer only help`)
  }
})

test('connector words: a failed test in the sheet says the connection is saved, gives the reason and offers Test again, in every language', () => {
  const en = connector('en')
  assert.match(en['conn.sheet.testFailed'], /is saved/)
  assert.equal(en['conn.sheet.testAgain'], 'Test again')
  for (const lng of LANGS) {
    const b = connector(lng)
    for (const key of ['conn.sheet.testFailed', 'conn.sheet.testFailedNoReason', 'conn.sheet.testAgain']) assert.ok(typeof b[key] === 'string' && b[key].trim() !== '', `${lng} has ${key}`)
    if (lng !== 'en') assert.notEqual(b['conn.sheet.testAgain'], en['conn.sheet.testAgain'], `${lng} translates Test again`)
  }
})
