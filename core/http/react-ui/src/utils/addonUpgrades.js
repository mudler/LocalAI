// The add-on upgrades this browser started, kept outside any page so a running
// upgrade survives a change of route and shows up in the Activity page.
//
// The add-on owner keeps its own operation journal; the page only follows it.
// This store does not decide anything: it submits the administrator's upgrade
// with the operation identity discipline of the other add-on actions (one
// identity per action, never a second submit after an unknown outcome), reads
// the operation until it settles, and keeps what it saw so a list can show it.
//
// Cancel aborts the request. The owner treats a canceled upgrade request as a
// cancellation (the last good generation is kept), and the store then reads
// the operation once more, so what it reports is the owner's answer and not
// the click.
import { createActionBook, effectOutcome } from '../components/addons/actionBook.js'
import { runLifecycle, settleOperation } from '../components/addons/lifecycleFlow.js'
import { updateErrorKey, updateView, upgradeBody } from '../components/addons/updateState.js'
import { AddonsError } from './addonsApi.js'

// How far along an operation phase is, for the progress bar. The owner reports
// phases, not percentages.
const PHASE_PROGRESS = { intent: 10, prepared: 50, applied: 85 }
const MAX_RECORDS = 50
const POLL_MS = 2000
const POLL_ATTEMPTS = 300

export function phaseProgress(phase) {
  return PHASE_PROGRESS[phase] || 5
}

export function createUpgradeStore({ now = Date.now, waits, sleep, book = createActionBook() } = {}) {
  let snapshot = { live: [], records: [] }
  const listeners = new Set()
  const controllers = new Map()
  const cancelled = new Set()
  // The wait of each followed upgrade, so "check now" can end it early.
  const wakers = new Map()
  const sleepOf = (jobID) => sleep || wakeableSleep(jobID)
  const wakeableSleep = (jobID) => (ms, signal) => new Promise((resolve, reject) => {
    const done = () => { clearTimeout(timer); wakers.delete(jobID); resolve() }
    const timer = setTimeout(done, ms)
    wakers.set(jobID, done)
    if (signal) {
      signal.addEventListener('abort', () => {
        clearTimeout(timer)
        wakers.delete(jobID)
        reject(new DOMException('Aborted', 'AbortError'))
      }, { once: true })
    }
  })
  const pollWaits = waits || Array.from({ length: POLL_ATTEMPTS }, () => POLL_MS)

  const emit = (next) => {
    snapshot = next
    for (const listener of listeners) listener()
  }
  const patch = (jobID, fields) => {
    emit({ ...snapshot, live: snapshot.live.map(op => (op.jobID === jobID ? { ...op, ...fields } : op)) })
  }
  const settle = (jobID, record) => {
    const live = snapshot.live.filter(op => op.jobID !== jobID)
    emit({ live, records: [{ ...record, jobID, finishedAt: new Date(now()).toISOString() }, ...snapshot.records].slice(0, MAX_RECORDS) })
  }
  const trackPhases = (api, jobID) => ({
    ...api,
    async operationStatus(...args) {
      const status = await api.operationStatus(...args)
      if (status && status.phase) patch(jobID, { phase: status.phase, progress: phaseProgress(status.phase) })
      return status
    },
  })

  const store = {
    subscribe(listener) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
    getSnapshot: () => snapshot,

    // Upgrades one installed add-on (the record of `addons-installed`). Never
    // throws: resolves { ok, message, outcome } where message is the key of the
    // words of a failure (see updateErrorKey) and is empty on success.
    async start(api, addon) {
      const view = updateView(addon && addon.update)
      if (!view.canUpgrade || !addon.digest) return { ok: false, outcome: 'invalid', message: 'incompatible' }
      if (snapshot.live.some(op => op.addonId === addon.id && op.state !== 'failed')) return { ok: false, outcome: 'busy', message: 'operation_conflict' }

      const key = `upgrade:${addon.id}:${addon.generation}`
      const begun = book.begin(addon.id, key)
      const name = addon.title || addon.id
      const base = {
        addonId: addon.id,
        id: addon.id,
        name,
        fullName: addon.id,
        isAddon: true,
        taskType: 'upgrade',
        fromVersion: addon.version || '',
        toVersion: view.version,
        startedAt: new Date(now()).toISOString(),
      }
      const withoutOld = snapshot.live.filter(op => op.addonId !== addon.id)
      if (begun.blocked) {
        const jobID = `addon:${addon.id}:${begun.operationId}`
        emit({ ...snapshot, live: [...withoutOld, { ...base, jobID, operationId: begun.operationId, state: 'failed', unknown: true, error: 'unknownOutcome', errorKey: 'unknownOutcome' }] })
        return { ok: false, outcome: 'blocked', message: 'unknownOutcome' }
      }
      const operationId = begun.operationId
      const jobID = `addon:${addon.id}:${operationId}`
      const controller = new AbortController()
      controllers.set(jobID, controller)
      emit({
        ...snapshot,
        live: [...withoutOld, { ...base, jobID, operationId, state: 'running', phase: 'intent', progress: phaseProgress('intent'), cancellable: true, error: '', errorKey: '' }],
      })
      return store.follow(api, { jobID, addon, operationId, key, controller, submit: true })
    },

    // Runs the submit (or, with submit false, only reads the earlier operation)
    // and turns its end into the entry of a list.
    async follow(api, { jobID, addon, operationId, key, controller, submit }) {
      const signal = controller.signal
      const tracked = trackPhases(api, jobID)
      try {
        let outcome
        // While the add-on service does not answer, the entry says that it
        // waits for it and when it asks again, instead of looking stuck.
        const onWait = ({ unavailable, waitMs }) => {
          const waiting = unavailable ? { since: snapshot.live.find(op => op.jobID === jobID)?.waiting?.since || now(), nextMs: waitMs } : null
          patch(jobID, { waiting })
        }
        if (submit) {
          const out = await runLifecycle(tracked, {
            operationId, addonId: addon.id, signal, waits: pollWaits, sleep: sleepOf(jobID), onWait,
            submit: () => {
              const body = upgradeBody(addon, operationId)
              if (!body) return Promise.reject(new AddonsError({ kind: 'invalid' }))
              return api.updates.upgrade(addon.id, body, signal)
            },
          })
          outcome = out.outcome
        } else {
          outcome = (await settleOperation(tracked, { operationId, addonId: addon.id, signal, waits: pollWaits, sleep: sleepOf(jobID), onWait })).outcome
        }
        let verified
        if (outcome === 'completed') {
          try {
            const now2 = (await api.installed(signal)).find(a => a.id === addon.id)
            verified = Boolean(now2) && now2.generation > addon.generation
          } catch (e) {
            if (e && e.name === 'AbortError') throw e
            verified = undefined
          }
        }
        return store.conclude(jobID, addon, key, effectOutcome(outcome, verified), operationId)
      } catch (e) {
        if (e && e.name === 'AbortError' && cancelled.has(jobID)) return store.afterCancel(api, { jobID, addon, operationId, key })
        const kind = e && e.kind ? e.kind : 'unavailable'
        if (kind === 'denied' || kind === 'invalid' || kind === 'auth') book.resolve(addon.id, key)
        const errorKey = updateErrorKey(e && e.kind ? e : { kind })
        patch(jobID, { state: 'failed', cancellable: false, error: errorKey, errorKey, unknown: errorKey === 'unknownOutcome' })
        if (errorKey === 'unknownOutcome') book.markUnknown(addon.id, key)
        return { ok: false, outcome: kind, message: errorKey }
      } finally {
        controllers.delete(jobID)
      }
    },

    conclude(jobID, addon, key, outcome, operationId) {
      if (outcome === 'unknown') {
        book.markUnknown(addon.id, key)
        patch(jobID, { state: 'failed', cancellable: false, unknown: true, error: 'unknownOutcome', errorKey: 'unknownOutcome' })
        return { ok: false, outcome, message: 'unknownOutcome' }
      }
      book.resolve(addon.id, key)
      if (outcome === 'completed') {
        const op = snapshot.live.find(o => o.jobID === jobID)
        settle(jobID, { ...op, outcome: 'completed', state: undefined })
        return { ok: true, outcome, message: '' }
      }
      const errorKey = outcome === 'not_applied' ? 'operation_conflict' : 'upgrade_failed'
      patch(jobID, { state: 'failed', cancellable: false, unknown: false, error: errorKey, errorKey, operationId })
      return { ok: false, outcome, message: errorKey }
    },

    // After the request was aborted by a cancel: what did the owner end up with?
    async afterCancel(api, { jobID, addon, operationId, key }) {
      cancelled.delete(jobID)
      let outcome = 'unknown'
      try {
        outcome = (await settleOperation(api, { operationId, addonId: addon.id, waits: [] })).outcome
      } catch { /* an unreadable status stays unknown */ }
      const op = snapshot.live.find(o => o.jobID === jobID)
      if (outcome === 'completed') {
        book.resolve(addon.id, key)
        settle(jobID, { ...op, outcome: 'completed' })
        return { ok: true, outcome, message: '' }
      }
      if (outcome === 'failed') {
        book.resolve(addon.id, key)
        settle(jobID, { ...op, outcome: 'cancelled' })
        return { ok: false, outcome: 'cancelled', message: 'canceled' }
      }
      book.markUnknown(addon.id, key)
      patch(jobID, { state: 'failed', cancellable: false, unknown: true, error: 'unknownOutcome', errorKey: 'unknownOutcome' })
      return { ok: false, outcome: 'unknown', message: 'unknownOutcome' }
    },

    // Ends the wait of a followed upgrade, so its operation is read now. False
    // when it is not waiting.
    checkNow(jobID) {
      const wake = wakers.get(jobID)
      if (!wake) return false
      wake()
      return true
    },

    cancel(jobID) {
      const controller = controllers.get(jobID)
      if (!controller) return false
      cancelled.add(jobID)
      patch(jobID, { state: 'cancelling', cancellable: false })
      controller.abort()
      return true
    },

    // Try a failed upgrade again. An unknown outcome is only read, never
    // submitted a second time; anything else starts from the installed record
    // as it is now, so a changed generation or candidate is honoured.
    async retry(api, jobID) {
      const op = snapshot.live.find(o => o.jobID === jobID)
      if (!op || op.state !== 'failed') return { ok: false, outcome: 'invalid', message: 'operation_conflict' }
      const held = book.pending(op.addonId)
      if (held && held.unknown) {
        const controller = new AbortController()
        controllers.set(jobID, controller)
        patch(jobID, { state: 'running', cancellable: false, error: '', errorKey: '' })
        const addon = op.addonRecord || { id: op.addonId, generation: Number(held.key.split(':')[2]) || 0 }
        return store.follow(api, { jobID, addon, operationId: held.operationId, key: held.key, controller, submit: false })
      }
      let fresh
      try {
        fresh = (await api.installed()).find(a => a.id === op.addonId)
      } catch (e) {
        return { ok: false, outcome: 'unavailable', message: updateErrorKey(e) }
      }
      if (!fresh) return { ok: false, outcome: 'invalid', message: 'stale_installed' }
      return store.start(api, fresh)
    },

    dismiss(jobID) {
      const op = snapshot.live.find(o => o.jobID === jobID)
      if (!op || op.state !== 'failed') return
      settle(jobID, { ...op, outcome: 'failed', state: undefined })
    },

    clearRecords() {
      emit({ ...snapshot, records: [] })
    },

    forAddon(addonId) {
      return snapshot.live.find(op => op.addonId === addonId) || null
    },
  }
  return store
}

export const addonUpgrades = createUpgradeStore()
