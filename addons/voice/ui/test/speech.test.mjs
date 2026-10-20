import test from 'node:test'
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { dirname, join, resolve } from 'node:path'
import { load } from './render.test.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const require = createRequire(join(resolve(here, '../../../../core/http/react-ui'), 'package.json'))
const React = require('react')
const { renderToString } = require('react-dom/server')

test('speech: the first paint shows the title and the text box', async () => {
  const { mod, sdk } = await load()
  const addon = await mod.createAddon(sdk)
  const html = renderToString(React.createElement(addon.Component, { page: 'speech', path: '' }))
  assert.match(html, /data-testid="voice-speech"/)
  assert.match(html, /<textarea/)
})

test('speech: the request is POST /v1/audio/speech with the model, the text and the chosen voice', async () => {
  const { buildSpeechRequest } = await import('../src/speechRequest.js')
  assert.deepEqual(buildSpeechRequest({ model: 'piper', text: ' hello ', voice: 'amy', instructions: ' calm ' }), {
    model: 'piper', input: 'hello', voice: 'amy', instructions: 'calm',
  })
  assert.deepEqual(buildSpeechRequest({ model: 'piper', text: 'hi', voice: '', instructions: '' }), { model: 'piper', input: 'hi' })
})

test('speech: nothing is sent without text or without a model', async () => {
  const { buildSpeechRequest } = await import('../src/speechRequest.js')
  assert.equal(buildSpeechRequest({ model: '', text: 'x' }), null)
  assert.equal(buildSpeechRequest({ model: 'm', text: '   ' }), null)
})

test('speech: a failed answer surfaces the API message', async () => {
  const { speak } = await import('../src/speechRequest.js')
  const core = { fetch: async () => ({ ok: false, status: 403, json: async () => ({ error: { message: 'feature not enabled for your account' } }), blob: async () => null }) }
  await assert.rejects(() => speak(core, { model: 'm', input: 'x' }), /feature not enabled for your account/)
})
