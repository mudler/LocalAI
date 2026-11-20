// Being at the bottom alone must never undo an explicit opt-out.
export function createAutoscroll(pane, onChange, timers = globalThis) {
  let enabled = true, armed = false, timer, previousTop = pane.scrollTop
  const atBottom = () => pane.scrollHeight - pane.clientHeight - pane.scrollTop <= 8
  function cancel() { timers.clearTimeout(timer); timer = undefined }
  function update(value) { enabled = value; onChange(value) }
  function follow() {
    if (!enabled) return
    pane.scrollTop = pane.scrollHeight
    previousTop = pane.scrollTop
  }
  return {
    follow,
    toggle(value) {
      cancel()
      armed = false
      update(value)
      if (value) follow()
    },
    scroll() {
      cancel()
      const bottom = atBottom()
      if (!bottom && pane.scrollTop < previousTop) {
        armed = true
        update(false)
      }
      previousTop = pane.scrollTop
      if (bottom && armed && !enabled) {
        timer = timers.setTimeout(() => {
          if (!armed || !atBottom()) return
          armed = false
          update(true)
          follow()
        }, 250)
      }
    },
    dispose: cancel,
  }
}
