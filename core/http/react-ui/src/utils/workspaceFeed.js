// The change feed of the user's files (GET /api/workspace/events), shared by
// every page of one tab. It has no React in it, so the node unit tests can drive
// it with a fake EventSource and fake timers.
//
// One EventSource per tab, opened by the first subscriber and closed after the
// last one leaves. A subscriber names path globs and gets one call per burst of
// matching changes. A "resync" of the server, a reconnect that could not resume,
// the window getting the focus again and the end of a chat turn call every
// subscriber once with resync set: the stream may have missed something, so a
// subscriber reads again what it shows.

import { retryDelay } from './streamRetry.js'

export const DEBOUNCE_MS = 150
export const MAX_WAIT_MS = 1000
const RECONNECT_MS = 1000 // first wait; it doubles up to 30 s
const FOCUS_GAP_MS = 1000 // focus and visibility often fire together
const OWN_WRITE_MS = 10000 // how long an invalidate without a revision waits for its echo

// globToRegExp turns a path glob into a RegExp. "*" matches inside one path
// segment, "**" any number of segments, "?" one character. Paths have no
// leading slash ("pages/x/index.html").
export function globToRegExp(glob) {
  const g = String(glob || '').replace(/^\/+/, '')
  let re = ''
  for (let i = 0; i < g.length; i++) {
    const c = g[i]
    if (c === '*' && g[i + 1] === '*') {
      const before = i === 0 || g[i - 1] === '/'
      const after = g[i + 2] === '/' || i + 2 === g.length
      if (before && after) {
        if (g[i + 2] === '/') {
          re += '(?:.*/)?' // "**/" also matches no folder at all
          i += 2
        } else {
          // A trailing "/**" matches the folder itself and anything below it.
          if (re.endsWith('/')) re = re.slice(0, -1) + '(?:/.*)?'
          else re += '.*'
          i += 1
        }
        continue
      }
      re += '[^/]*'
      i += 1
      continue
    }
    if (c === '*') re += '[^/]*'
    else if (c === '?') re += '[^/]'
    else re += c.replace(/[.+^${}()|[\]\\]/g, '\\$&')
  }
  return new RegExp(`^${re}$`)
}

export function normalizePath(p) {
  return String(p || '').replace(/^\/+/, '')
}

// createWorkspaceFeed returns a feed. Every option has a browser default; the
// tests pass fakes.
export function createWorkspaceFeed({
  url = '/api/workspace/events',
  EventSourceImpl = typeof EventSource === 'undefined' ? null : EventSource,
  target = typeof window === 'undefined' ? null : window,
  doc = typeof document === 'undefined' ? null : document,
  setTimer = (fn, ms) => setTimeout(fn, ms),
  clearTimer = (t) => clearTimeout(t),
  now = () => Date.now(),
  debounceMs = DEBOUNCE_MS,
  maxWaitMs = MAX_WAIT_MS,
} = {}) {
  const subs = new Set()
  const own = new Map() // path -> {rev, until}: changes this tab made itself
  let source = null
  let retryTimer = null
  let attempt = 0
  let revision = null // the newest revision this tab was told about
  let lastNudge = -Infinity

  const flush = (s) => {
    s.timer = null
    const ev = { paths: [...s.paths].sort(), revision: s.revision }
    if (s.resync) ev.resync = true
    s.paths.clear()
    s.resync = false
    if (!subs.has(s)) return
    try {
      s.onChange(ev)
    } catch (err) {
      console.error('workspace feed subscriber failed', err)
    }
  }

  // queue adds changes to a subscriber's next call. The wait restarts with
  // each change (trailing debounce), but never past maxWaitMs from the first
  // one, so a steady stream of writes still refreshes the page.
  const queue = (s, paths, rev, resync) => {
    for (const p of paths) s.paths.add(p)
    if (resync) s.resync = true
    if (rev != null) s.revision = rev
    const t = now()
    if (s.timer == null) s.first = t
    else clearTimer(s.timer)
    const wait = Math.max(0, Math.min(debounceMs, s.first + maxWaitMs - t))
    s.timer = setTimer(() => flush(s), wait)
  }

  // dropOwn removes the paths whose change this tab made and already shows.
  const dropOwn = (paths, rev) => {
    const t = now()
    return paths.filter(p => {
      const o = own.get(p)
      if (!o) return true
      if (o.rev != null) {
        if (rev != null && rev <= o.rev) return false
        own.delete(p)
        return true
      }
      own.delete(p)
      return o.until < t // an echo that came too late counts as a new change
    })
  }

  const onChangeEvent = (data) => {
    if (!data || typeof data.revision !== 'number') return
    revision = Math.max(revision ?? 0, data.revision)
    const paths = dropOwn((data.paths || []).map(normalizePath), data.revision)
    if (paths.length === 0) return
    for (const s of subs) {
      const hit = paths.filter(p => s.patterns.some(re => re.test(p)))
      if (hit.length > 0) queue(s, hit, data.revision, false)
    }
  }

  const resyncAll = () => {
    for (const s of subs) queue(s, [], revision, true)
  }

  const parse = (e) => {
    try {
      return JSON.parse(e.data)
    } catch {
      return null
    }
  }

  const connect = () => {
    retryTimer = null
    if (!EventSourceImpl || subs.size === 0) return
    const resumed = revision != null
    const u = resumed ? `${url}${url.includes('?') ? '&' : '?'}after=${revision}` : url
    const es = new EventSourceImpl(u)
    source = es
    es.addEventListener('change', (e) => onChangeEvent(parse(e)))
    es.addEventListener('resync', (e) => {
      const d = parse(e)
      if (d && typeof d.revision === 'number') revision = Math.max(revision ?? 0, d.revision)
      resyncAll()
    })
    es.onopen = () => {
      // A stream that starts from now may have missed what happened while it
      // was down; one that resumed gets those changes from the server.
      if (attempt > 0 && !resumed) resyncAll()
      attempt = 0
    }
    es.onerror = () => {
      es.close()
      if (source === es) source = null
      if (subs.size === 0) return
      retryTimer = setTimer(connect, retryDelay(attempt++, RECONNECT_MS))
    }
  }

  const nudge = () => {
    const t = now()
    if (t - lastNudge < FOCUS_GAP_MS) return
    lastNudge = t
    resyncAll()
  }
  const onVisible = () => { if (!doc || doc.visibilityState === 'visible') nudge() }

  const start = () => {
    target?.addEventListener?.('focus', nudge)
    doc?.addEventListener?.('visibilitychange', onVisible)
    connect()
  }

  const stop = () => {
    target?.removeEventListener?.('focus', nudge)
    doc?.removeEventListener?.('visibilitychange', onVisible)
    if (retryTimer != null) clearTimer(retryTimer)
    retryTimer = null
    source?.close()
    source = null
    attempt = 0
  }

  return {
    // subscribe calls onChange({paths, revision, resync?}) for the changes of
    // paths that match one of the globs, debounced. It returns the unsubscribe.
    subscribe(patterns, onChange) {
      const s = {
        patterns: (patterns || []).map(globToRegExp),
        onChange,
        paths: new Set(),
        revision: null,
        resync: false,
        timer: null,
        first: 0,
      }
      subs.add(s)
      if (subs.size === 1) start()
      return () => {
        if (!subs.delete(s)) return
        if (s.timer != null) clearTimer(s.timer)
        if (subs.size === 0) stop()
      }
    },
    // invalidate says that this tab wrote path itself and already shows the
    // new content, so its echo on the feed calls nobody. With a revision, every
    // change up to it is dropped; without one, the next change within ten
    // seconds is.
    invalidate(path, rev) {
      own.set(normalizePath(path), { rev: typeof rev === 'number' ? rev : null, until: now() + OWN_WRITE_MS })
    },
    // turnEnded is the fallback at the end of a chat turn, when the tools of
    // the turn may have written files.
    turnEnded() {
      lastNudge = -Infinity
      nudge()
    },
    get size() { return subs.size },
    get connected() { return source != null },
  }
}
