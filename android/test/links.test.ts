// SPDX-License-Identifier: MIT
// Invites that reach the app from outside: the AppLinks plugin (a localai://pair
// link or the share sheet) and the Scanner plugin (a QR code), as the bridge
// hands them to the launcher shell. The shell only opens the invite form with
// them; it never pairs by itself (see the launcher renderer tests).
import { afterEach, describe, expect, it } from 'vitest'
import type { PluginListenerHandle } from '@capacitor/core'
import { createAndroidBridge, idleUpdater, inviteLinkFeed, type InviteSources } from '../src/bridge/androidBridge'
import { ClientCore } from '../src/core/clientCore'
import type { AppLinksPlugin, ScannerPlugin } from '../src/plugins/definitions'
import { fakePlatform, flush } from './fakes/plugins'

const LINK = 'localai://pair?v=1&k=device&p=12D3KooWQ6jWKvb3LfQwPzbNxQmPqYGSmrW3Fq2PTSaAVoiBAJGm&c=ABCDEFGHJK&e=1795960500'

interface FakeAppLinks extends AppLinksPlugin {
  initial?: string
  takes: number
  fail: boolean
  emit(text: string): void
}

function fakeAppLinks(initial?: string): FakeAppLinks {
  const cbs = new Set<(x: { text: string }) => void>()
  const f: FakeAppLinks = {
    initial,
    takes: 0,
    fail: false,
    async takeInitial() {
      f.takes++
      if (f.fail) throw new Error('not implemented')
      const text = f.initial
      f.initial = undefined
      return text === undefined ? {} : { text }
    },
    addListener(_e: 'link', cb: (x: { text: string }) => void): Promise<PluginListenerHandle> {
      if (f.fail) return Promise.reject(new Error('not implemented'))
      cbs.add(cb)
      return Promise.resolve({ remove: async () => void cbs.delete(cb) })
    },
    emit(text) {
      for (const cb of cbs) cb({ text })
    },
  }
  return f
}

function fakeScanner(answers: { text?: string; cancelled: boolean }[]): ScannerPlugin & { calls: number } {
  const s = {
    calls: 0,
    async scan() {
      s.calls++
      const a = answers.shift()
      if (!a) throw new Error('camera permission denied')
      return a
    },
  }
  return s
}

let cores: ClientCore[] = []
afterEach(async () => {
  for (const c of cores) await c.shutdown()
  cores = []
})

async function rig(invites?: InviteSources) {
  const core = await ClientCore.open(fakePlatform())
  cores.push(core)
  const bridge = createAndroidBridge(core, { clipboard: async () => undefined, updater: idleUpdater(), info: { version: '5.0.2' }, invites })
  await flush()
  return bridge
}

describe('inviteLinkFeed', () => {
  it('gives the link the app was opened with once, to the first listener', async () => {
    const links = fakeAppLinks(LINK)
    const on = inviteLinkFeed(links)
    await flush()
    const a: string[] = []
    const b: string[] = []
    const offA = on((t) => a.push(t))
    offA()
    on((t) => b.push(t))
    await flush()
    expect(a).toEqual([LINK])
    expect(b).toEqual([])
    expect(links.takes).toBe(1)
  })

  it('keeps a link that arrives while nobody listens, until the shell subscribes', async () => {
    const links = fakeAppLinks()
    const on = inviteLinkFeed(links)
    await flush()
    links.emit('first')
    links.emit(LINK)
    const got: string[] = []
    on((t) => got.push(t))
    expect(got).toEqual([LINK])
  })

  it('passes later links to every listener, and none after it unsubscribes', async () => {
    const links = fakeAppLinks()
    const on = inviteLinkFeed(links)
    await flush()
    const got: string[] = []
    const off = on((t) => got.push(t))
    links.emit(LINK)
    links.emit('hello')
    off()
    links.emit('late')
    expect(got).toEqual([LINK, 'hello'])
  })

  it('stays quiet when the plugin is missing', async () => {
    const links = fakeAppLinks(LINK)
    links.fail = true
    const on = inviteLinkFeed(links)
    const got: string[] = []
    on((t) => got.push(t))
    await flush()
    expect(got).toEqual([])
  })
})

describe('the Android bridge and invites', () => {
  it('has no scan or link channel without the plugins', async () => {
    const b = await rig()
    expect('scanInvite' in b).toBe(false)
    expect('onInviteLink' in b).toBe(false)
  })

  it('emits the initial link once through onInviteLink', async () => {
    const links = fakeAppLinks(LINK)
    const b = await rig({ appLinks: links, scanner: fakeScanner([]) })
    const got: string[] = []
    b.onInviteLink!((t) => got.push(t))
    await flush()
    b.onInviteLink!((t) => got.push(`second:${t}`))
    await flush()
    expect(got).toEqual([LINK])
    expect(links.takes).toBe(1)
  })

  it('scanInvite answers the scanned text, undefined when cancelled, and passes a failure on', async () => {
    const scanner = fakeScanner([{ text: LINK, cancelled: false }, { cancelled: true }])
    const b = await rig({ appLinks: fakeAppLinks(), scanner })
    expect(await b.scanInvite!()).toBe(LINK)
    expect(await b.scanInvite!()).toBeUndefined()
    await expect(b.scanInvite!()).rejects.toThrow('camera permission denied')
    expect(scanner.calls).toBe(3)
  })
})
