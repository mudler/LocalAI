import { describe, expect, it, vi } from 'vitest'
import { guard, isTrustedSender, UntrustedSenderError, type FrameInfo, type TrustedShell } from '../src/main/ipcGuard'
import * as v from '../src/main/ipcValidate'

const PAGE = 'file:///opt/app/resources/app.asar/dist/renderer/index.html'
const shell: TrustedShell = { webContentsId: 1, processId: 10, routingId: 1, pageUrl: PAGE }
const shellFrame: FrameInfo = { webContentsId: 1, processId: 10, routingId: 1, url: PAGE, isMainFrame: true }

describe('isTrustedSender', () => {
  it("accepts the shell view's main frame, also with a hash route or a query", () => {
    expect(isTrustedSender(shellFrame, shell)).toBe(true)
    expect(isTrustedSender({ ...shellFrame, url: `${PAGE}#/logs` }, shell)).toBe(true)
    expect(isTrustedSender({ ...shellFrame, url: `${PAGE}?x=1` }, shell)).toBe(true)
  })

  it('rejects a subframe of the shell view', () => {
    expect(isTrustedSender({ ...shellFrame, isMainFrame: false, routingId: 7 }, shell)).toBe(false)
    // Even one that claims the main frame's ids.
    expect(isTrustedSender({ ...shellFrame, isMainFrame: false }, shell)).toBe(false)
  })

  it('rejects the WebUI view and any other WebContents', () => {
    const webui: FrameInfo = { webContentsId: 2, processId: 11, routingId: 1, url: 'http://127.0.0.1:8080/app', isMainFrame: true }
    expect(isTrustedSender(webui, shell)).toBe(false)
    // Another WebContents that loads the shell page (a second window, devtools) is still not the shell.
    expect(isTrustedSender({ ...shellFrame, webContentsId: 3, processId: 12 }, shell)).toBe(false)
  })

  it('rejects a stale frame after a reload or a renderer process swap', () => {
    expect(isTrustedSender({ ...shellFrame, processId: 9 }, shell)).toBe(false)
    expect(isTrustedSender({ ...shellFrame, routingId: 2 }, shell)).toBe(false)
  })

  it('rejects the shell view after it navigated away from the app page', () => {
    expect(isTrustedSender({ ...shellFrame, url: 'https://example.com/' }, shell)).toBe(false)
    expect(isTrustedSender({ ...shellFrame, url: 'file:///opt/app/other.html' }, shell)).toBe(false)
    expect(isTrustedSender({ ...shellFrame, url: 'not a url' }, shell)).toBe(false)
  })

  it('rejects when there is no frame or no shell', () => {
    expect(isTrustedSender(null, shell)).toBe(false)
    expect(isTrustedSender(undefined, shell)).toBe(false)
    expect(isTrustedSender(shellFrame, undefined)).toBe(false)
  })
})

describe('guard', () => {
  it('runs the handler only for the trusted sender and reports the refusal', () => {
    const handler = vi.fn((...a: unknown[]) => a.length)
    const refused = vi.fn()
    const g = guard(handler, (e: { frame: FrameInfo | null }) => e.frame, () => shell, refused)
    expect(g({ frame: shellFrame }, 'a', 'b')).toBe(2)
    expect(() => g({ frame: { ...shellFrame, webContentsId: 2 } }, 'a')).toThrow(UntrustedSenderError)
    expect(() => g({ frame: null })).toThrow(UntrustedSenderError)
    expect(handler).toHaveBeenCalledTimes(1)
    expect(refused).toHaveBeenCalledTimes(2)
  })

  it('refuses everything while there is no shell view', () => {
    const handler = vi.fn()
    const g = guard(handler, (e: FrameInfo) => e, () => undefined)
    expect(() => g(shellFrame)).toThrow(UntrustedSenderError)
    expect(handler).not.toHaveBeenCalled()
  })
})

describe('input checks', () => {
  it('argument counts', () => {
    expect(() => v.noArgs([])).not.toThrow()
    expect(() => v.noArgs([1])).toThrow(v.IpcInputError)
    expect(v.oneOptional([])).toBeUndefined()
    expect(() => v.oneOptional([1, 2])).toThrow(v.IpcInputError)
    expect(() => v.one([])).toThrow(v.IpcInputError)
  })

  it('install options: known booleans only', () => {
    expect(v.installOptions(undefined)).toEqual({})
    expect(v.installOptions({ stopFirst: true })).toEqual({ stopFirst: true })
    expect(() => v.installOptions({ stopFirst: 'yes' })).toThrow(v.IpcInputError)
    expect(() => v.installOptions({ path: '/x' })).toThrow(/unknown field/)
    expect(() => v.installOptions([])).toThrow(v.IpcInputError)
    expect(() => v.installOptions(null)).toThrow(v.IpcInputError)
  })

  it('log query: enums and ranges, with a default limit', () => {
    expect(v.logQuery(undefined)).toEqual({ limit: 1000 })
    expect(v.logQuery({ source: 'server', minLevel: 'warn', afterSeq: 5, limit: 10 })).toEqual({ source: 'server', minLevel: 'warn', afterSeq: 5, limit: 10 })
    expect(() => v.logQuery({ source: 'kernel' })).toThrow(v.IpcInputError)
    expect(() => v.logQuery({ limit: 0 })).toThrow(v.IpcInputError)
    expect(() => v.logQuery({ limit: v.MAX_LOG_QUERY + 1 })).toThrow(v.IpcInputError)
    expect(() => v.logQuery({ limit: 1.5 })).toThrow(v.IpcInputError)
    expect(() => v.logQuery({ afterSeq: -1 })).toThrow(v.IpcInputError)
  })

  it('plan kind, model id and clipboard text', () => {
    expect(v.planKind(undefined)).toBe('setup')
    expect(v.planKind('remove')).toBe('remove')
    expect(() => v.planKind('run')).toThrow(v.IpcInputError)
    expect(v.modelId(undefined)).toBeUndefined()
    expect(v.modelId('qwen3-4b')).toBe('qwen3-4b')
    expect(() => v.modelId('a\nb')).toThrow(v.IpcInputError)
    expect(() => v.modelId(5)).toThrow(v.IpcInputError)
    expect(v.clipboardText('line 1\nline 2')).toBe('line 1\nline 2')
    expect(() => v.clipboardText('x'.repeat(64 * 1024 + 1))).toThrow(v.IpcInputError)
  })

  it('address', () => {
    for (const ok of ['127.0.0.1:8080', '0.0.0.0:1', 'localhost:65535', '[::1]:8080', ':8080', 'host.example.com:443']) {
      expect(v.address(ok)).toBe(ok)
    }
    for (const bad of ['127.0.0.1', '127.0.0.1:0', '127.0.0.1:70000', 'a b:80', 'http://x:80', '[zz]:80', '-x:80', 'a..b:80', 8080]) {
      expect(() => v.address(bad)).toThrow(v.IpcInputError)
    }
  })

  it('settings patch: never a data folder path, which only the main-process picker sets', () => {
    for (const p of [{ modelsPath: '/srv/localai/models' }, { backendsPath: '/srv/localai/backends' }, { address: '127.0.0.1:8080', modelsPath: '/srv/m' }]) {
      expect(() => v.settingsPatch(p)).toThrow(v.IpcInputError)
    }
  })

  it('settings patch: known keys, each checked', () => {
    expect(v.settingsPatch({ showStatusBar: false, logLevel: 'debug' })).toEqual({ showStatusBar: false, logLevel: 'debug' })
    expect(v.settingsPatch({ secretsPlaintextFile: true })).toEqual({ secretsPlaintextFile: true })
    expect(() => v.settingsPatch({ secretsPlaintextFile: 'yes' })).toThrow(v.IpcInputError)
    expect(() => v.settingsPatch({ logLevel: 'verbose' })).toThrow(v.IpcInputError)
    expect(() => v.settingsPatch({ extra: {} })).toThrow(/unknown field/)
    // Values of environment variables never travel in settings: their own channels take them.
    expect(() => v.settingsPatch({ environmentVars: { LOCALAI_THREADS: '4' } })).toThrow(/unknown field/)
    expect(() => v.settingsPatch('x')).toThrow(v.IpcInputError)
    expect(() => v.settingsPatch(Object.create({ polluted: true }))).toThrow(v.IpcInputError)
  })
})
