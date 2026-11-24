import assert from 'node:assert/strict'
import test from 'node:test'

import { voiceProfileFields } from './changeProposal.js'

test('voiceProfileFields shows the pin of the recording: size, duration and 8 hash characters', () => {
  const content = JSON.stringify({
    name: 'Narrator', audio: '/workspace/home/clip.wav', transcript: 'Hello.',
    audio_sha256: 'abcdef0123456789'.repeat(4), audio_bytes: 64044, audio_ms: 2000,
  })
  const f = voiceProfileFields({ content })
  assert.equal(f.clip, '63 KiB · 2.0 s · SHA-256 abcdef01')
})

test('voiceProfileFields has no pin line for a proposal without one', () => {
  assert.equal(voiceProfileFields({ content: '{"name":"N","audio":"a","transcript":"t"}' }).clip, '')
  assert.equal(voiceProfileFields({ content: 'not json' }).clip, '')
})

test('voiceProfileFields lists every clip of a profile with several, with the totals of the pin', () => {
  const content = JSON.stringify({
    name: 'Narrator', audio: 'a.wav', transcript: 'One.', extra_clips: [{ audio: 'b.wav', transcript: 'Two.' }],
    audio_sha256: 'abcdef0123456789'.repeat(4), audio_bytes: 128088, audio_ms: 4000,
  })
  const f = voiceProfileFields({ content })
  assert.equal(f.audio, 'a.wav, b.wav')
  assert.equal(f.transcript, '1. One. 2. Two.')
  assert.equal(f.clip, '2 clips · 125 KiB · 4.0 s · SHA-256 abcdef01')
})
