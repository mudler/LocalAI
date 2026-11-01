import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { failureView, isStaleFailure, STALE_FAILURE_MS } from '../../src/utils/chatFailure.js'

const locales = new URL('../../public/locales/', import.meta.url)
const load = (lng) => JSON.parse(readFileSync(new URL(`${lng}/chat.json`, locales), 'utf8'))

// A t() that reads the locale file the way i18next does: a dotted path and
// {{name}} placeholders.
const translator = (lng) => {
  const catalog = load(lng)
  return (key, vars = {}) => {
    const text = key.split('.').reduce((o, k) => o?.[k], catalog)
    if (typeof text !== 'string') throw new Error(`missing ${lng} key ${key}`)
    return text.replace(/\{\{(\w+)\}\}/g, (_, n) => String(vars[n]))
  }
}

const en = translator('en')

test('chat failure: a model whose backend is missing says so, names the model and points an admin to Backends', () => {
  const failure = { code: 'model_backend_missing', model: 'test', retry: 8, detail: 'the model could not be loaded', text: 'x' }
  const admin = failureView(en, failure, { isAdmin: true })
  assert.equal(admin.reason, 'The model "test" could not be started: its backend is not installed.')
  assert.equal(admin.backendsLink, true)
  assert.match(admin.hint, /Backends page/)
  const user = failureView(en, failure, { isAdmin: false })
  assert.equal(user.backendsLink, false)
  assert.match(user.hint, /administrator/)
})

test('chat failure: a model in cooldown says when to retry', () => {
  const view = failureView(en, { code: 'model_cooldown', model: 'test', retry: 8 })
  assert.match(view.reason, /"test"/)
  assert.equal(view.hint, 'Try again in 8 seconds.')
})

test('chat failure: a model the server did not name gets the wording without a name', () => {
  assert.equal(failureView(en, { code: 'model_not_found', model: '' }).reason, 'The model was not found.')
})

test('chat failure: the upstream causes tell what is wrong in plain words', () => {
  assert.match(failureView(en, { code: 'upstream_key' }).reason, /refused the key/)
  assert.match(failureView(en, { code: 'upstream_path' }).hint, /full endpoint.*\/v1\/chat\/completions/)
  assert.match(failureView(en, { code: 'upstream_unreachable' }).hint, /address/)
  for (const code of ['upstream_key', 'upstream_path', 'upstream_unreachable', 'upstream']) {
    assert.doesNotMatch(failureView(en, { code }).hint, /backend/i, `${code} must not blame the backend`)
  }
})

test('chat failure: a code the page does not know shows the sentence of the server', () => {
  const view = failureView(en, { code: 'quota', text: 'The model refused the request: your quota is used up or the server is busy.' })
  assert.equal(view.reason, 'The model refused the request: your quota is used up or the server is busy.')
  assert.equal(view.hint, '')
})

test('chat failure: without a cause the banner still says the turn failed', () => {
  const view = failureView(en, null)
  assert.match(view.reason, /stopped because of an error/)
  assert.equal(view.detail, '')
})

test('chat failure: every locale has every reason and hint the codes use', () => {
  const want = Object.keys(load('en').error.reason).map(k => `reason.${k}`)
    .concat(Object.keys(load('en').error.hint).map(k => `hint.${k}`))
    .concat(['title', 'kept', 'retry', 'detail', 'openBackends'])
  const langs = readdirSync(locales).filter(l => l !== 'en')
  assert.equal(langs.length, 8)
  for (const lng of langs) {
    const error = load(lng).error
    for (const key of want) {
      const text = key.split('.').reduce((o, k) => o?.[k], error)
      assert.ok(typeof text === 'string' && text !== '', `${lng} lacks error.${key}`)
    }
  }
})

test('chat failure: an old failure seen after a reload is stale, a recent one is not', () => {
  const now = Date.parse('2026-10-30T12:00:00Z')
  assert.equal(isStaleFailure('2026-10-30T11:00:00Z', now), false)
  assert.equal(isStaleFailure(new Date(now - STALE_FAILURE_MS - 1000).toISOString(), now), true)
  assert.equal(isStaleFailure('', now), false)
})
