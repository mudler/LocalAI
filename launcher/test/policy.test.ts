import { describe, expect, it } from 'vitest'
import { decideClose, shouldStartVisible, trayWasCreated } from '../src/main/trayPolicy'
import { launcherUpdateAction, normalizeUpdaterEvent } from '../src/main/updaterStatus'
import { releasePageUrl } from '../src/main/updateProvider'
import { chatUrl } from '../src/main/models'

describe('decideClose', () => {
  it('hides the window when a tray exists', () => {
    expect(decideClose({ trayCreated: true, serverRunning: true, quitRequested: false })).toBe('hide')
    expect(decideClose({ trayCreated: true, serverRunning: false, quitRequested: false })).toBe('hide')
  })
  it('quits at once without a tray when no server runs', () => {
    expect(decideClose({ trayCreated: false, serverRunning: false, quitRequested: false })).toBe('quit')
  })
  it('asks first without a tray when the server runs', () => {
    expect(decideClose({ trayCreated: false, serverRunning: true, quitRequested: false })).toBe('confirm-quit')
  })
  it('an explicit quit always quits', () => {
    expect(decideClose({ trayCreated: true, serverRunning: true, quitRequested: true })).toBe('quit')
  })
})

describe('tray detection', () => {
  it('the window starts visible without a tray, whatever the setting', () => {
    expect(shouldStartVisible(false, true)).toBe(true)
    expect(shouldStartVisible(true, true)).toBe(false)
    expect(shouldStartVisible(true, false)).toBe(true)
  })
  it('a constructed tray with no host does not count', () => {
    expect(trayWasCreated(true, false)).toBe(false)
    expect(trayWasCreated(true, true)).toBe(true)
    expect(trayWasCreated(true, undefined)).toBe(true)
    expect(trayWasCreated(false, true)).toBe(false)
  })
})

describe('normalizeUpdaterEvent', () => {
  const idle = { phase: 'idle' as const }
  it('maps the events to one shape', () => {
    expect(normalizeUpdaterEvent({ type: 'checking-for-update' }, idle)).toEqual({ phase: 'checking' })
    expect(normalizeUpdaterEvent({ type: 'update-available', info: { version: '1.2.0' } }, idle)).toEqual({ phase: 'available', version: '1.2.0' })
    expect(normalizeUpdaterEvent({ type: 'update-not-available' }, idle).phase).toBe('not-available')
    expect(normalizeUpdaterEvent({ type: 'update-downloaded', info: { version: '1.2.0' } }, idle)).toEqual({ phase: 'downloaded', version: '1.2.0' })
  })
  it('clamps progress and keeps the version', () => {
    const prev = { phase: 'available' as const, version: '1.2.0' }
    expect(normalizeUpdaterEvent({ type: 'download-progress', progress: { percent: 41.7 } }, prev)).toEqual({ phase: 'downloading', version: '1.2.0', percent: 42 })
    expect(normalizeUpdaterEvent({ type: 'download-progress', progress: { percent: 400 } }, prev).percent).toBe(100)
    expect(normalizeUpdaterEvent({ type: 'download-progress', progress: {} }, prev).percent).toBe(0)
  })
  it('turns an error into a short message', () => {
    const s = normalizeUpdaterEvent({ type: 'error', error: new Error('boom\nheaders: x\nmore') }, idle)
    expect(s).toEqual({ phase: 'error', message: 'boom' })
    expect(normalizeUpdaterEvent({ type: 'error' }, idle).message).toBe('update check failed')
  })
})

describe('chatUrl', () => {
  it('opens /app with or without a model', () => {
    expect(chatUrl('http://127.0.0.1:8080')).toBe('http://127.0.0.1:8080/app')
    expect(chatUrl('http://127.0.0.1:8080/', 'qwen3-8b')).toBe('http://127.0.0.1:8080/app?model=qwen3-8b')
    expect(chatUrl('http://h:1', 'a/b c')).toBe('http://h:1/app?model=a%2Fb%20c')
  })
})

describe('launcherUpdateAction', () => {
  it('installs a downloaded update, downloads an available one, and does nothing otherwise', () => {
    expect(launcherUpdateAction({ phase: 'downloaded', version: '1.2.0' })).toBe('install')
    expect(launcherUpdateAction({ phase: 'available', version: '1.2.0' })).toBe('download')
    for (const phase of ['unsupported', 'idle', 'checking', 'not-available', 'downloading', 'error'] as const) {
      expect(launcherUpdateAction({ phase }), phase).toBe('none')
    }
  })
})

describe('releasePageUrl', () => {
  const repo = { owner: 'mudler', repo: 'LocalAI' }
  it('builds the tag page of a plain release version', () => {
    expect(releasePageUrl(repo, '4.2.1')).toBe('https://github.com/mudler/LocalAI/releases/tag/v4.2.1')
    expect(releasePageUrl(repo, 'v4.2.1-rc.1')).toBe('https://github.com/mudler/LocalAI/releases/tag/v4.2.1-rc.1')
  })
  it('refuses anything that is not a version', () => {
    for (const v of ['', '4.2', '4.2.1/../../x', '4.2.1?x=1', 'https://example.test/', '4.2.1 ', '4.2.1#x']) expect(releasePageUrl(repo, v), v).toBeUndefined()
  })
})
