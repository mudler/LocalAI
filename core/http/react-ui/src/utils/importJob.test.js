import assert from 'node:assert/strict'
import test from 'node:test'

import { classifyImportJob, waitForImportJob } from './importJob.js'

test('reads a running job', () => {
  assert.equal(classifyImportJob({ processed: false, message: 'downloading' }).state, 'running')
  assert.equal(classifyImportJob(null).state, 'running')
})

test('reads completion from either endpoint shape', () => {
  assert.equal(classifyImportJob({ completed: true }).state, 'done')
  assert.deepEqual(
    classifyImportJob({ processed: true, gallery_element_name: 'm' }),
    { state: 'done', message: '', name: 'm' },
  )
})

test('failure wins over processed', () => {
  assert.deepEqual(classifyImportJob({ processed: true, error: 'boom' }), { state: 'failed', message: 'boom' })
  assert.deepEqual(classifyImportJob({ processed: true, error: { message: 'bad' } }), { state: 'failed', message: 'bad' })
  assert.deepEqual(classifyImportJob({ processed: true, message: 'error: no space' }), { state: 'failed', message: 'no space' })
  assert.deepEqual(classifyImportJob({ processed: true, cancelled: true }), { state: 'failed', message: 'Import cancelled' })
})

const fast = { intervalMs: 0 }

test('wait resolves when the job finishes', async () => {
  const answers = [{ processed: false }, { processed: true }]
  const status = await waitForImportJob('j', { ...fast, getStatus: async () => answers.shift() })
  assert.equal(status.state, 'done')
})

test('wait rejects with the job error', async () => {
  await assert.rejects(
    waitForImportJob('j', { ...fast, getStatus: async () => ({ processed: true, error: 'download failed' }) }),
    /download failed/,
  )
})

test('wait gives up when the status endpoint keeps failing', async () => {
  let calls = 0
  await assert.rejects(
    waitForImportJob('j', {
      ...fast,
      maxConsecutiveFailures: 3,
      getStatus: async () => { calls++; throw new Error('404 not found') },
    }),
    /Lost track of the import job: 404 not found/,
  )
  assert.equal(calls, 3)
})

test('a successful read resets the failure count', async () => {
  const answers = [new Error('x'), new Error('x'), { processed: false }, new Error('x'), new Error('x'), { processed: true }]
  const status = await waitForImportJob('j', {
    ...fast,
    maxConsecutiveFailures: 3,
    getStatus: async () => { const a = answers.shift(); if (a instanceof Error) throw a; return a },
  })
  assert.equal(status.state, 'done')
})

test('wait stops at the time limit', async () => {
  let t = 0
  await assert.rejects(
    waitForImportJob('j', {
      ...fast,
      maxWaitMs: 5,
      now: () => (t += 3),
      getStatus: async () => ({ processed: false }),
    }),
    /still running/,
  )
})

test('wait rejects with AbortError when the signal fires', async () => {
  const ac = new AbortController()
  const p = waitForImportJob('j', { intervalMs: 10000, signal: ac.signal, getStatus: async () => ({}) })
  ac.abort()
  await assert.rejects(p, err => err.name === 'AbortError')
})
