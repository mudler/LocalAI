// SPDX-License-Identifier: MIT
// Fakes of the native plugins and the platform for the client core tests.
// Each fake records its calls; a test emits plugin events with `emit`.
import type { PluginListenerHandle } from '@capacitor/core'
import type { HttpAnswer, HttpGet, TestResult } from '@launcher/core/testClassify'
import type { FileApi } from '../../src/core/configIO'
import type { ClientPlatform } from '../../src/core/clientCore'
import type { P2PForwardInput, P2PPlugin, WebuiViewPlugin } from '../../src/plugins/definitions'

export interface Call {
  name: string
  args: unknown[]
}

type Listener = (data: never) => void

/** A WebuiView plugin that records calls and lets the test send its events. */
export interface FakeWebui extends WebuiViewPlugin {
  calls: Call[]
  /** The value goBack answers. */
  history: boolean
  emit(event: string, data?: unknown): void
}

export function fakeWebui(): FakeWebui {
  const listeners = new Map<string, Set<Listener>>()
  const calls: Call[] = []
  const rec =
    <T>(name: string, answer: () => T) =>
    async (...args: unknown[]): Promise<T> => {
      calls.push({ name, args })
      return answer()
    }
  const w: Record<string, unknown> & { history: boolean } = {
    calls,
    history: false,
    show: rec('show', () => undefined),
    navigate: rec('navigate', () => undefined),
    close: rec('close', () => undefined),
    reload: rec('reload', () => undefined),
    setLayout: rec('setLayout', () => undefined),
    setPermissionStore: rec('setPermissionStore', () => undefined),
    goBack: rec('goBack', () => ({ handled: w.history })),
    currentUrl: rec('currentUrl', () => ({})),
    addListener(event: string, cb: Listener): Promise<PluginListenerHandle> {
      let set = listeners.get(event)
      if (!set) listeners.set(event, (set = new Set()))
      set.add(cb)
      return Promise.resolve({ remove: async () => void set.delete(cb) })
    },
    emit(event: string, data?: unknown) {
      for (const cb of listeners.get(event) ?? []) (cb as (d: unknown) => void)(data)
    },
  }
  return w as unknown as FakeWebui
}

/** Files in memory. `failRead` makes every read throw. */
export interface FakeFiles extends FileApi {
  files: Map<string, string>
  failRead: boolean
  ops: string[]
}

export function fakeFiles(initial: Record<string, string> = {}): FakeFiles {
  const f: FakeFiles = {
    files: new Map(Object.entries(initial)),
    failRead: false,
    ops: [],
    async read(name) {
      f.ops.push(`read ${name}`)
      if (f.failRead) throw new Error('read failed')
      return f.files.get(name)
    },
    async write(name, text) {
      f.ops.push(`write ${name}`)
      f.files.set(name, text)
    },
    async rename(from, to) {
      f.ops.push(`rename ${from} ${to}`)
      const v = f.files.get(from)
      if (v === undefined) throw new Error('does not exist')
      f.files.delete(from)
      f.files.set(to, v)
    },
  }
  return f
}

export type Answer = 'ok' | 'signin' | 'unreachable'

/** A scripted HTTP transport: each origin answers as `answers` says (default unreachable). */
export interface FakeHttp {
  get: HttpGet
  answers: Map<string, Answer>
  /** The number of server checks per origin (one discovery request each). */
  checks(origin: string): number
}

class Unreachable extends Error {}

export function fakeHttp(answers: Record<string, Answer> = {}): FakeHttp {
  const map = new Map(Object.entries(answers))
  const count = new Map<string, number>()
  const get: HttpGet = async (url): Promise<HttpAnswer> => {
    const u = new URL(url)
    if (u.pathname === '/.well-known/localai.json') count.set(u.origin, (count.get(u.origin) ?? 0) + 1)
    const a = map.get(u.origin) ?? 'unreachable'
    if (a === 'unreachable') throw new Unreachable('refused')
    if (a === 'signin') return { status: 401, contentType: 'text/plain', body: '' }
    if (u.pathname === '/.well-known/localai.json') {
      return { status: 200, contentType: 'application/json', body: JSON.stringify({ version: 'v4.1.3', endpoints: { chat_completions: '/v1/chat/completions' } }) }
    }
    return { status: 404, contentType: 'text/plain', body: '' }
  }
  return { get, answers: map, checks: (o) => count.get(o) ?? 0 }
}

export function classifyFake(e: unknown): TestResult {
  return e instanceof Unreachable ? { status: 'unreachable', reason: 'refused' } : { status: 'unreachable', reason: 'network' }
}

/**
 * A P2P plugin that records calls and lets the test send its events. With
 * `version` undefined it behaves like a build without the library: every
 * engine call rejects with `unavailable`.
 */
export interface FakeP2P extends P2PPlugin {
  calls: Call[]
  version: P2PPlugin['version']
  /** What `version()` answers; undefined rejects. */
  libraryVersion: string | undefined
  /** The pairing answers, one per call (the last repeats). */
  pairAnswers: Awaited<ReturnType<P2PPlugin['pair']>>[]
  /** The next ports `freePort` gives, one per call. */
  ports: number[]
  /** The state lines `statuses` answers. */
  lines: { id: string; line: string }[]
  keep: boolean
  /** Peers signed out of in their WebUI: `allowAutoSignIn` takes a peer out and answers whether it was in. */
  signedOut: Set<string>
  /** Makes `setForwards` reject with this message. */
  failSetForwards?: string
  /** The forward lists sent, in order. */
  forwardLists(): P2PForwardInput[][]
  emit(event: string, data?: unknown): void
}

/** A pairing answer of the library for `peer`. */
export function pairJson(peer: string, serverName = 'Studio', phrase = 'apple banana cherry'): string {
  return JSON.stringify({ device_id: 'dev1', kind: 'device', phrase, server: { peer_id: peer, name: serverName, version: 'v4.1.3' }, relays: [], addrs: [] })
}

export function fakeP2P(libraryVersion?: string): FakeP2P {
  const listeners = new Map<string, Set<Listener>>()
  const calls: Call[] = []
  const unavailable = () => Promise.reject(Object.assign(new Error('unavailable'), { code: 'unavailable' }))
  const f: FakeP2P = {
    calls,
    libraryVersion,
    pairAnswers: [],
    ports: [],
    lines: [],
    keep: false,
    signedOut: new Set(),
    forwardLists: () => calls.filter((c) => c.name === 'setForwards').map((c) => (c.args[0] as { forwards: P2PForwardInput[] }).forwards),
    async version() {
      calls.push({ name: 'version', args: [] })
      return f.libraryVersion === undefined ? unavailable() : { version: f.libraryVersion }
    },
    async deviceName() {
      return { name: 'Test phone' }
    },
    async peerId() {
      return {}
    },
    async pair(o) {
      calls.push({ name: 'pair', args: [o] })
      if (f.libraryVersion === undefined) return unavailable()
      return f.pairAnswers.length > 1 ? f.pairAnswers.shift()! : (f.pairAnswers[0] ?? { ok: false, detail: 'no answer scripted' })
    },
    async forgetCredential(o) {
      calls.push({ name: 'forgetCredential', args: [o] })
    },
    async allowAutoSignIn(o) {
      calls.push({ name: 'allowAutoSignIn', args: [o] })
      return { wasSignedOut: f.signedOut.delete(o.peer) }
    },
    async retainCredentials(o) {
      calls.push({ name: 'retainCredentials', args: [o] })
    },
    async freePort(o) {
      calls.push({ name: 'freePort', args: [o] })
      return { port: f.ports.shift() ?? 41000 }
    },
    async setForwards(o) {
      calls.push({ name: 'setForwards', args: [o] })
      if (f.libraryVersion === undefined) return unavailable()
      if (f.failSetForwards) throw new Error(f.failSetForwards)
    },
    async statuses() {
      calls.push({ name: 'statuses', args: [] })
      return { states: [...f.lines] }
    },
    async setKeepInBackground(o) {
      calls.push({ name: 'setKeepInBackground', args: [o] })
      f.keep = o.on
    },
    async keepInBackground() {
      calls.push({ name: 'keepInBackground', args: [] })
      return { on: f.keep }
    },
    async requestNotifications() {
      calls.push({ name: 'requestNotifications', args: [] })
      return { granted: true }
    },
    addListener(event: string, cb: Listener): Promise<PluginListenerHandle> {
      let set = listeners.get(event)
      if (!set) listeners.set(event, (set = new Set()))
      set.add(cb)
      return Promise.resolve({ remove: async () => void set.delete(cb) })
    },
    emit(event: string, data?: unknown) {
      for (const cb of listeners.get(event) ?? []) (cb as (d: unknown) => void)(data)
    },
  } as FakeP2P
  return f
}

export interface FakePlatform extends ClientPlatform {
  files: FakeFiles
  webui: FakeWebui
  p2p: FakeP2P
  fakeHttp: FakeHttp
  opened: string[]
}

export function fakePlatform(o: { files?: Record<string, string>; answers?: Record<string, Answer>; p2pVersion?: string } = {}): FakePlatform {
  const http = fakeHttp(o.answers)
  let seq = 0
  const opened: string[] = []
  return {
    files: fakeFiles(o.files),
    http: http.get,
    fakeHttp: http,
    classifyError: classifyFake,
    webui: fakeWebui(),
    p2p: fakeP2P(o.p2pVersion),
    openExternal: async (url) => void opened.push(url),
    opened,
    now: () => Date.UTC(2026, 10, 25, 12, 0, 0),
    randomBytes: (n) => {
      seq++
      return new Uint8Array(n).fill(seq)
    },
    appVersion: '5.0.2',
    deviceName: async () => 'Test phone',
  }
}

/** Lets queued promises (plugin calls, config saves) run. */
export async function flush(): Promise<void> {
  for (let i = 0; i < 10; i++) await new Promise((r) => setTimeout(r, 0))
}
