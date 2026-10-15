import assert from 'node:assert/strict'
import test from 'node:test'

import {
  explainError, judgeClip, labelRows, labelsText, loadList, missingFromServer, parseLabels,
  rankMatches, saveList, scaleMax, scaleTicks, strength,
} from './identity.js'

test('strength reads the distance against the cut-off', () => {
  assert.equal(strength(0.1, 0.25), 'strong')
  assert.equal(strength(0.18, 0.25), 'likely')
  assert.equal(strength(0.24, 0.25), 'close')
  assert.equal(strength(0.27, 0.25), 'near')
  assert.equal(strength(0.5, 0.25), 'far')
  assert.equal(strength(NaN, 0.25), 'far')
})

test('rankMatches sorts, numbers and marks against the new cut-off', () => {
  const rows = rankMatches([{ id: 'b', distance: 0.41 }, { id: 'a', distance: 0.18, match: false }, { id: 'x' }], 0.25)
  assert.deepEqual(rows.map(r => [r.id, r.rank, r.within]), [['a', 1, true], ['b', 2, false]])
  assert.equal(rankMatches([{ id: 'b', distance: 0.41 }], 0.5)[0].within, true)
})

test('scaleMax keeps the cut-off and every dot on the scale', () => {
  assert.equal(scaleMax(0.25, [0.18, 0.52]), 0.6)
  assert.equal(scaleMax(0.35, []), 0.9)
  assert.deepEqual(scaleTicks(0.6).slice(0, 3), [0, 0.1, 0.2])
})

test('labelRows drops crowded labels to a second row', () => {
  assert.deepEqual(labelRows([10, 14, 60], 14), [0, 1, 0])
  assert.deepEqual(labelRows([10, 40]), [0, 0])
  assert.deepEqual(labelRows([60, 70, 80, 90]), [0, 1, 2, 0])
})

test('parseLabels reads key: value lines', () => {
  const labels = parseLabels('team: platform\nnotes\n role : lead ')
  assert.deepEqual(labels, { team: 'platform', role: 'lead' })
  assert.equal(labelsText(labels), 'team: platform, role: lead')
})

test('the saved list survives bad storage and keeps at most 50', () => {
  const store = new Map()
  const storage = { getItem: k => store.get(k) ?? null, setItem: (k, v) => store.set(k, v) }
  assert.deepEqual(loadList('k', storage), [])
  store.set('k', '{not json')
  assert.deepEqual(loadList('k', storage), [])
  saveList('k', Array.from({ length: 60 }, (_, i) => ({ id: String(i) })), storage)
  assert.equal(loadList('k', storage).length, 50)
  assert.deepEqual(loadList('k', { getItem() { throw new Error('blocked') } }), [])
})

test('missingFromServer claims nothing unless the search saw everyone', () => {
  const entries = [{ id: 'a' }, { id: 'b' }]
  assert.equal(missingFromServer(entries, null).size, 0)
  assert.equal(missingFromServer(entries, { complete: false, ids: ['a'] }).size, 0)
  assert.deepEqual([...missingFromServer(entries, { complete: true, ids: ['a'] })], ['b'])
})

test('explainError names the real cause', () => {
  assert.match(explainError({ message: 'no face detected' }, 'face').title, /No face/)
  assert.match(explainError({ status: 404, message: 'x' }, 'voice').title, /model/)
  assert.match(explainError({ status: 500, message: 'boom' }, 'voice').title, /failed/)
})

test('judgeClip flags short, quiet and clipped audio', () => {
  assert.equal(judgeClip({ seconds: 8, peak: 0.5, clipped: false }).level, 'good')
  assert.equal(judgeClip({ seconds: 1.2, peak: 0.5 }).notes.length, 1)
  assert.equal(judgeClip({ seconds: 8, peak: 0.01 }).level, 'warn')
  assert.equal(judgeClip({ seconds: 8, peak: 1, clipped: true }).notes.length, 1)
})
