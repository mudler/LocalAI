// Desktop notifications from the server's notification inbox. No Electron
// import: notifyGlue.ts shows the notifications, this file decides and tests
// without a window.
//
// Each server (the local one and every saved remote one) has one stream:
// `GET /api/chats/notifications/events`, server-sent events. The stream is
// live only. A `notification` event is a new inbox item; a
// `notifications_changed` event means items were read or deleted, so the
// unread count is read again from `/api/chats/notifications/count`.
import { httpOrigin } from './webuiPolicy'

export const EVENTS_PATH = '/api/chats/notifications/events'
export const COUNT_PATH = '/api/chats/notifications/count'

// ---- events ------------------------------------------------------------------------

export interface InboxItem {
  id: string
  kind: string
  status: string
  source: string
  title: string
  body: string
  link: string
  read: boolean
  created: string
}

export type StreamEvent = { type: 'notification'; item: InboxItem } | { type: 'changed' }

export const MAX_TEXT = 500

const str = (v: unknown, max = MAX_TEXT): string => (typeof v === 'string' ? v.slice(0, max) : '')

/** An inbox item from the JSON of a `notification` event, or undefined when it has no id or title. */
export function parseItem(data: unknown): InboxItem | undefined {
  if (typeof data !== 'object' || data === null) return undefined
  const o = data as Record<string, unknown>
  const id = str(o.id, 200)
  const title = str(o.title)
  if (id === '' || title === '') return undefined
  return { id, kind: str(o.kind, 80), status: str(o.status, 80), source: str(o.source, 80), title, body: str(o.body), link: str(o.link, 300), read: o.read === true, created: str(o.created, 80) }
}

/**
 * Reads server-sent events from chunks of text, in any split. Comment lines
 * (the server's `: ping`) and events of other names are dropped; a bad JSON
 * body is dropped too. Lines end with LF, CR or CRLF.
 */
export class SseParser {
  private buf = ''
  private name = ''
  private data: string[] = []
  private skipLf = false
  /** Bytes of an unfinished event the parser keeps before it gives up on it. */
  static readonly MAX_PENDING = 1 << 20

  push(chunk: string): StreamEvent[] {
    const out: StreamEvent[] = []
    let text = chunk
    if (this.skipLf && text.startsWith('\n')) text = text.slice(1)
    this.skipLf = false
    this.buf += text
    for (;;) {
      const m = /\r\n|\n|\r/.exec(this.buf)
      if (!m) break
      // A CR at the very end may be the first half of a CRLF: wait for the next chunk.
      if (m[0] === '\r' && m.index === this.buf.length - 1) {
        this.skipLf = true
      }
      const line = this.buf.slice(0, m.index)
      this.buf = this.buf.slice(m.index + m[0].length)
      const ev = this.line(line)
      if (ev) out.push(ev)
    }
    if (this.buf.length + this.data.join('\n').length > SseParser.MAX_PENDING) this.reset()
    return out
  }

  reset(): void {
    this.buf = ''
    this.name = ''
    this.data = []
  }

  private line(line: string): StreamEvent | undefined {
    if (line === '') {
      const name = this.name
      const data = this.data.join('\n')
      this.name = ''
      this.data = []
      return this.dispatch(name, data)
    }
    if (line.startsWith(':')) return undefined
    const i = line.indexOf(':')
    const field = i < 0 ? line : line.slice(0, i)
    let value = i < 0 ? '' : line.slice(i + 1)
    if (value.startsWith(' ')) value = value.slice(1)
    if (field === 'event') this.name = value
    else if (field === 'data') this.data.push(value)
    return undefined
  }

  private dispatch(name: string, data: string): StreamEvent | undefined {
    if (name === 'notifications_changed') return { type: 'changed' }
    if (name !== 'notification') return undefined
    try {
      const item = parseItem(JSON.parse(data))
      return item ? { type: 'notification', item } : undefined
    } catch {
      return undefined
    }
  }
}

// ---- reconnect ---------------------------------------------------------------------

export const BACKOFF_START_MS = 1_000
export const BACKOFF_MAX_MS = 60_000
/** The server answered 401, 403, 404 or 5xx-off: signing in or turning the feature on takes a person, so look rarely. */
export const BACKOFF_REFUSED_MS = 300_000

/** The wait before try number `attempt` (0 first): 1 s, doubled each time, at most 60 s, with up to 20 % jitter. */
export function backoffDelay(attempt: number, random: () => number = Math.random): number {
  const base = Math.min(BACKOFF_MAX_MS, BACKOFF_START_MS * 2 ** Math.min(Math.max(attempt, 0), 20))
  return Math.round(base * (1 + 0.2 * random()))
}

/** Statuses after which a retry soon cannot help. */
export function isRefusal(status: number): boolean {
  return status === 401 || status === 403 || status === 404 || status === 501
}

// ---- links and suppression -----------------------------------------------------------

/**
 * The URL a notification opens: its in-app path on the server it came from.
 * Only a path that starts with one slash is taken. A full URL, a
 * protocol-relative `//host`, a backslash, a control character or a path that
 * climbs out with `..` gives undefined, and so does anything that does not
 * stay on the server's origin after parsing. Same rule as webuiPolicy: the
 * view only ever shows its own origin.
 */
export function resolveLink(serverOrigin: string, link: string): string | undefined {
  const origin = httpOrigin(serverOrigin)
  if (!origin || typeof link !== 'string' || link === '') return undefined
  if (!link.startsWith('/') || link.startsWith('//') || /[\\\u0000-\u001f\u007f]/.test(link)) return undefined
  // The URL parser folds `..` away; a link that climbs is refused as it is written, also in its encoded form.
  if (link.split(/[?#]/)[0].split('/').some((p) => /^(\.|%2e){2}$/i.test(p))) return undefined
  let u: URL
  try {
    u = new URL(link, origin)
  } catch {
    return undefined
  }
  if (u.origin !== origin) return undefined
  return u.href
}

export interface ViewFacts {
  /** The launcher window has the keyboard focus. */
  windowFocused: boolean
  /** The WebUI view is the one on show (no shell screen above it). */
  webuiShown: boolean
  /** The URL the WebUI view shows, if any. */
  currentUrl?: string
}

/**
 * True when the user already looks at what the notification is about: the
 * window has the focus, the WebUI view is on show, and its page is the
 * notification's link (same origin and path; the query and hash are not
 * compared). The server marks a chat read when it is opened, so nothing is
 * lost.
 */
export function shouldSuppress(f: ViewFacts, serverOrigin: string, link: string): boolean {
  if (!f.windowFocused || !f.webuiShown || !f.currentUrl) return false
  const target = resolveLink(serverOrigin, link)
  if (!target) return false
  try {
    const a = new URL(target)
    const b = new URL(f.currentUrl)
    return a.origin === b.origin && a.pathname.replace(/\/+$/, '') === b.pathname.replace(/\/+$/, '')
  } catch {
    return false
  }
}

// ---- what is shown -------------------------------------------------------------------

export interface Shown {
  title: string
  body: string
}

/** The words of one desktop notification. The server name leads the title when more than one server is configured. */
export function formatNotification(serverName: string, item: InboxItem, multipleServers: boolean): Shown {
  const title = multipleServers && serverName ? `${serverName}: ${item.title}` : item.title
  return { title: title.slice(0, 200), body: item.body.slice(0, 300) }
}

// ---- streams -------------------------------------------------------------------------

/** One server to watch. The id is stable (`local`, or a saved server's id), so another kind of server needs only a new target. */
export interface NotifyTarget {
  id: string
  name: string
  /** `scheme://host:port`, no path. */
  origin: string
  /** Request headers that sign in to this server. Called for each request, so a key change is picked up. */
  headers?(): Record<string, string>
  /** Replaces `fetch` for this server, for a server that signs in with cookies of a session. */
  fetch?: typeof fetch
}

export interface NotifierDeps {
  fetch: typeof fetch
  setTimeout(fn: () => void, ms: number): unknown
  clearTimeout(t: unknown): void
  random?(): number
  log?(message: string): void
  onNotification(target: NotifyTarget, item: InboxItem): void
  /** The unread count of a server changed (or the server went away: 0). */
  onUnread(targetId: string, unread: number): void
}

export type StreamState = 'connecting' | 'live' | 'waiting' | 'stopped'

/** One server's stream, with reconnect. */
export class ServerStream {
  private ac: AbortController | undefined
  private timer: unknown
  private attempt = 0
  private gen = 0
  private seen: string[] = []
  state: StreamState = 'stopped'

  constructor(
    readonly target: NotifyTarget,
    private readonly d: NotifierDeps,
  ) {}

  start(): void {
    if (this.state !== 'stopped') return
    this.gen++
    this.attempt = 0
    void this.connect(this.gen)
  }

  stop(): void {
    this.gen++
    this.ac?.abort()
    this.ac = undefined
    if (this.timer !== undefined) this.d.clearTimeout(this.timer)
    this.timer = undefined
    this.state = 'stopped'
    this.d.onUnread(this.target.id, 0)
  }

  private wait(gen: number, ms: number): void {
    if (gen !== this.gen) return
    this.state = 'waiting'
    this.timer = this.d.setTimeout(() => {
      this.timer = undefined
      if (gen === this.gen) void this.connect(gen)
    }, ms)
  }

  private async request(path: string, signal: AbortSignal): Promise<Response> {
    const f = this.target.fetch ?? this.d.fetch
    return f(this.target.origin + path, { headers: { accept: 'text/event-stream', ...(this.target.headers?.() ?? {}) }, signal, redirect: 'error' })
  }

  /** Reads the unread count; a failure leaves the count as it is. */
  async refreshCount(): Promise<void> {
    const ac = this.ac
    const gen = this.gen
    if (!ac) return
    try {
      const r = await this.request(COUNT_PATH, ac.signal)
      if (!r.ok) return
      const body = (await r.json()) as { unread?: unknown }
      if (gen === this.gen && typeof body.unread === 'number' && Number.isFinite(body.unread) && body.unread >= 0) this.d.onUnread(this.target.id, Math.floor(body.unread))
    } catch {
      /* the stream shows the real trouble */
    }
  }

  private async connect(gen: number): Promise<void> {
    const ac = new AbortController()
    this.ac = ac
    this.state = 'connecting'
    let refused = false
    try {
      const r = await this.request(EVENTS_PATH, ac.signal)
      if (gen !== this.gen) return
      if (!r.ok || !r.body) {
        refused = isRefusal(r.status)
        await r.body?.cancel().catch(() => undefined)
        this.d.log?.(`notifications: ${this.target.name}: the stream answered ${r.status}`)
      } else {
        this.state = 'live'
        this.attempt = 0
        void this.refreshCount()
        await this.read(gen, r.body)
      }
    } catch (e) {
      if (gen !== this.gen) return
      this.d.log?.(`notifications: ${this.target.name}: ${(e as Error).name === 'AbortError' ? 'the stream was cut' : 'the stream failed'}`)
    }
    if (gen !== this.gen) return
    this.wait(gen, refused ? BACKOFF_REFUSED_MS : backoffDelay(this.attempt++, this.d.random))
  }

  private async read(gen: number, body: ReadableStream<Uint8Array>): Promise<void> {
    const parser = new SseParser()
    const dec = new TextDecoder()
    const reader = body.getReader()
    try {
      for (;;) {
        const { done, value } = await reader.read()
        if (done || gen !== this.gen) return
        for (const ev of parser.push(dec.decode(value, { stream: true }))) this.handle(ev)
      }
    } finally {
      reader.cancel().catch(() => undefined)
    }
  }

  private handle(ev: StreamEvent): void {
    if (ev.type === 'changed') {
      void this.refreshCount()
      return
    }
    if (this.seen.includes(ev.item.id)) return
    this.seen.push(ev.item.id)
    if (this.seen.length > 200) this.seen.shift()
    this.d.onNotification(this.target, ev.item)
    if (!ev.item.read) void this.refreshCount()
  }
}

/** The streams of all servers. `sync` is cheap and can be called at every change. */
export class Notifier {
  private streams = new Map<string, ServerStream>()
  private unread = new Map<string, number>()
  private enabled = true

  constructor(private readonly d: Omit<NotifierDeps, 'onUnread'> & { onTotal(total: number): void }) {}

  private deps(): NotifierDeps {
    return {
      ...this.d,
      onUnread: (id, n) => {
        if (n === 0) this.unread.delete(id)
        else this.unread.set(id, n)
        this.d.onTotal(this.total())
      },
    }
  }

  total(): number {
    let t = 0
    for (const n of this.unread.values()) t += n
    return t
  }

  /** The number of servers with a stream. */
  get count(): number {
    return this.streams.size
  }

  /** True when more than one server is configured, which puts the server name in the title. */
  multiple = false

  /**
   * Makes the streams match `targets`. A target that is gone or whose origin
   * changed is stopped; a new one starts (when notifications are on). `all`
   * is the number of configured servers, which can be larger than the number
   * of targets to watch (a local server that is not running has no stream).
   */
  sync(targets: NotifyTarget[], all = targets.length): void {
    this.multiple = all > 1
    const want = new Map(targets.map((t) => [t.id, t]))
    for (const [id, s] of this.streams) {
      const t = want.get(id)
      if (!t || t.origin !== s.target.origin || !this.enabled) {
        s.stop()
        this.streams.delete(id)
      }
    }
    if (!this.enabled) return
    for (const t of targets) {
      const cur = this.streams.get(t.id)
      if (cur) {
        // Same server: keep the stream, but use the new name and headers.
        Object.assign(cur.target, { name: t.name, headers: t.headers, fetch: t.fetch })
        continue
      }
      const s = new ServerStream({ ...t }, this.deps())
      this.streams.set(t.id, s)
      s.start()
    }
    this.last = targets
    this.lastAll = all
  }

  private last: NotifyTarget[] = []
  private lastAll = 0

  /** The setting "Desktop notifications". Off stops every stream; on starts them again. */
  setEnabled(on: boolean): void {
    if (on === this.enabled) return
    this.enabled = on
    this.sync(this.last, this.lastAll)
  }

  /** Reads the counts again, for example after the user opened the inbox in the window. */
  refresh(): void {
    for (const s of this.streams.values()) void s.refreshCount()
  }

  stopAll(): void {
    for (const s of this.streams.values()) s.stop()
    this.streams.clear()
  }

  stateOf(id: string): StreamState | undefined {
    return this.streams.get(id)?.state
  }
}
