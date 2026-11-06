// Automatic resume of a turn that a restart of the server interrupted.
//
// The server marks such a turn "interrupted" and keeps the transcript; the
// resume call goes on from the last finished step. The page asks for that
// resume by itself a few times, with a growing wait, so a rolling restart does
// not leave a long chat stopped. Only an interrupted turn is resumed this way:
// a turn that failed (a model error, a bad key) needs a person to look first.
import { retryDelay } from './streamRetry.js'

// How many times the page resumes one interrupted turn by itself. Change it
// here; the card says "try n of N".
export const RESUME_MAX_ATTEMPTS = 3
export const RESUME_BASE_MS = 3000
export const RESUME_CAP_MS = 30000
// An interruption older than this is news from the past (a chat opened later):
// it waits for the person and is not resumed by the page.
export const RESUME_WINDOW_MS = 10 * 60 * 1000

// The wait before automatic try number `attempt` (0 for the first).
export function resumeWait(attempt) {
  return retryDelay(attempt, RESUME_BASE_MS, RESUME_CAP_MS)
}

// What to do about the chat as the page sees it now:
//   'none'      nothing: not interrupted, running, stopped by the person, or old
//   'wait'      resume after the wait
//   'exhausted' the tries are used; the person decides
export function nextResume({ lastTurn, running, attempts = 0, stopped = false, updated, now = Date.now(), max = RESUME_MAX_ATTEMPTS }) {
  if (lastTurn !== 'interrupted' || running || stopped) return 'none'
  const at = Date.parse(updated || '')
  if (Number.isFinite(at) && now - at > RESUME_WINDOW_MS) return 'none'
  return attempts >= max ? 'exhausted' : 'wait'
}

// The state machine behind the hook. It holds the tries per chat, one timer,
// and what the card shows: phase is 'idle', 'waiting', 'resuming' or
// 'exhausted'. observe() is called each time the chat's state changes and is
// safe to call again with the same state. stop() is the person saying no.
export function createResumer({
  resume,
  onChange = () => {},
  max = RESUME_MAX_ATTEMPTS,
  wait = resumeWait,
  schedule = setTimeout,
  cancel = clearTimeout,
  now = Date.now,
} = {}) {
  const attempts = new Map()
  const stopped = new Set()
  let timer = null
  let chat = null
  let view = { phase: 'idle', attempt: 0, max, seconds: 0 }

  const set = (next) => {
    if (next.phase === view.phase && next.attempt === view.attempt && next.seconds === view.seconds) return
    view = { max, ...next }
    onChange(view)
  }
  const clear = () => {
    if (timer !== null) cancel(timer)
    timer = null
  }

  return {
    get view() { return view },
    observe(state) {
      if (state.id !== chat) {
        clear()
        chat = state.id
        set({ phase: 'idle', attempt: 0, seconds: 0 })
      }
      const id = state.id
      if (!id) return
      if (state.running) {
        clear()
        stopped.delete(id)
        set({ phase: 'idle', attempt: 0, seconds: 0 })
        return
      }
      if (state.lastTurn === 'done') attempts.delete(id)
      if (state.resuming) {
        set({ phase: 'resuming', attempt: attempts.get(id) || 0, seconds: 0 })
        return
      }
      const tried = attempts.get(id) || 0
      const next = nextResume({
        lastTurn: state.lastTurn, running: false, attempts: tried, stopped: stopped.has(id), updated: state.updated, now: now(), max,
      })
      if (next === 'exhausted') {
        clear()
        set({ phase: 'exhausted', attempt: tried, seconds: 0 })
        return
      }
      if (next === 'none') {
        clear()
        set({ phase: 'idle', attempt: 0, seconds: 0 })
        return
      }
      if (timer !== null) return
      const ms = wait(tried)
      set({ phase: 'waiting', attempt: tried + 1, seconds: Math.round(ms / 1000) })
      timer = schedule(() => {
        timer = null
        attempts.set(id, tried + 1)
        set({ phase: 'resuming', attempt: tried + 1, seconds: 0 })
        Promise.resolve(resume(id)).catch(() => {})
      }, ms)
    },
    stop() {
      clear()
      if (chat) stopped.add(chat)
      set({ phase: 'idle', attempt: 0, seconds: 0 })
    },
    // A Retry of the person starts the count again.
    reset() {
      if (!chat) return
      attempts.delete(chat)
      stopped.delete(chat)
    },
    dispose: clear,
  }
}
