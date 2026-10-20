// A read that met a moment of unavailability is tried again a few times with
// short waits: the add-on's process may be restarting, which takes a moment
// and changes nothing. Only a read is ever repeated here. A change is never
// retried by the page: its unknown outcome is shown and repeated by the
// person under the same operation identifier.
export const DELAYS = Object.freeze([400, 1200, 2500])

const transient = (e) => Boolean(e) && (e.kind === 'unavailable' || e.kind === 'network')

// retryRead runs `fn`. On a transient failure it waits and runs it again, up to
// delays.length more times, and gives the last error back. `sleep` and
// `signal` are injected: a withdrawn add-on stops the waiting at once.
export async function retryRead(fn, { delays = DELAYS, sleep = defaultSleep, signal } = {}) {
  let last
  for (let attempt = 0; attempt <= delays.length; attempt++) {
    if (signal && signal.aborted) throw last || Object.assign(new Error('withdrawn'), { code: 'withdrawn' })
    try {
      return await fn()
    } catch (e) {
      last = e
      if (!transient(e) || attempt === delays.length) throw e
      await sleep(delays[attempt], signal)
    }
  }
  throw last
}

function defaultSleep(ms, signal) {
  return new Promise((resolve) => {
    const id = setTimeout(done, ms)
    function done() { if (signal) signal.removeEventListener('abort', done); clearTimeout(id); resolve() }
    if (signal) signal.addEventListener('abort', done, { once: true })
  })
}
