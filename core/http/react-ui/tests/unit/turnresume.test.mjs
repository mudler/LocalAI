import test from 'node:test'
import assert from 'node:assert/strict'
import { createResumer, nextResume, resumeWait, RESUME_MAX_ATTEMPTS, RESUME_CAP_MS, RESUME_WINDOW_MS } from '../../src/utils/turnResume.js'

const NOW = Date.parse('2026-11-06T12:00:00Z')
const fresh = new Date(NOW - 1000).toISOString()

// A scheduler the test drives by hand.
function clock() {
  const timers = new Map()
  let n = 0
  return {
    schedule: (fn, ms) => { timers.set(++n, { fn, ms }); return n },
    cancel: (t) => { timers.delete(t) },
    pending: () => [...timers.values()],
    fire() { const [[k, t]] = [...timers]; timers.delete(k); t.fn() },
  }
}

const interrupted = (extra = {}) => ({ id: 'c1', lastTurn: 'interrupted', running: false, resuming: false, updated: fresh, ...extra })

test('turn resume: only a fresh interrupted turn is resumed by the page', () => {
  const base = { running: false, attempts: 0, updated: fresh, now: NOW }
  assert.equal(nextResume({ ...base, lastTurn: 'interrupted' }), 'wait')
  assert.equal(nextResume({ ...base, lastTurn: 'failed' }), 'none', 'a model error needs a person')
  assert.equal(nextResume({ ...base, lastTurn: 'stopped' }), 'none')
  assert.equal(nextResume({ ...base, lastTurn: 'done' }), 'none')
  assert.equal(nextResume({ ...base, lastTurn: 'interrupted', running: true }), 'none')
  assert.equal(nextResume({ ...base, lastTurn: 'interrupted', stopped: true }), 'none')
  assert.equal(nextResume({ ...base, lastTurn: 'interrupted', updated: new Date(NOW - RESUME_WINDOW_MS - 1).toISOString() }), 'none')
  assert.equal(nextResume({ ...base, lastTurn: 'interrupted', attempts: RESUME_MAX_ATTEMPTS }), 'exhausted')
})

test('turn resume: the wait doubles and stops at the cap', () => {
  assert.equal(resumeWait(0), 3000)
  assert.equal(resumeWait(1), 6000)
  assert.equal(resumeWait(2), 12000)
  assert.equal(resumeWait(30), RESUME_CAP_MS)
})

test('turn resume: resumes after the wait, then again, then gives up after N tries', () => {
  const c = clock()
  const calls = []
  const views = []
  const r = createResumer({ resume: (id) => { calls.push(id) }, onChange: v => views.push(v.phase), schedule: c.schedule, cancel: c.cancel, now: () => NOW })
  r.observe(interrupted())
  assert.deepEqual(r.view, { phase: 'waiting', attempt: 1, max: 3, seconds: 3 })
  r.observe(interrupted()) // the same state again must not add a timer
  assert.equal(c.pending().length, 1)
  c.fire()
  assert.deepEqual(calls, ['c1'])
  assert.equal(r.view.phase, 'resuming')

  // The resume ended and the chat is interrupted again (another restart).
  r.observe(interrupted())
  assert.deepEqual(r.view, { phase: 'waiting', attempt: 2, max: 3, seconds: 6 })
  c.fire()
  r.observe(interrupted())
  assert.equal(r.view.seconds, 12)
  c.fire()
  assert.equal(calls.length, 3)
  r.observe(interrupted())
  assert.equal(r.view.phase, 'exhausted')
  assert.equal(c.pending().length, 0, 'no fourth try')
})

test('turn resume: Stop cancels the wait and the chat stays for the person', () => {
  const c = clock()
  const calls = []
  const r = createResumer({ resume: (id) => calls.push(id), schedule: c.schedule, cancel: c.cancel, now: () => NOW })
  r.observe(interrupted())
  r.stop()
  assert.equal(c.pending().length, 0)
  assert.equal(r.view.phase, 'idle')
  r.observe(interrupted())
  assert.equal(c.pending().length, 0, 'it does not start again by itself')
  assert.deepEqual(calls, [])
  // A new turn clears the person's no.
  r.observe(interrupted({ running: true }))
  r.observe(interrupted())
  assert.equal(r.view.phase, 'waiting')
})

test('turn resume: a finished turn resets the tries, and another chat has its own', () => {
  const c = clock()
  const r = createResumer({ resume: () => {}, schedule: c.schedule, cancel: c.cancel, now: () => NOW })
  r.observe(interrupted()); c.fire()
  r.observe(interrupted({ lastTurn: 'done' }))
  r.observe(interrupted())
  assert.equal(r.view.attempt, 1, 'the count started again after a finished turn')
  r.observe(interrupted({ id: 'c2' }))
  assert.equal(r.view.attempt, 1)
  assert.equal(c.pending().length, 1, 'the timer of the first chat is gone')
})

test('turn resume: a manual resume in flight shows as resuming and adds no timer', () => {
  const c = clock()
  const r = createResumer({ resume: () => {}, schedule: c.schedule, cancel: c.cancel, now: () => NOW })
  r.observe(interrupted({ resuming: true }))
  assert.equal(r.view.phase, 'resuming')
  assert.equal(c.pending().length, 0)
})

test('turn resume: when the server resumes, the page runs no timer of its own', () => {
  const c = clock()
  const calls = []
  const r = createResumer({ resume: (id) => { calls.push(id) }, refresh: () => {}, schedule: c.schedule, cancel: c.cancel, now: () => NOW })
  r.observe(interrupted({ willResume: true, resumeAttempts: 1 }))
  assert.equal(r.view.phase, 'server')
  assert.equal(r.view.attempt, 2, 'the next try is number 2')
  assert.deepEqual(c.pending().map(t => t.ms), [30000], 'only the slow re-read is scheduled, not a resume')
  assert.deepEqual(calls, [])
  // The server is done with the chat: the timer is gone and the turn runs.
  r.observe(interrupted({ running: true, willResume: false }))
  assert.equal(r.view.phase, 'idle')
  assert.equal(c.pending().length, 0)
})

test('turn resume: a waiting page hands over to the server', () => {
  const c = clock()
  const r = createResumer({ resume: () => {}, schedule: c.schedule, cancel: c.cancel, now: () => NOW })
  r.observe(interrupted())
  assert.equal(r.view.phase, 'waiting')
  r.observe(interrupted({ willResume: true }))
  assert.equal(r.view.phase, 'server')
  assert.deepEqual(c.pending().map(t => t.ms), [30000], 'its own resume timer is cancelled')
})

test('turn resume: a chat the server gave up on is not retried by the page', () => {
  const c = clock()
  const calls = []
  const r = createResumer({ resume: (id) => { calls.push(id) }, schedule: c.schedule, cancel: c.cancel, now: () => NOW })
  r.observe(interrupted({ willResume: false, resumeAttempts: 3 }))
  assert.equal(r.view.phase, 'exhausted')
  assert.equal(r.view.attempt, 3)
  assert.equal(c.pending().length, 0)
  assert.equal(nextResume({ lastTurn: 'interrupted', running: false, updated: fresh, now: NOW, serverTried: 1 }), 'exhausted')
  assert.equal(nextResume({ lastTurn: 'interrupted', running: false, updated: fresh, now: NOW, willResume: true }), 'server')
})

test('turn resume: while the server resumes the page reads the chat again until it is done', () => {
  const c = clock()
  const reads = []
  const r = createResumer({ resume: () => {}, refresh: (id) => { reads.push(id) }, schedule: c.schedule, cancel: c.cancel, now: () => NOW })
  r.observe(interrupted({ willResume: true }))
  c.fire()
  assert.deepEqual(reads, ['c1'])
  assert.equal(c.pending().length, 1, 'it reads again later')
  r.dispose()
  assert.equal(c.pending().length, 0)
})
