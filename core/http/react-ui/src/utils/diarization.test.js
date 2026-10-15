import assert from 'node:assert/strict'
import test from 'node:test'

import { axisTicks, clock, hasText, runLength, speakerRows, toRttm, toSrt } from './diarization.js'

const result = {
  speakers: [{ id: 'SPEAKER_00', label: '0' }, { id: 'SPEAKER_01', label: '1', name: 'Ada' }],
  segments: [
    { id: 0, speaker: 'SPEAKER_00', label: '0', start: 0, end: 6, text: 'Morning everyone.' },
    { id: 1, speaker: 'SPEAKER_01', label: '1', start: 6.5, end: 8.5, text: ' Hello. ' },
    { id: 2, speaker: 'SPEAKER_00', label: '0', start: 9, end: 11 },
  ],
}

test('clock reads minutes and seconds, hours only when needed', () => {
  assert.equal(clock(0), '0:00')
  assert.equal(clock(74.6), '1:15')
  assert.equal(clock(3725), '1:02:05')
  assert.equal(clock('x'), '0:00')
})

test('speakerRows sums talk time per speaker in order of first speech', () => {
  const rows = speakerRows(result)
  assert.deepEqual(rows.map(r => r.label), ['0', '1'])
  assert.equal(rows[0].seconds, 8)
  assert.equal(rows[1].seconds, 2)
  assert.equal(rows[1].name, 'Ada')
  assert.ok(Math.abs(rows[0].share - 0.8) < 1e-9)
})

test('speakerRows keeps a speaker that has no segment', () => {
  const rows = speakerRows({ speakers: [{ id: 'S', label: '4' }], segments: [] })
  assert.equal(rows.length, 1)
  assert.equal(rows[0].share, 0)
})

test('runLength is the last end, zero when empty', () => {
  assert.equal(runLength(result), 11)
  assert.equal(runLength({}), 0)
})

test('axisTicks steps over round numbers and spans the run', () => {
  const ticks = axisTicks(168)
  assert.equal(ticks[0].at, 0)
  assert.ok(ticks.length >= 4 && ticks.length <= 8)
  assert.ok(ticks.every(t => t.pct >= 0 && t.pct <= 100))
  assert.deepEqual(axisTicks(0), [])
})

test('toRttm writes one line per segment and uses a known name', () => {
  const lines = toRttm(result, 'team call.wav').trim().split('\n')
  assert.equal(lines.length, 3)
  assert.equal(lines[0], 'SPEAKER team_call 1 0.000 6.000 <NA> <NA> SPEAKER_00 <NA> <NA>')
  assert.match(lines[1], /Ada/)
})

test('toSrt keeps only segments with text and numbers them', () => {
  const srt = toSrt(result)
  assert.match(srt, /^1\n00:00:00,000 --> 00:00:06,000\nSPEAKER_00: Morning everyone\./)
  assert.match(srt, /\n2\n00:00:06,500 --> 00:00:08,500\nAda: Hello\./)
  assert.equal(srt.includes('\n3\n'), false)
  assert.equal(hasText(result), true)
  assert.equal(hasText({ segments: [{ start: 0, end: 1 }] }), false)
  assert.equal(toSrt({ segments: [] }), '')
})
