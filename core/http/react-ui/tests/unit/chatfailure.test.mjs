import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { updateForBackend, withMetaUpdates } from '../../src/utils/updates.js'
import { failureView, isStaleFailure, savedFailure, keptAbove, STALE_FAILURE_MS } from '../../src/utils/chatFailure.js'

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

test('chat failure: a model that is still loading says so and asks to try again later', () => {
  const view = failureView(en, { code: 'model_loading', model: 'test' })
  assert.equal(view.reason, 'The model "test" is still loading.')
  assert.match(view.hint, /few minutes/)
  assert.equal(failureView(en, { code: 'model_loading' }).reason, 'The model is still loading.')
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
    .concat(['title', 'kept', 'retry', 'detail', 'openBackends', 'updateBackend', 'updateBackendBusy', 'updateBackendStarted', 'updateBackendFailed', 'openUpdates'])
  const langs = readdirSync(locales).filter(l => l !== 'en')
  assert.equal(langs.length, 10)
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

test('chat failure: the reason the server keeps in the transcript is read back for a reload', () => {
  const messages = [
    { role: 'user', text: 'build the report' },
    { role: 'assistant', text: 'step one' },
    { role: 'failure', text: 'The provider failed.', failure: { code: 'upstream', model: 'm', retry_after: 3, detail: 'HTTP 502' } },
    { role: 'checkpoint', text: '' },
  ]
  assert.deepEqual(savedFailure(messages), { code: 'upstream', model: 'm', retry: 3, detail: 'HTTP 502', backend: '', text: 'The provider failed.' })
  // A transcript that went on after the failure has no failure to show.
  assert.equal(savedFailure([...messages, { role: 'user', text: 'again' }]), null)
  assert.equal(savedFailure([]), null)
  assert.equal(savedFailure(undefined), null)
})

test('chat failure: the banner says what stays only when the failed turn produced something', () => {
  assert.equal(keptAbove([{ role: 'user' }, { role: 'failure' }]), false)
  assert.equal(keptAbove([{ role: 'user' }, { role: 'assistant' }, { role: 'tool' }, { role: 'failure' }]), true)
  assert.equal(keptAbove([{ role: 'assistant' }, { role: 'user' }, { role: 'failure' }]), false, 'only the last turn counts')
})

test('chat failure: an interrupted turn says the server restarted, in every language, with no hint of a cause', () => {
  assert.equal(failureView(en, { code: 'interrupted' }).reason, 'The server restarted while it was answering.')
  assert.equal(failureView(en, { code: 'interrupted' }).hint, '')
  for (const lng of readdirSync(locales)) {
    const t = translator(lng)
    assert.ok(failureView(t, { code: 'interrupted' }).reason, lng)
    for (const key of ['error.titleInterrupted', 'error.resume.waiting', 'error.resume.resuming', 'error.resume.exhausted', 'error.resume.stop', 'connection.lost']) {
      assert.ok(t(key, { seconds: 3, attempt: 1, max: 3 }), `${lng} ${key}`)
    }
    if (lng !== 'en') assert.notEqual(t('error.resume.stop'), en('error.resume.stop'), `${lng} is translated`)
  }
})

test('chat failure: a backend that is too old says so and names the backend for the update button', () => {
  const failure = { code: 'model_backend_outdated', model: 'qwen', backend: 'llama-cpp' }
  const admin = failureView(en, failure, { isAdmin: true })
  assert.equal(admin.reason, 'The model "qwen" could not be started: the backend may be too old for it.')
  assert.match(admin.hint, /Updates page/)
  assert.equal(admin.outdatedBackend, 'llama-cpp')
  assert.match(failureView(en, failure, { isAdmin: false }).hint, /administrator/)
  assert.equal(failureView(en, { code: 'model_load_failed', model: 'x', backend: 'llama-cpp' }).outdatedBackend, '')
})

test('chat failure: the update row of a backend is found by the name the loader gives', () => {
  const rows = [
    { kind: 'gallery', name: 'metal-llama-cpp', state: 'ready' },
    { kind: 'gallery', name: 'vllm', state: 'current' },
    { kind: 'library', name: 'llama-cpp', state: 'ready' },
  ]
  assert.equal(updateForBackend(rows, 'llama-cpp').name, 'metal-llama-cpp')
  assert.equal(updateForBackend(rows, 'metal-llama-cpp').name, 'metal-llama-cpp')
  assert.equal(updateForBackend(rows, 'vllm'), null, 'a current backend has nothing to update')
  assert.equal(updateForBackend(rows, ''), null)
  assert.equal(updateForBackend(undefined, 'llama-cpp'), null)
})

test('chat failure: a backend too old is worded in every language', () => {
  for (const lng of readdirSync(locales)) {
    const t = translator(lng)
    assert.ok(failureView(t, { code: 'model_backend_outdated', model: 'm', backend: 'b' }, { isAdmin: true }).hint, lng)
    assert.ok(failureView(t, { code: 'model_backend_outdated' }).reason, lng)
  }
})

test('backends list: the meta backend that points at an outdated build shows that build\'s update', () => {
  const upgrades = { 'metal-llama-cpp': { backend_name: 'metal-llama-cpp', available_version: 'v2' } }
  const installed = [
    { Name: 'llama-cpp', Metadata: { meta_backend_for: 'metal-llama-cpp' } },
    { Name: 'metal-llama-cpp', Metadata: {} },
    { Name: 'whisper', Metadata: { meta_backend_for: 'cpu-whisper' } },
  ]
  const out = withMetaUpdates(upgrades, installed)
  assert.equal(out['llama-cpp'].available_version, 'v2')
  assert.equal(out['metal-llama-cpp'].available_version, 'v2')
  assert.equal(out.whisper, undefined, 'no update for the build, none for the meta backend')
  assert.deepEqual(withMetaUpdates({}, undefined), {})
})
