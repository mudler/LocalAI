// Coalesces streamed text pieces so the chat page renders at most once a
// frame, and at least every `interval` ms (a hidden tab does not fire rAF).
export const FLUSH_MS = 50

export function createTokenBuffer({
  onFlush,
  raf,
  cancelRaf,
  setTimer,
  clearTimer,
  interval = FLUSH_MS,
}) {
  let pending = {}
  let frameHandle = null
  let timerHandle = null

  // Resolve browser globals lazily when not injected.
  const _raf = typeof raf === 'function' ? raf : (() => {
    if (typeof requestAnimationFrame === 'function') return requestAnimationFrame
    return null
  })()
  const _cancelRaf = typeof cancelRaf === 'function' ? cancelRaf : (() => {
    if (typeof cancelAnimationFrame === 'function') return cancelAnimationFrame
    return null
  })()
  const _setTimer = typeof setTimer === 'function' ? setTimer : (() => {
    if (typeof setTimeout === 'function') return setTimeout
    return null
  })()
  const _clearTimer = typeof clearTimer === 'function' ? clearTimer : (() => {
    if (typeof clearTimeout === 'function') return clearTimeout
    return null
  })()

  function scheduleFlush() {
    if (_raf) {
      frameHandle = _raf(flush)
    }
    if (_setTimer) {
      timerHandle = _setTimer(flush, interval)
    }
  }

  function flush() {
    // Reset handles unconditionally; guard only the cancel call.
    if (_cancelRaf && frameHandle !== null) {
      _cancelRaf(frameHandle)
    }
    frameHandle = null

    if (_clearTimer && timerHandle !== null) {
      _clearTimer(timerHandle)
    }
    timerHandle = null

    if (Object.keys(pending).length === 0) return

    const delta = pending
    pending = {}
    onFlush(delta)
  }

  function push(field, text) {
    if (!text) return
    pending[field] = (pending[field] || '') + text

    // Never call onFlush synchronously from push.
    // Schedule both frame and timer only when no flush is already pending.
    if (frameHandle === null && timerHandle === null) {
      scheduleFlush()
    }
  }

  function cancel() {
    if (_cancelRaf && frameHandle !== null) {
      _cancelRaf(frameHandle)
    }
    frameHandle = null

    if (_clearTimer && timerHandle !== null) {
      _clearTimer(timerHandle)
    }
    timerHandle = null

    pending = {}
  }

  function isEmpty() {
    return Object.keys(pending).length === 0
  }

  return { push, flush, cancel, isEmpty }
}
