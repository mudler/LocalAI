// The upload queue of the Files page, without React: what is waiting, what is
// sending with how many bytes, and what finished or failed and why. It lives
// outside the page, so opening another folder or another page of the app does
// not lose an upload. The page draws it and the node unit tests drive it with
// a fake sender.
//
// createUploadQueue({ send, concurrency, now })
//   send(item, { onProgress(loaded), signal }) -> Promise<node>
//     sends one file; it rejects with an Error (status and reason optional)
//   concurrency  how many files go at once (2: enough to fill a link, few
//                enough that the first file finishes first)
//
// An item: { id, name, path, dir, size, file, mode, loaded, status, error,
//   rate, startedAt, finishedAt, result }
//   status   queued | sending | done | failed | canceled
//   mode     keep (the server keeps both on a clash) | replace
//   error    { reason, retry }: the words to show, and whether Try again
//            can help (a file over the limit cannot)

let seq = 0

export function createUploadQueue({ send, concurrency = 2, now = () => Date.now() } = {}) {
  let items = []
  let snapshot = { items, active: 0 }
  const listeners = new Set()
  const settled = new Set()
  const controllers = new Map()

  const publish = () => {
    snapshot = { items, active: items.filter(i => i.status === 'queued' || i.status === 'sending').length }
    listeners.forEach(fn => fn())
  }
  const patch = (id, fields) => {
    items = items.map(i => (i.id === id ? { ...i, ...fields } : i))
  }
  const find = (id) => items.find(i => i.id === id)

  const pump = () => {
    let sending = items.filter(i => i.status === 'sending').length
    for (const item of items) {
      if (sending >= concurrency) break
      if (item.status !== 'queued') continue
      sending++
      start(item)
    }
    publish()
  }

  const start = (item) => {
    const ctrl = new AbortController()
    controllers.set(item.id, ctrl)
    const begun = now()
    patch(item.id, { status: 'sending', loaded: 0, rate: 0, startedAt: begun, error: null })
    let lastAt = begun
    let lastLoaded = 0
    const onProgress = (loaded) => {
      const cur = find(item.id)
      if (!cur || cur.status !== 'sending') return
      const t = now()
      const dt = (t - lastAt) / 1000
      let rate = cur.rate
      // A smoothed rate: one slow chunk must not make the time left jump.
      if (dt >= 0.2) {
        const r = (loaded - lastLoaded) / dt
        rate = rate ? rate * 0.7 + r * 0.3 : r
        lastAt = t
        lastLoaded = loaded
      }
      patch(item.id, { loaded: Math.min(loaded, cur.size || loaded), rate })
      publish()
    }
    Promise.resolve()
      .then(() => send(find(item.id), { onProgress, signal: ctrl.signal }))
      .then((result) => {
        const cur = find(item.id)
        if (!cur || cur.status !== 'sending') return
        patch(item.id, { status: 'done', loaded: cur.size, result, finishedAt: now() })
        settled.forEach(fn => fn(find(item.id)))
      })
      .catch((err) => {
        const cur = find(item.id)
        if (!cur || cur.status !== 'sending') return
        if (ctrl.signal.aborted) {
          patch(item.id, { status: 'canceled', finishedAt: now() })
        } else {
          patch(item.id, { status: 'failed', error: describeError(err), finishedAt: now() })
        }
        settled.forEach(fn => fn(find(item.id)))
      })
      .finally(() => {
        controllers.delete(item.id)
        pump()
      })
  }

  return {
    // add queues files: [{ file, name, path, dir, mode, error }]. An entry with
    // error is refused before sending (too large) and shows as failed.
    add(entries) {
      const added = (entries || []).map(e => ({
        id: `u${++seq}`,
        name: e.name || e.file?.name || '',
        path: e.path,
        dir: e.dir,
        size: e.file?.size || 0,
        file: e.file,
        mode: e.mode || 'keep',
        loaded: 0,
        rate: 0,
        status: e.error ? 'failed' : 'queued',
        error: e.error || null,
        addedAt: now(),
      }))
      items = [...items, ...added]
      pump()
      return added.map(i => i.id)
    },
    cancel(id) {
      const cur = find(id)
      if (!cur) return
      if (cur.status === 'queued') {
        patch(id, { status: 'canceled', finishedAt: now() })
        publish()
      } else if (cur.status === 'sending') {
        controllers.get(id)?.abort()
      }
    },
    cancelAll() {
      for (const i of items) this.cancel(i.id)
    },
    retry(id) {
      const cur = find(id)
      if (!cur || (cur.status !== 'failed' && cur.status !== 'canceled') || cur.error?.retry === false) return
      patch(id, { status: 'queued', error: null, loaded: 0, rate: 0 })
      pump()
    },
    retryAll() {
      for (const i of items) this.retry(i.id)
    },
    // dismiss removes finished items: one by id, or every finished one.
    dismiss(id) {
      const over = (i) => i.status === 'done' || i.status === 'failed' || i.status === 'canceled'
      items = items.filter(i => (id ? i.id !== id || !over(i) : !over(i)))
      publish()
    },
    getSnapshot: () => snapshot,
    subscribe(fn) {
      listeners.add(fn)
      return () => listeners.delete(fn)
    },
    // onSettled calls fn with each item that finished, failed or was canceled.
    onSettled(fn) {
      settled.add(fn)
      return () => settled.delete(fn)
    },
  }
}

// describeError turns a failed send into the words of the tray and says
// whether Try again can help.
export function describeError(err) {
  const status = err?.status || 0
  if (status === 413) return { key: 'uploads.errors.tooLarge', reason: err?.message || '', retry: false }
  if (status === 409 && err?.type === 'files_off') return { key: 'uploads.errors.off', reason: err?.message || '', retry: false }
  if (status === 400) return { key: 'uploads.errors.invalid', reason: err?.message || '', retry: false }
  if (status === 0) return { key: 'uploads.errors.network', reason: '', retry: true }
  return { key: 'uploads.errors.server', reason: err?.message || '', retry: true }
}

// summarize is the head line of the tray: how many files, how many bytes of
// how many, and the time left from the current rate.
export function summarize(items, now = Date.now()) {
  const live = items.filter(i => i.status !== 'canceled')
  const pending = live.filter(i => i.status === 'queued' || i.status === 'sending')
  const total = live.reduce((n, i) => n + (i.size || 0), 0)
  const loaded = live.reduce((n, i) => n + (i.status === 'done' ? i.size || 0 : i.loaded || 0), 0)
  const rate = live.filter(i => i.status === 'sending').reduce((n, i) => n + (i.rate || 0), 0)
  const left = pending.reduce((n, i) => n + Math.max(0, (i.size || 0) - (i.loaded || 0)), 0)
  const sendingSince = Math.min(...live.filter(i => i.status === 'sending').map(i => i.startedAt ?? now), now)
  return {
    count: live.length,
    done: live.filter(i => i.status === 'done').length,
    failed: live.filter(i => i.status === 'failed').length,
    pending: pending.length,
    total,
    loaded,
    percent: total > 0 ? Math.min(100, Math.round((loaded / total) * 100)) : (pending.length ? 0 : 100),
    rate,
    // Only worth saying once the upload has run for a moment and will take a while.
    eta: rate > 0 && now - sendingSince > 1500 && left / rate > 3 ? Math.ceil(left / rate) : null,
  }
}

// The time left in words, in the reader's language: "12s", "3m", "1h 5m"
// in English. The tray puts it in "… left".
export function formatDuration(secs, locale) {
  if (!Number.isFinite(secs) || secs < 0) return ''
  const unit = (n, u) => new Intl.NumberFormat(locale, { style: 'unit', unit: u, unitDisplay: 'narrow' }).format(n)
  if (secs < 60) return unit(Math.max(1, Math.round(secs)), 'second')
  const mins = Math.round(secs / 60)
  if (mins < 60) return unit(mins, 'minute')
  return `${unit(Math.floor(mins / 60), 'hour')} ${unit(mins % 60, 'minute')}`
}
