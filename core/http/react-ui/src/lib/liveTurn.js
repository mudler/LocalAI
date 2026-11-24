// Follows one voice turn of the chat from the chat's own flags, so the Live
// loop knows when its reply is ready. Pure: the hook feeds it `running` and
// `failure` on every change and acts on what it returns.
//
// A turn counts as ours only after the message was sent (`arm`). A turn that
// was already running, or one that shows up for a message queued behind it
// (`started: false`), is told apart by the false-to-true edge of `running`.

export function createTurnTracker() {
  let armed = false
  let queued = false
  let seenStart = false
  let prevRunning = false
  let failureAtArm = null
  let lastFailure = null

  // Returns 'started' | { ended: true, failed } | null.
  function check(running, failure) {
    let out = null
    if (armed && !seenStart && running) {
      if (!queued || !prevRunning) {
        seenStart = true
        out = 'started'
      }
    } else if (armed && seenStart && !running) {
      armed = false
      seenStart = false
      out = { ended: true, failed: Boolean(failure) && failure !== failureAtArm }
    }
    prevRunning = running
    lastFailure = failure
    return out
  }

  return {
    // The message was sent. `running` and `failure` are the chat's values now.
    arm({ started, running, failure }) {
      armed = true
      queued = started === false
      seenStart = false
      failureAtArm = failure ?? lastFailure
      if (queued) prevRunning = Boolean(running)
      // The edge of a queued message must not be the state we already see.
      return check(Boolean(running), failure ?? null)
    },
    observe({ running, failure }) {
      return check(Boolean(running), failure ?? null)
    },
    reset() {
      armed = false
      seenStart = false
    },
  }
}
