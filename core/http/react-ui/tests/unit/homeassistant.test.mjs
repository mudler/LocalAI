import test from 'node:test'
import assert from 'node:assert/strict'
import { readdirSync, readFileSync } from 'node:fs'
import { availableActions, filterActions } from '../../src/components/home/homeActions.js'

const base = { isAdmin: true, hasModels: true, loadedCount: 0, studioInstalled: false, assistantAvailable: true }
const ids = (ctx) => availableActions(ctx).map(a => a.id)

test('home assistant: the /assistant action is offered to an admin when the server has the assistant', () => {
  assert.ok(ids(base).includes('assistant'))
})

test('home assistant: the /assistant action is hidden from a non-admin and when the setting is off', () => {
  assert.ok(!ids({ ...base, isAdmin: false }).includes('assistant'))
  assert.ok(!ids({ ...base, assistantAvailable: false }).includes('assistant'))
  assert.ok(!ids({ ...base, assistantAvailable: undefined }).includes('assistant'))
})

test('home assistant: the /assistant action can be found by typing its first letters', () => {
  const found = filterActions(availableActions(base), 'ass', a => a.id)
  assert.deepEqual(found.map(a => a.id), ['assistant'])
})

test('home assistant: every language has the keys the Home entry uses', () => {
  const dir = new URL('../../public/locales/', import.meta.url)
  const keys = ['assistant.title', 'assistant.description', 'assistant.open', 'assistant.noModel', 'assistant.startFailed', 'assistant.chatTitle']
  for (const lang of readdirSync(dir)) {
    const home = JSON.parse(readFileSync(new URL(`${lang}/home.json`, dir), 'utf8'))
    for (const k of keys) {
      const v = k.split('.').reduce((o, p) => (o == null ? o : o[p]), home)
      assert.equal(typeof v, 'string', `${lang}: ${k}`)
      assert.ok(v.length > 0, `${lang}: ${k}`)
    }
  }
  // The slash menu strings exist in English only today; the others fall back to it.
  const en = JSON.parse(readFileSync(new URL('en/home.json', dir), 'utf8'))
  assert.ok(en.slash.assistant.label && en.slash.assistant.desc)
})
