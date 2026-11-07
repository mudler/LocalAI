import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

import i18next from 'i18next'

import { barItems, chipLoops, groupView, isLiveWait, isWait, loopSummary, spanText, waitView } from '../../src/utils/chatLoops.js'

// The real English strings, so a missing key or a changed sentence fails here.
const en = JSON.parse(readFileSync(new URL('../../public/locales/en/chats.json', import.meta.url), 'utf8'))
const i18n = i18next.createInstance()
await i18n.init({ lng: 'en', resources: { en: { chats: en } }, ns: ['chats'], defaultNS: 'chats', interpolation: { escapeValue: false } })
const t = i18n.getFixedT('en', 'chats')

const NOW = Date.parse('2026-11-06T10:00:00Z')
const at = (s) => new Date(NOW + s * 1000).toISOString()

const wait = (watch = {}, extra = {}) => ({
  id: 'w1', prompt: 'Wait for the download', interval: '1m', interval_seconds: 60, state: 'active', runs: 0, running: false,
  kind: 'wait', created: at(-600),
  watch: {
    phase: 'waiting', reason: 'The download of the model files has to finish first.', source_kind: 'tool', source: 'list_downloads',
    mode: 'change', condition: 'the output changes', checks: 4, last_check: at(-20), outcome: 'unchanged',
    next_check: at(40), deadline: at(3540), errors: 0, max_errors: 3, ...watch,
  },
  ...extra,
})

const text = (v, key) => v.facts.find(f => f.key === key)?.text

test('a wait that waits says what for, the source, the condition, the checks, the next check and the deadline', () => {
  const v = waitView(t, wait(), NOW)
  assert.equal(v.phase, 'waiting')
  assert.equal(v.tone, 'run')
  assert.equal(v.title, 'Waiting for')
  assert.equal(v.reason, 'The download of the model files has to finish first.')
  assert.equal(text(v, 'source'), 'Watching tool list_downloads')
  assert.equal(text(v, 'until'), 'Until the output changes')
  assert.equal(text(v, 'checked'), 'Checked 4 times, last 20 s ago · no change')
  assert.equal(text(v, 'next'), 'Next check in 40 s')
  assert.match(text(v, 'deadline'), /^Gives up at .+ \(in 59 min\)$/)
  assert.equal(text(v, 'error'), undefined)
})

test('a URL source is shown by the name the server sent, and a first check is counted in the singular', () => {
  const v = waitView(t, wait({ source_kind: 'url', source: 'status.example.com/api/job', checks: 1 }), NOW)
  assert.equal(text(v, 'source'), 'Watching status.example.com/api/job')
  assert.match(text(v, 'checked'), /^Checked 1 time, last 20 s ago/)
})

test('a wait that was not checked yet says so, and does not invent a check', () => {
  const v = waitView(t, wait({ checks: 0, last_check: undefined, outcome: '' }), NOW)
  assert.equal(text(v, 'checked'), 'Not checked yet')
})

test('failed checks in a row turn the wait amber and show the message and the count of the maximum', () => {
  const v = waitView(t, wait({ errors: 2, outcome: 'error', last_error: 'connection refused' }), NOW)
  assert.equal(v.tone, 'warn')
  assert.equal(text(v, 'error'), 'Last check failed (2 of 3 in a row): connection refused')
  assert.match(text(v, 'checked'), /· failed$/)
  const bare = waitView(t, wait({ errors: 1, outcome: 'error' }), NOW)
  assert.equal(text(bare, 'error'), 'Last check failed (1 of 3 in a row)')
})

test('a wait that is settled and about to wake the model says how it was settled', () => {
  const met = waitView(t, wait({ phase: 'waking', end: 'condition_met', next_check: undefined, outcome: 'changed' }), NOW)
  assert.equal(met.title, 'The condition was met. Asking the model what to do.')
  assert.equal(met.tone, 'ok')
  assert.equal(text(met, 'next'), undefined)
  assert.equal(text(met, 'deadline'), undefined)
  assert.equal(met.reason, '')
  const failing = waitView(t, wait({ phase: 'waking', end: 'source_failing', errors: 3, outcome: 'error', last_error: 'timeout' }), NOW)
  assert.equal(failing.title, 'The source failed. Asking the model what to do.')
  assert.equal(failing.tone, 'error')
  const late = waitView(t, wait({ phase: 'waking', end: 'timed_out' }), NOW)
  assert.equal(late.title, 'Timed out, the condition was not met. Asking the model what to do.')
  assert.equal(late.tone, 'warn')
})

test('a paused wait says it is paused and keeps its reason; it shows no next check', () => {
  const v = waitView(t, wait({ phase: 'paused', next_check: undefined }), NOW)
  assert.equal(v.title, 'Wait paused')
  assert.equal(v.tone, 'warn')
  assert.equal(v.reason, 'The download of the model files has to finish first.')
  assert.equal(text(v, 'next'), undefined)
})

test('a wait that ended because the condition was met reads as done', () => {
  const v = waitView(t, wait({ phase: 'ended', end: 'condition_met', next_check: undefined, outcome: 'changed', checks: 6 }, { state: 'ended' }), NOW)
  assert.equal(v.title, 'Done: the condition was met')
  assert.equal(v.tone, 'ok')
  assert.equal(v.icon, 'check-circle')
  assert.equal(text(v, 'next'), undefined)
  assert.equal(text(v, 'deadline'), undefined)
  assert.match(text(v, 'checked'), /^Checked 6 times/)
})

test('a wait whose source failed ends in red, with the count and the last error, never as success', () => {
  const v = waitView(t, wait({ phase: 'ended', end: 'source_failing', errors: 3, outcome: 'error', last_error: 'status.example.com/api: 503', next_check: undefined }, { state: 'ended' }), NOW)
  assert.equal(v.title, 'Stopped: the source failed 3 times in a row')
  assert.equal(v.tone, 'error')
  assert.equal(v.icon, 'close-circle')
  assert.equal(text(v, 'error'), 'Last error: status.example.com/api: 503')
})

test('a wait that timed out says the condition was not met', () => {
  const v = waitView(t, wait({ phase: 'ended', end: 'timed_out', next_check: undefined }, { state: 'ended' }), NOW)
  assert.equal(v.title, 'Timed out, the condition was not met')
  assert.equal(v.tone, 'warn')
  assert.equal(v.icon, 'warning')
  assert.notEqual(v.title, waitView(t, wait({ phase: 'ended', end: 'condition_met' }, { state: 'ended' }), NOW).title)
})

test('a wait that ended some other way is only stopped', () => {
  const v = waitView(t, wait({ phase: 'ended', end: 'stopped', next_check: undefined }, { state: 'ended' }), NOW)
  assert.equal(v.title, 'Wait stopped')
  assert.equal(v.tone, 'idle')
})

test('the three ways to end differ in tone and in icon, so colour is never the only sign', () => {
  const ends = ['condition_met', 'source_failing', 'timed_out', 'stopped'].map(end => waitView(t, wait({ phase: 'ended', end, errors: 3 }, { state: 'ended' }), NOW))
  assert.equal(new Set(ends.map(v => v.icon)).size, 4)
  assert.equal(new Set(ends.map(v => v.title)).size, 4)
})

test('spans read as seconds, minutes, or hours and minutes', () => {
  assert.equal(spanText(t, 45_000), '45 s')
  assert.equal(spanText(t, 12 * 60_000), '12 min')
  assert.equal(spanText(t, 80 * 60_000), '1 h 20 min')
  assert.equal(spanText(t, 2 * 3600_000), '2 h')
  assert.equal(spanText(t, -5), '0 s')
})

test('a check that just happened says so, and a next check that is due says now', () => {
  const v = waitView(t, wait({ last_check: at(-2), next_check: at(0) }), NOW)
  assert.match(text(v, 'checked'), /last just now/)
  assert.equal(text(v, 'next'), 'Next check now')
})

test('only a wait with a watch is a wait; a plain loop and nothing at all are not', () => {
  assert.equal(isWait(wait()), true)
  assert.equal(isWait({ id: 'l1', state: 'active', kind: 'wait' }), false)
  assert.equal(isWait({ id: 'l1', state: 'active' }), false)
  assert.equal(isWait(null), false)
  assert.deepEqual(chipLoops([]), [])
  assert.deepEqual(chipLoops(undefined), [])
})

test('waits are listed with the other loops: running first, then the ended ones', () => {
  const list = [wait({ phase: 'ended', end: 'timed_out' }, { id: 'a', state: 'ended' }), wait({}, { id: 'b' }), wait({ phase: 'paused' }, { id: 'c', state: 'paused' })]
  assert.deepEqual(chipLoops(list).map(l => l.id), ['b', 'c', 'a'])
})

test('a looping sub-agent shows its loop or its wait in one line', () => {
  const sub = { id: 's1', running: true, loop: { id: 'l9', state: 'active', interval_seconds: 0, runs: 3 } }
  assert.equal(loopSummary(t, sub.loop, NOW), 'self-paced · run 3')
  const w = wait()
  assert.equal(loopSummary(t, w, NOW), 'waiting · Next check in 40 s')
  assert.equal(loopSummary(t, w, NOW, false), 'Next check in 40 s')
  const done = wait({ phase: 'ended', end: 'timed_out' }, { state: 'ended' })
  assert.equal(loopSummary(t, done, NOW), 'ended · timed out')
})

test('every locale has every key of the wait strings', () => {
  const flat = (o, p = '') => Object.entries(o).flatMap(([k, v]) => (v && typeof v === 'object' ? flat(v, `${p}${k}.`) : [`${p}${k}`]))
  const want = flat(en.loops.wait).sort()
  for (const lng of ['de', 'es', 'fr', 'id', 'it', 'ko', 'pt-BR', 'zh-CN']) {
    const other = JSON.parse(readFileSync(new URL(`../../public/locales/${lng}/chats.json`, import.meta.url), 'utf8'))
    assert.deepEqual(flat(other.loops.wait).sort(), want, lng)
    assert.ok(other.subAgents.waiting && other.subAgents.looping, lng)
  }
})

// --- Many waits fold into one chip ------------------------------------------

const many = (n) => Array.from({ length: n }, (_, i) => wait({ deadline: at(1000 + i * 100), last_check: at(-10 - i) }, { id: `w${i}` }))
const kinds = (items) => items.map(i => i.kind)

test('one or two live waits keep their own chips', () => {
  assert.deepEqual(kinds(barItems(many(1))), ['wait'])
  assert.deepEqual(kinds(barItems(many(2))), ['wait', 'wait'])
})

test('three live waits fold into one group that keeps every wait', () => {
  const items = barItems(many(3))
  assert.deepEqual(kinds(items), ['group'])
  assert.deepEqual(items[0].waits.map(w => w.id), ['w0', 'w1', 'w2'])
})

test('a wait that woke the model or ended stays outside the group, and does not count towards it', () => {
  const woke = wait({ phase: 'waking', end: 'condition_met' }, { id: 'woke' })
  const done = wait({ phase: 'ended', end: 'timed_out' }, { id: 'done', state: 'ended' })
  assert.ok(!isLiveWait(woke) && !isLiveWait(done))
  assert.deepEqual(kinds(barItems([...many(2), woke, done])), ['wait', 'wait', 'wait', 'wait'])
  const items = barItems([...many(3), woke, done])
  assert.deepEqual(kinds(items), ['group', 'wait', 'wait'])
  assert.deepEqual(items.slice(1).map(i => i.loop.id), ['woke', 'done'])
})

test('a paused wait counts as live; a loop that is not a wait is never grouped', () => {
  const paused = wait({ phase: 'paused' }, { id: 'p', state: 'paused' })
  const plain = { id: 'l1', prompt: 'x', state: 'active', interval_seconds: 300, runs: 1 }
  const items = barItems([plain, ...many(2), paused])
  assert.deepEqual(kinds(items), ['loop', 'group'])
  assert.equal(items[1].waits.length, 3)
})

test('the group line counts the waits and names the nearest deadline and the latest check', () => {
  const v = groupView(t, many(6), NOW)
  assert.equal(v.count, 6)
  assert.match(v.facts.find(f => f.key === 'deadline').text, /^Nearest deadline .+ \(in 17 min\)$/)
  assert.equal(v.facts.find(f => f.key === 'checked').text, 'Last check 10 s ago')
  assert.equal(v.facts.find(f => f.key === 'paused'), undefined)
  assert.equal(t('loops.wait.group.title', { count: 6 }), '6 waiting')
  const p = groupView(t, [...many(2), wait({ phase: 'paused' }, { id: 'p', state: 'paused' })], NOW)
  assert.equal(p.facts[0].text, '1 paused')
})
