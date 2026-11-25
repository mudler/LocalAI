// SPDX-License-Identifier: MIT
import { describe, expect, it } from 'vitest'
import { ConfigStore } from '@launcher/core/configStore'
import { defaultTarget, remotes } from '@launcher/core/serversConfig'
import { ANDROID_DEFAULTS, CONFIG_FILE, clientConfigIO } from '../src/core/configIO'
import { fakeFiles } from './fakes/plugins'

const NOW = () => new Date(Date.UTC(2026, 10, 25, 9, 8, 7))

describe('clientConfigIO', () => {
  it('loads the defaults when there is no file', async () => {
    const r = await clientConfigIO(fakeFiles(), NOW).load()
    expect(r.status).toBe('missing')
    expect(r.config.models_path).toBe(ANDROID_DEFAULTS.modelsPath)
    expect(r.config.extra).toEqual({})
  })

  it('moves a file that is not JSON aside and reports it', async () => {
    const files = fakeFiles({ [CONFIG_FILE]: '{ not json' })
    const r = await clientConfigIO(files, NOW).load()
    expect(r.status).toBe('recovered')
    expect(r.reason).toBe('config file is not valid JSON')
    expect(r.backupPath).toBe(`${CONFIG_FILE}.bad-20261125T090807Z`)
    expect(files.files.has(CONFIG_FILE)).toBe(false)
    expect(files.files.get(`${CONFIG_FILE}.bad-20261125T090807Z`)).toBe('{ not json')
  })

  it('moves a JSON value that is not an object aside', async () => {
    const files = fakeFiles({ [CONFIG_FILE]: '[1, 2]' })
    const r = await clientConfigIO(files, NOW).load()
    expect(r).toMatchObject({ status: 'recovered', reason: 'config file is not a JSON object' })
    expect(files.files.has(CONFIG_FILE)).toBe(false)
  })

  it('reports a file it cannot read and leaves it in place', async () => {
    const files = fakeFiles({ [CONFIG_FILE]: '{}' })
    files.failRead = true
    const r = await clientConfigIO(files, NOW).load()
    expect(r).toMatchObject({ status: 'recovered', reason: 'config file could not be read (error)' })
    expect(files.files.has(CONFIG_FILE)).toBe(true)
  })

  it('saves only the client keys, through a temp file', async () => {
    const files = fakeFiles()
    const store = await ConfigStore.open(clientConfigIO(files, NOW))
    await store.update((c) => ({
      ...c,
      extra: { ...c.extra, remotes: [{ id: 'srv_00000001', name: 'GPU box', url: 'https://ai.example.com' }], default_target: 'srv_00000001' },
    }))
    expect(files.ops.slice(-2)).toEqual([`write ${CONFIG_FILE}.tmp`, `rename ${CONFIG_FILE}.tmp ${CONFIG_FILE}`])
    const text = files.files.get(CONFIG_FILE)!
    expect(text.endsWith('}\n')).toBe(true)
    expect(text).toContain('\n  "remotes"')
    const saved = JSON.parse(text)
    expect(saved.default_target).toBe('srv_00000001')
    expect(saved.remotes).toHaveLength(1)
    for (const k of ['address', 'models_path', 'backends_path', 'auto_start_server', 'environment_vars', 'show_welcome']) expect(k in saved, k).toBe(false)
  })

  it('keeps unknown keys on a round trip', async () => {
    const start = { remotes: [{ id: 'srv_00000001', name: 'GPU box', url: 'https://ai.example.com' }], default_target: 'srv_00000001', future_key: { a: 1 } }
    const files = fakeFiles({ [CONFIG_FILE]: JSON.stringify(start) })
    const io = clientConfigIO(files, NOW)
    const loaded = await io.load()
    expect(loaded.status).toBe('ok')
    expect(remotes(loaded.config)).toHaveLength(1)
    expect(defaultTarget(loaded.config)).toBe('srv_00000001')
    await io.save(loaded.config)
    expect(JSON.parse(files.files.get(CONFIG_FILE)!)).toEqual(start)
  })

  it('uses the temp file when a save stopped between its two steps', async () => {
    const files = fakeFiles({ [`${CONFIG_FILE}.tmp`]: JSON.stringify({ default_target: 'local', future_key: 1 }) })
    const r = await clientConfigIO(files, NOW).load()
    expect(r.status).toBe('ok')
    expect(r.config.extra.future_key).toBe(1)
  })
})
