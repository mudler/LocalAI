import { spawn, type ChildProcess } from 'node:child_process'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  BACKOFF_MAX_MS,
  BACKOFF_REFUSED_MS,
  BACKOFF_START_MS,
  backoffDelay,
  EVENTS_PATH,
  formatNotification,
  Notifier,
  parseItem,
  resolveLink,
  ServerStream,
  shouldSuppress,
  SseParser,
  type InboxItem,
  type NotifierDeps,
  type NotifyTarget,
} from '../src/main/notifications'
import { unreadTooltip } from '../src/main/trayPolicy'
import { applySettings, toSettings } from '../src/main/settings'
import { desktopNotifications, withDesktopNotifications } from '../src/main/configStore'
import { defaultConfig } from '../src/main/config'
import { FAKE_SERVER, freePort } from './helpers'

const item = (over: Partial<InboxItem> = {}): InboxItem => ({ id: 'n1', kind: 'run_ended', status: 'ok', source: 'trigger', title: 'Run finished', body: 'It took 3 s.', link: '/app/chats/c1', read: false, created: '2026-11-18T10:00:00Z', ...over })
const frame = (i: InboxItem | Record<string, unknown>) => `id: x\nevent: notification\ndata: ${JSON.stringify(i)}\n\n`

describe('SseParser', () => {
  it('reads a notification and a change event, and skips pings and other events', () => {
    const p = new SseParser()
    const out = p.push(`: ping\n\n${frame(item())}event: notifications_changed\ndata: {}\n\nevent: other\ndata: {}\n\n`)
    expect(out).toEqual([{ type: 'notification', item: item() }, { type: 'changed' }])
  })

  it('reads an event split at any byte, and CRLF line ends', () => {
    const text = frame(item({ title: 'Ünï cödé' })).replace(/\n/g, '\r\n')
    for (let cut = 1; cut < text.length; cut++) {
      const p = new SseParser()
      const out = [...p.push(text.slice(0, cut)), ...p.push(text.slice(cut))]
      expect(out, `cut ${cut}`).toEqual([{ type: 'notification', item: item({ title: 'Ünï cödé' }) }])
    }
  })

  it('joins the data lines of one event', () => {
    expect(new SseParser().push(`event: notification\ndata: {"id":"a",\ndata: "title":"t"}\n\n`)).toMatchObject([{ type: 'notification', item: { id: 'a', title: 't' } }])
  })

  it('drops bad JSON and items without an id or a title, and keeps going', () => {
    const p = new SseParser()
    const out = p.push(`event: notification\ndata: {oops\n\n${frame({ id: '', title: 't' })}${frame({ id: 'a' })}${frame(item({ id: 'ok' }))}`)
    expect(out.map((e) => (e.type === 'notification' ? e.item.id : e.type))).toEqual(['ok'])
  })

  it('cuts long text and ignores a field that is not text', () => {
    const i = parseItem({ id: 'a', title: 'x'.repeat(5000), body: 7, read: 'yes', link: 'l'.repeat(900) })
    expect(i?.title).toHaveLength(500)
    expect(i).toMatchObject({ body: '', read: false })
    expect(i?.link).toHaveLength(300)
  })

  it('gives up on an event that never ends', () => {
    const p = new SseParser()
    expect(p.push('data: ' + 'x'.repeat(SseParser.MAX_PENDING + 10))).toEqual([])
    expect(p.push(`\n${frame(item())}`)).toHaveLength(1)
  })
})

describe('backoffDelay', () => {
  it('doubles from one second to a minute, with at most 20 % jitter', () => {
    const none = () => 0
    expect([0, 1, 2, 3, 4, 5, 6, 7, 30].map((n) => backoffDelay(n, none))).toEqual([1000, 2000, 4000, 8000, 16000, 32000, 60000, 60000, 60000])
    expect(backoffDelay(0, () => 1)).toBe(1200)
    expect(backoffDelay(10, () => 1)).toBe(72000)
    expect(backoffDelay(-3, none)).toBe(BACKOFF_START_MS)
    expect(BACKOFF_MAX_MS).toBe(60_000)
  })
})

describe('resolveLink', () => {
  const O = 'https://srv.example.test:8443'
  it('takes an in-app path on the origin', () => {
    expect(resolveLink(O, '/app/chats/c1')).toBe(`${O}/app/chats/c1`)
    expect(resolveLink(O, '/app/updates?x=1#top')).toBe(`${O}/app/updates?x=1#top`)
  })
  it('refuses everything that could leave the origin', () => {
    for (const l of ['', 'app/chats', 'https://evil.example.test/', `${O}/app`, '//evil.example.test/app', '/\\evil.example.test', '/\\/evil.example.test', 'javascript:alert(1)', 'file:///etc/passwd', '/app/../../x', '/app/%2e%2E/x', '/a\nb', '/a\u0000b', '\t/app', ' /app']) {
      expect(resolveLink(O, l), JSON.stringify(l)).toBeUndefined()
    }
  })
  it('refuses a server origin that is not an http origin', () => {
    expect(resolveLink('file:///x', '/app')).toBeUndefined()
    expect(resolveLink('https://u:p@srv.example.test', '/app')).toBeUndefined()
  })
})

describe('shouldSuppress', () => {
  const O = 'http://127.0.0.1:8080'
  const f = { windowFocused: true, webuiShown: true, currentUrl: `${O}/app/chats/c1` }
  it('hides a notification for the chat that is open in the focused window', () => {
    expect(shouldSuppress(f, O, '/app/chats/c1')).toBe(true)
    expect(shouldSuppress({ ...f, currentUrl: `${O}/app/chats/c1/?m=2#x` }, O, '/app/chats/c1')).toBe(true)
  })
  it('shows it in every other case', () => {
    expect(shouldSuppress({ ...f, windowFocused: false }, O, '/app/chats/c1')).toBe(false)
    expect(shouldSuppress({ ...f, webuiShown: false }, O, '/app/chats/c1')).toBe(false)
    expect(shouldSuppress({ ...f, currentUrl: undefined }, O, '/app/chats/c1')).toBe(false)
    expect(shouldSuppress(f, O, '/app/chats/c2')).toBe(false)
    expect(shouldSuppress(f, O, '')).toBe(false)
    expect(shouldSuppress(f, O, 'https://evil.example.test/app/chats/c1')).toBe(false)
    expect(shouldSuppress({ ...f, currentUrl: 'http://other.example.test/app/chats/c1' }, O, '/app/chats/c1')).toBe(false)
    expect(shouldSuppress({ ...f, currentUrl: 'not a url' }, O, '/app/chats/c1')).toBe(false)
  })
})

describe('formatNotification', () => {
  it('names the server in the title only when more than one is configured', () => {
    expect(formatNotification('Office', item(), false)).toEqual({ title: 'Run finished', body: 'It took 3 s.' })
    expect(formatNotification('Office', item(), true)).toEqual({ title: 'Office: Run finished', body: 'It took 3 s.' })
  })
  it('tooltip shows the unread count', () => {
    expect(unreadTooltip(0)).toBe('LocalAI Launcher')
    expect(unreadTooltip(3)).toBe('LocalAI Launcher (3 unread)')
  })
})

// ---- streams, with a fake fetch and fake timers ----------------------------------------

function streamOf() {
  const enc = new TextEncoder()
  let ctl!: ReadableStreamDefaultController<Uint8Array>
  const body = new ReadableStream<Uint8Array>({ start: (c) => void (ctl = c) })
  return { body, push: (t: string) => ctl.enqueue(enc.encode(t)), end: () => ctl.close(), fail: () => ctl.error(new Error('reset')) }
}

type Handler = (url: string, init: RequestInit) => Response | Promise<Response>
function rig(handler: Handler) {
  const calls: { url: string; headers: Record<string, string> }[] = []
  const seen: { target: NotifyTarget; item: InboxItem }[] = []
  const totals: number[] = []
  const unread: [string, number][] = []
  const fetchFn = (async (url: string, init: RequestInit) => {
    calls.push({ url, headers: init.headers as Record<string, string> })
    return handler(url, init)
  }) as unknown as typeof fetch
  const deps: NotifierDeps = {
    fetch: fetchFn,
    setTimeout: (fn, ms) => setTimeout(fn, ms),
    clearTimeout: (t) => clearTimeout(t as NodeJS.Timeout),
    random: () => 0,
    onNotification: (target, i) => seen.push({ target, item: i }),
    onUnread: (id, n) => unread.push([id, n]),
  }
  const notifier = new Notifier({ ...deps, onTotal: (n) => totals.push(n) })
  return { calls, seen, totals, unread, deps, notifier }
}
const T: NotifyTarget = { id: 'local', name: 'This computer', origin: 'http://127.0.0.1:8080', headers: () => ({ authorization: 'Bearer k1' }) }
const json = (o: unknown) => new Response(JSON.stringify(o), { headers: { 'content-type': 'application/json' } })
const flush = () => vi.advanceTimersByTimeAsync(0)

describe('ServerStream', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('signs in with the target headers, reads events and the unread count', async () => {
    const s = streamOf()
    const r = rig((url) => (url.endsWith(EVENTS_PATH) ? new Response(s.body) : json({ unread: 2 })))
    r.notifier.sync([T])
    await flush()
    expect(r.calls[0]).toMatchObject({ url: `${T.origin}${EVENTS_PATH}`, headers: { authorization: 'Bearer k1', accept: 'text/event-stream' } })
    expect(r.notifier.stateOf('local')).toBe('live')
    expect(r.totals).toEqual([2])
    s.push(frame(item()))
    await flush()
    expect(r.seen.map((x) => x.item.id)).toEqual(['n1'])
    // The same id again is shown once.
    s.push(frame(item()))
    await flush()
    expect(r.seen).toHaveLength(1)
    s.push('event: notifications_changed\ndata: {}\n\n')
    await flush()
    expect(r.calls.filter((c) => c.url.endsWith('/count')).length).toBeGreaterThanOrEqual(2)
  })

  it('reconnects after the stream ends or fails, with growing waits, and starts again from the first wait once live', async () => {
    const bodies = [streamOf(), streamOf(), streamOf()]
    let n = 0
    const r = rig((url) => {
      if (url.endsWith('/count')) return json({ unread: 0 })
      const b = bodies[n++]
      return b ? new Response(b.body) : new Response('', { status: 502 })
    })
    r.notifier.sync([T])
    await flush()
    bodies[0].end()
    await flush()
    expect(r.notifier.stateOf('local')).toBe('waiting')
    await vi.advanceTimersByTimeAsync(BACKOFF_START_MS - 1)
    expect(n).toBe(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(n).toBe(2)
    // It was live in between, so the wait is one second again.
    bodies[1].fail()
    await flush()
    await vi.advanceTimersByTimeAsync(BACKOFF_START_MS)
    expect(n).toBe(3)
    bodies[2].end()
    await flush()
    // Now the server answers 502 each time: 1 s, 2 s, 4 s ...
    await vi.advanceTimersByTimeAsync(1000)
    expect(n).toBe(4)
    await vi.advanceTimersByTimeAsync(1999)
    expect(r.calls.filter((c) => c.url.endsWith(EVENTS_PATH))).toHaveLength(4)
    await vi.advanceTimersByTimeAsync(1)
    expect(r.calls.filter((c) => c.url.endsWith(EVENTS_PATH))).toHaveLength(5)
  })

  it('looks only rarely when the server refuses the key or has no inbox', async () => {
    const r = rig(() => new Response('', { status: 401 }))
    r.notifier.sync([T])
    await flush()
    expect(r.calls).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(BACKOFF_REFUSED_MS - 1)
    expect(r.calls).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(r.calls).toHaveLength(2)
  })

  it('stops when the server is removed, and counts it as zero unread', async () => {
    const s = streamOf()
    const r = rig((url) => (url.endsWith(EVENTS_PATH) ? new Response(s.body) : json({ unread: 4 })))
    r.notifier.sync([T])
    await flush()
    expect(r.notifier.total()).toBe(4)
    r.notifier.sync([])
    expect(r.notifier.count).toBe(0)
    expect(r.notifier.total()).toBe(0)
    s.push(frame(item()))
    await vi.advanceTimersByTimeAsync(120_000)
    expect(r.seen).toEqual([])
    expect(r.calls.filter((c) => c.url.endsWith(EVENTS_PATH))).toHaveLength(1)
  })

  it('keeps one stream for each server, with its own identity, and sums the unread counts', async () => {
    const r = rig((url) => (url.includes('a.example') ? (url.endsWith('/count') ? json({ unread: 1 }) : new Response(streamOf().body)) : url.endsWith('/count') ? json({ unread: 5 }) : new Response(streamOf().body)))
    const A: NotifyTarget = { id: 'srv_a', name: 'A', origin: 'https://a.example.test' }
    const B: NotifyTarget = { id: 'srv_b', name: 'B', origin: 'https://b.example.test' }
    r.notifier.sync([T, A, B])
    await flush()
    expect(r.notifier.count).toBe(3)
    expect(r.notifier.multiple).toBe(true)
    expect(r.notifier.total()).toBe(5 + 1 + 5)
    // Same ids again: no new connections.
    const before = r.calls.length
    r.notifier.sync([T, A, B])
    await flush()
    expect(r.calls.length).toBe(before)
    // An origin that changed restarts that one stream only.
    r.notifier.sync([T, { ...A, origin: 'https://a2.example.test' }, B])
    await flush()
    expect(r.calls.slice(before).map((c) => c.url)).toEqual([`https://a2.example.test${EVENTS_PATH}`, 'https://a2.example.test/api/chats/notifications/count'])
  })

  it('uses the fetch of the target when it has one', async () => {
    const own = vi.fn(async () => new Response('', { status: 404 }))
    const r = rig(() => new Response('', { status: 500 }))
    r.notifier.sync([{ ...T, fetch: own as unknown as typeof fetch }])
    await flush()
    expect(own).toHaveBeenCalledTimes(1)
    expect(r.calls).toHaveLength(0)
  })

  it('does not follow a redirect to another origin', async () => {
    let redirect: RequestInit['redirect']
    const r = rig((_u, init) => {
      redirect = init.redirect
      return new Response('', { status: 500 })
    })
    r.notifier.sync([T])
    await flush()
    expect(redirect).toBe('error')
  })

  it('the setting off stops every stream and on starts them again', async () => {
    const r = rig((url) => (url.endsWith('/count') ? json({ unread: 1 }) : new Response(streamOf().body)))
    r.notifier.sync([T])
    await flush()
    r.notifier.setEnabled(false)
    expect(r.notifier.count).toBe(0)
    expect(r.notifier.total()).toBe(0)
    r.notifier.sync([T])
    expect(r.notifier.count).toBe(0)
    r.notifier.setEnabled(true)
    await flush()
    expect(r.notifier.count).toBe(1)
    expect(r.notifier.stateOf('local')).toBe('live')
  })

  it('a stream can be built alone', async () => {
    const r = rig(() => new Response('', { status: 500 }))
    const s = new ServerStream({ ...T }, r.deps)
    s.start()
    await flush()
    expect(s.state).toBe('waiting')
    s.stop()
    expect(s.state).toBe('stopped')
  })
})

// ---- the setting ---------------------------------------------------------------------

describe('the "Desktop notifications" setting', () => {
  it('is on when the config does not say, and a saved choice is kept', () => {
    const c = defaultConfig()
    expect(desktopNotifications(c)).toBe(true)
    const off = applySettings(withDesktopNotifications(c, false), { showWelcome: true })
    expect(desktopNotifications(off)).toBe(false)
    expect(off.extra.desktop_notifications).toBe(false)
    expect(desktopNotifications(withDesktopNotifications(off, true))).toBe(true)
    expect(toSettings(off)).toBeDefined()
  })
  it('is on for a hand-edited value that is not a boolean', () => {
    const c = defaultConfig()
    expect(desktopNotifications({ ...c, extra: { desktop_notifications: 'no' } })).toBe(true)
  })
})

// ---- against the fake server fixture ---------------------------------------------------

describe('against the fake server', () => {
  let child: ChildProcess | undefined
  let origin = ''
  afterEach(() => {
    child?.kill('SIGKILL')
    child = undefined
  })

  async function start(env: Record<string, string> = {}) {
    const port = await freePort()
    origin = `http://127.0.0.1:${port}`
    child = spawn(process.execPath, [FAKE_SERVER], { env: { ...process.env, FAKE_PORT: String(port), ...env }, stdio: 'ignore' })
    for (let i = 0; i < 100; i++) {
      try {
        if ((await fetch(`${origin}/readyz`)).ok) return
      } catch {
        /* not up yet */
      }
      await new Promise((r) => setTimeout(r, 50))
    }
    throw new Error('the fake server did not start')
  }
  const emit = (body: object) => fetch(`${origin}/__emit`, { method: 'POST', body: JSON.stringify(body) })
  const stats = async () => (await (await fetch(`${origin}/__stats`)).json()) as { streams: number; connections: number }
  const until = async (cond: () => boolean | Promise<boolean>, ms = 10_000) => {
    const end = Date.now() + ms
    while (!(await cond())) {
      if (Date.now() > end) throw new Error('timed out')
      await new Promise((r) => setTimeout(r, 20))
    }
  }

  it('gets a notification and the count with the launcher key, and reconnects when the stream is cut', async () => {
    await start({ FAKE_NOTIFY_KEY: 'lai-secret' })
    const seen: InboxItem[] = []
    const totals: number[] = []
    const n = new Notifier({
      fetch,
      setTimeout: (fn, ms) => setTimeout(fn, ms),
      clearTimeout: (t) => clearTimeout(t as NodeJS.Timeout),
      random: () => 0,
      onNotification: (_t, i) => seen.push(i),
      onTotal: (x) => totals.push(x),
    })
    n.sync([{ id: 'local', name: 'This computer', origin, headers: () => ({ authorization: 'Bearer lai-secret' }) }])
    await until(async () => (await stats()).streams === 1)
    await emit({ event: 'notification', data: item({ id: 'a', title: 'Hello' }), unread: 1 })
    await until(() => seen.length === 1 && n.total() === 1)
    expect(seen[0]).toMatchObject({ id: 'a', title: 'Hello', link: '/app/chats/c1' })
    await emit({ event: 'notifications_changed', unread: 0 })
    await until(() => n.total() === 0)
    // A frame split in two writes still gives one event.
    const whole = frame(item({ id: 'b' }))
    await emit({ raw: whole.slice(0, 12) })
    await emit({ raw: whole.slice(12) })
    await until(() => seen.length === 2)
    // Stopping the notifier closes the stream on the server too.
    n.stopAll()
    await until(async () => (await stats()).streams === 0)
  })

  it('does not get past a wrong key, and keeps no stream open', async () => {
    await start({ FAKE_NOTIFY_KEY: 'lai-secret' })
    const n = new Notifier({ fetch, setTimeout: (fn, ms) => setTimeout(fn, ms), clearTimeout: (t) => clearTimeout(t as NodeJS.Timeout), onNotification: () => {}, onTotal: () => {} })
    n.sync([{ id: 'local', name: 'x', origin, headers: () => ({ authorization: 'Bearer wrong' }) }])
    await until(() => n.stateOf('local') === 'waiting')
    expect((await stats()).streams).toBe(0)
    n.stopAll()
  })
})
