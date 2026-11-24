import test from 'node:test'
import assert from 'node:assert/strict'
import { summarizeInstall } from '../../src/utils/nodeInstallSummary.js'

test('node install summary: all nodes succeed, toast and close', () => {
  const s = summarizeInstall({ a: { status: 'done' }, b: { status: 'done' } })
  assert.deepEqual([s.success, s.fail, s.autoClose], [2, 0, true])
  assert.deepEqual(s.toast, { message: 'Installed on 2 nodes', type: 'success' })
})

test('node install summary: one node says "node", not "nodes"', () => {
  assert.equal(summarizeInstall({ a: { status: 'done' } }).toast.message, 'Installed on 1 node')
})

test('node install summary: every node fails, error toast and stay open', () => {
  const s = summarizeInstall({ a: { status: 'error' }, b: { status: 'error' } })
  assert.deepEqual(s.toast, { message: 'Install failed on all 2 nodes', type: 'error' })
  assert.equal(s.autoClose, false)
})

test('node install summary: a mixed run warns and stays open', () => {
  const s = summarizeInstall({ a: { status: 'done' }, b: { status: 'error' }, c: { status: 'error' } })
  assert.deepEqual(s.toast, { message: 'Installed on 1, failed on 2', type: 'warning' })
  assert.equal(s.autoClose, false)
})

test('node install summary: a repeated error counts once per node', () => {
  const s = summarizeInstall({ a: { status: 'error', error: 'x' } })
  assert.deepEqual([s.success, s.fail], [0, 1])
})

test('node install summary: nodes still installing or no outcomes give no toast', () => {
  assert.equal(summarizeInstall({ a: { status: 'installing' } }).toast, null)
  assert.equal(summarizeInstall({}).toast, null)
  assert.equal(summarizeInstall(undefined).toast, null)
})
