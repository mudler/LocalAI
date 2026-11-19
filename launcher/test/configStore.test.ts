import fs from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'
import { saveConfig, type ConfigOptions, type LauncherConfig } from '../src/main/config'
import {
  ConfigStore,
  sandboxNoticeDismissedVersion,
  showStatusBar,
  withSandboxNoticeDismissed,
  withShowStatusBar,
} from '../src/main/configStore'
import { tmpDir } from './helpers'

function setup(content?: string) {
  const home = tmpDir('cfgstore-')
  const file = path.join(home, '.localai', 'launcher.json')
  if (content !== undefined) {
    fs.mkdirSync(path.dirname(file), { recursive: true })
    fs.writeFileSync(file, content)
  }
  return { home, file }
}

const read = (file: string) => JSON.parse(fs.readFileSync(file, 'utf8'))

describe('launcher-only keys', () => {
  it('show_status_bar defaults to true and is written beside the Go keys', async () => {
    const { home, file } = setup(JSON.stringify({ address: '127.0.0.1:9000', theme: 'dark' }))
    const store = await ConfigStore.open({ home })
    expect(showStatusBar(store.get())).toBe(true)
    await store.update((c) => withShowStatusBar(c, false))
    const w = read(file)
    expect(w.show_status_bar).toBe(false)
    expect(w.theme).toBe('dark') // unknown keys survive
    expect(w.address).toBe('127.0.0.1:9000')
    expect(Object.keys(w).indexOf('show_status_bar')).toBeGreaterThan(Object.keys(w).indexOf('show_welcome'))
  })

  it('a wrong type for show_status_bar reads as the default', async () => {
    const { home } = setup(JSON.stringify({ show_status_bar: 'no' }))
    expect(showStatusBar((await ConfigStore.open({ home })).get())).toBe(true)
  })

  it('stores the dismissed sandbox notice version', async () => {
    const { home, file } = setup()
    const store = await ConfigStore.open({ home })
    expect(sandboxNoticeDismissedVersion(store.get())).toBeNull()
    await store.update((c) => withSandboxNoticeDismissed(c, '1.2.3'))
    expect(read(file).sandbox_notice_dismissed_version).toBe('1.2.3')
    expect(sandboxNoticeDismissedVersion((await ConfigStore.open({ home })).get())).toBe('1.2.3')
  })
})

describe('single writer', () => {
  it('runs concurrent changes one at a time, each on the result of the one before', async () => {
    const { home, file } = setup()
    let active = 0
    let maxActive = 0
    const save = async (c: LauncherConfig, o: ConfigOptions) => {
      active++
      maxActive = Math.max(maxActive, active)
      await new Promise((r) => setTimeout(r, 5))
      await saveConfig(c, o)
      active--
    }
    const store = await ConfigStore.open({ home, save })
    await Promise.all(
      Array.from({ length: 20 }, (_, i) =>
        store.update((c) => ({ ...c, environment_vars: { ...c.environment_vars, [`K${i}`]: String(i) } })),
      ),
    )
    expect(maxActive).toBe(1)
    expect(Object.keys(read(file).environment_vars)).toHaveLength(20)
    expect(Object.keys(store.get().environment_vars)).toHaveLength(20)
  })

  it('keeps the previous config after a failed save and runs the next change', async () => {
    const { home, file } = setup()
    let fail = true
    const save = async (c: LauncherConfig, o: ConfigOptions) => {
      if (fail) {
        fail = false
        throw Object.assign(new Error('disk full'), { code: 'ENOSPC' })
      }
      await saveConfig(c, o)
    }
    const store = await ConfigStore.open({ home, save })
    const a = store.update((c) => ({ ...c, log_level: 'debug' }))
    const b = store.update((c) => withShowStatusBar(c, false))
    await expect(a).rejects.toThrow('disk full')
    await b
    expect(store.get().log_level).toBe('info')
    expect(read(file).log_level).toBe('info')
    expect(read(file).show_status_bar).toBe(false)
  })

  it('hands out copies, so a caller cannot change the stored config', async () => {
    const { home } = setup()
    const store = await ConfigStore.open({ home })
    const c = store.get()
    c.environment_vars.X = '1'
    expect(store.get().environment_vars).toEqual({})
  })

  it('reports a recovered file through `loaded`', async () => {
    const { home } = setup('{ not json')
    const store = await ConfigStore.open({ home })
    expect(store.loaded.status).toBe('recovered')
    expect(store.loaded.reason).toBe('config file is not valid JSON')
  })
})
