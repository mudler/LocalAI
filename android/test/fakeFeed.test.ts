// SPDX-License-Identifier: MIT
// The manual self-update test (android/README.md, "Test the self-update")
// serves a feed with the launcher's fake release server, at the paths the app
// reads, on an address the phone can reach.
import { spawn, type ChildProcess } from 'node:child_process'
import fs from 'node:fs'
import net from 'node:net'
import os from 'node:os'
import path from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
import { feedUrls, type AppSelf } from '../src/core/apkUpdate'

const SERVER = path.resolve(__dirname, '../../launcher/scripts/fake-release-server.mjs')

let child: ChildProcess | undefined
let dir = ''

afterEach(() => {
  child?.kill()
  child = undefined
  if (dir) fs.rmSync(dir, { recursive: true, force: true })
})

async function freePort(): Promise<number> {
  return new Promise((resolve) => {
    const s = net.createServer().listen(0, '127.0.0.1', () => {
      const p = (s.address() as net.AddressInfo).port
      s.close(() => resolve(p))
    })
  })
}

async function serve(host: string): Promise<string> {
  const port = await freePort()
  child = spawn(process.execPath, [SERVER, String(port), dir, path.join(dir, 'requests.log'), host], { stdio: 'ignore' })
  const base = `http://${host}:${port}`
  for (let i = 0; i < 100; i++) {
    try {
      await fetch(`${base}/`)
      return base
    } catch {
      await new Promise((r) => setTimeout(r, 50))
    }
  }
  throw new Error('the fake release server did not start')
}

function put(rel: string, text: string) {
  const f = path.join(dir, rel)
  fs.mkdirSync(path.dirname(f), { recursive: true })
  fs.writeFileSync(f, text)
}

const self = (channel: 'stable' | 'dev', feedBase: string): AppSelf => ({
  applicationId: channel === 'dev' ? 'io.localai.client.dev' : 'io.localai.client',
  versionName: '5.0.0',
  versionCode: 500000099,
  sdkInt: 34,
  feedBase,
  channel,
})

describe('the fake release server as an APK feed', () => {
  it('serves the manifest, APK and checksum of both channels on the host it is given', async () => {
    dir = fs.mkdtempSync(path.join(os.tmpdir(), 'apk-feed-'))
    put('latest/download/android-update.json', '{"schema":1}')
    put('latest/download/LocalAI-Android.apk', 'stable apk')
    put('latest/download/LocalAI-Android.apk.sha256', 'abc  LocalAI-Android.apk\n')
    put('download/dev-master/android-update-dev.json', '{"schema":1,"dev":true}')
    put('download/dev-master/LocalAI-Android-dev.apk', 'dev apk')
    put('download/dev-master/LocalAI-Android-dev.apk.sha256', 'def  LocalAI-Android-dev.apk\n')
    // Another loopback address stands for the LAN address of the test machine.
    const base = await serve('127.0.0.2')
    const stable = feedUrls(self('stable', base))
    expect(await (await fetch(stable.manifest)).text()).toBe('{"schema":1}')
    expect(await (await fetch(stable.apk)).text()).toBe('stable apk')
    expect(await (await fetch(stable.checksum)).text()).toBe('abc  LocalAI-Android.apk\n')
    const dev = feedUrls(self('dev', base))
    expect(await (await fetch(dev.manifest)).text()).toBe('{"schema":1,"dev":true}')
    expect(await (await fetch(dev.apk)).text()).toBe('dev apk')
    expect((await fetch(`${base}/latest/download/missing.apk`)).status).toBe(404)
  })
})
