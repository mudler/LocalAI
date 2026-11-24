// The budget of a page frame that runs without being opened: a tile on Home
// or in a slot. Pure rules, no DOM: PageFrame feeds them and the unit tests
// check them.
//
// The frame's bridge sends a beat about once a second with the time the page
// kept the main thread busy since the last beat (long tasks, and the delay of
// the beat's own timer). The host stops the frame when:
//
//   busy     the page was busy for more than BUSY_MS within the last WINDOW_MS
//   silent   no beat came for SILENT_MS while the frame was in sight (a page
//            in a loop blocks its own timers, or it cleared them)
//   noStart  the first beat did not come within START_MS of the load
//
// A frame out of sight (hidden tab, tile scrolled away) is not watched: the
// browser slows its timers. Its silence clock starts again when it is back.
// The numbers keep a clock or a small chart well inside the budget and stop a
// page that spins: 30% of a core over 10 seconds, or 5 seconds without an
// answer.

export const BEAT_MS = 1000
export const BUSY_MS = 3000
export const WINDOW_MS = 10000
export const SILENT_MS = 5000
export const START_MS = 10000

// The busy time one beat may report: the page cannot have been busy longer
// than the time between two beats plus the window.
const MAX_BEAT_BUSY = WINDOW_MS

export function createFrameWatch({ now = Date.now, busyMs = BUSY_MS, windowMs = WINDOW_MS, silentMs = SILENT_MS, startMs = START_MS } = {}) {
  const loadedAt = now()
  let lastBeat = 0 // 0 until the first beat
  let resumedAt = loadedAt
  let beats = [] // [{at, busy}] within the window
  let stopped = ''

  return {
    // beat records one beat of the frame; busy is the time in ms the page
    // kept the thread busy since its last beat. A value that is not a small
    // non-negative number counts as 0.
    beat(busy) {
      const t = now()
      lastBeat = t
      const b = Number.isFinite(busy) && busy > 0 ? Math.min(busy, MAX_BEAT_BUSY) : 0
      beats.push({ at: t, busy: b })
      beats = beats.filter((x) => t - x.at <= windowMs)
    },
    // resume starts the silence clock again: the frame came back into sight.
    resume() {
      resumedAt = now()
    },
    // check says why the frame must stop, or '' while it keeps to its budget.
    // watching is false while the frame is out of sight. Once a frame is
    // stopped it stays stopped.
    check(watching = true) {
      if (stopped) return stopped
      const t = now()
      const busy = beats.filter((x) => t - x.at <= windowMs).reduce((sum, x) => sum + x.busy, 0)
      if (busy > busyMs) stopped = 'busy'
      else if (!watching) return ''
      else if (lastBeat === 0 && t - Math.max(loadedAt, resumedAt) > startMs) stopped = 'noStart'
      else if (lastBeat !== 0 && t - Math.max(lastBeat, resumedAt) > silentMs) stopped = 'silent'
      return stopped
    },
  }
}
