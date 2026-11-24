import { describe, expect, it } from 'vitest'
import {
  decideNavigation,
  decidePermission,
  decidePermissionCheck,
  decideWindowOpen,
  externalUrl,
  hasRecentGesture,
  httpOrigin,
  isLocalOrPrivateHost,
  isServerOrigin,
  parsePermissionStore,
  partitionForOrigin,
  recall,
  remember,
  resetPermissions,
  sanitizeFileName,
  validateRemoteOrigin,
  type PermissionRequestInfo,
} from '../src/main/webuiPolicy'

const SERVER = 'http://127.0.0.1:8080'
const LOCALHOST = 'http://localhost:8080'

describe('httpOrigin', () => {
  it.each([
    ['http://127.0.0.1:8080/app', 'http://127.0.0.1:8080'],
    ['HTTP://LOCALHOST:8080/x', 'http://localhost:8080'],
    ['https://example.test:443/', 'https://example.test'],
    ['http://[0:0:0:0:0:0:0:1]:8080/', 'http://[::1]:8080'],
    ['http://2130706433:8080/', 'http://127.0.0.1:8080'],
    ['http://bücher.example.test/', 'http://xn--bcher-kva.example.test'],
  ])('%s -> %s', (url, origin) => expect(httpOrigin(url)).toBe(origin))

  it.each([
    'http://user@127.0.0.1:8080/',
    'http://localhost:8080@evil.example.test/',
    'http://:pw@127.0.0.1:8080/',
    'file:///etc/passwd',
    'javascript:alert(1)',
    'data:text/html,x',
    'blob:http://127.0.0.1:8080/uuid',
    'about:blank',
    'not a url',
    '',
  ])('%s has no usable origin', (url) => expect(httpOrigin(url)).toBeUndefined())
})

describe('isServerOrigin: look-alikes never match', () => {
  it.each([
    ['http://127.0.0.1:8080/app', true],
    ['http://127.0.0.1:8080', true],
    ['http://127.0.0.1:8080/app?model=x#y', true],
    // The same host in other spellings is the same origin, as in Chromium.
    ['HTTP://127.0.0.1:8080/app', true],
    ['http://2130706433:8080/', true],
    ['http://0x7f.0.0.1:8080/', true],
    // The URL standard reads an IPv4 address with a trailing dot as the same address.
    ['http://127.0.0.1.:8080/app', true],
    // Everything else is a different origin.
    ['https://127.0.0.1:8080/app', false],
    ['http://127.0.0.1:8081/app', false],
    ['http://127.0.0.1/app', false],
    ['http://localhost:8080/app', false],
    ['http://[::1]:8080/app', false],
    ['http://127.0.0.2:8080/app', false],
    ['http://127.0.0.1@evil.example.test:8080/', false],
    ['http://127.0.0.1:8080@evil.example.test/', false],
    ['http://evil.example.test/http://127.0.0.1:8080/', false],
    ['http://127.0.0.1.evil.example.test:8080/', false],
    ['http://127.0.0.1%2eevil.example.test:8080/', false],
    ['http://user:pw@127.0.0.1:8080/', false],
    ['ws://127.0.0.1:8080/', false],
    ['blob:http://127.0.0.1:8080/abc', false],
    ['//127.0.0.1:8080/app', false],
    ['/app', false],
  ])('%s -> %s', (url, same) => expect(isServerOrigin(url, SERVER)).toBe(same))

  it.each([
    ['http://localhost:8080/', true],
    ['http://LOCALHOST:8080/', true],
    ['http://localhost.:8080/', false],
    ['http://localhost.evil.example.test:8080/', false],
    ['http://evil.example.test.localhost:8080/', false],
    ['http://localhost@evil.example.test:8080/', false],
    ['http://xn--localhost-:8080/', false],
    ['http://lоcalhost:8080/', false], // Cyrillic o
    ['http://127.0.0.1:8080/', false],
  ])('server localhost: %s -> %s', (url, same) => expect(isServerOrigin(url, LOCALHOST)).toBe(same))

  it('IPv6 server origins compare in canonical form', () => {
    const v6 = 'http://[::1]:8080'
    expect(isServerOrigin('http://[0000::1]:8080/app', v6)).toBe(true)
    expect(isServerOrigin('http://[::ffff:127.0.0.1]:8080/app', v6)).toBe(false)
    expect(isServerOrigin('http://[::1]:80/app', v6)).toBe(false)
  })
})

describe('decideNavigation, main frame', () => {
  it.each([
    ['http://127.0.0.1:8080/app/chat', 'allow'],
    ['http://127.0.0.1:8080/api/x?y=1', 'allow'],
    ['https://example.test/docs', 'external'],
    ['http://127.0.0.1:9999/', 'external'],
    ['http://localhost:8080/app', 'external'],
    ['mailto:someone@example.test', 'external'],
    ['javascript:alert(1)', 'deny'],
    ['JaVaScRiPt:alert(1)', 'deny'],
    ['file:///etc/passwd', 'deny'],
    ['file://127.0.0.1:8080/etc/passwd', 'deny'],
    ['data:text/html,<script>alert(1)</script>', 'deny'],
    ['blob:http://127.0.0.1:8080/uuid', 'deny'],
    ['blob:https://evil.example.test/uuid', 'deny'],
    ['about:blank', 'deny'],
    ['about:srcdoc', 'deny'],
    ['chrome://settings', 'deny'],
    ['devtools://devtools/bundled/inspector.html', 'deny'],
    ['view-source:http://127.0.0.1:8080/', 'deny'],
    ['ftp://example.test/file', 'deny'],
    ['ws://127.0.0.1:8080/', 'deny'],
    ['vscode://file/etc/passwd', 'deny'],
    ['ms-msdt:/id', 'deny'],
    ['zoommtg://example.test/join', 'deny'],
    ['tel:+100', 'deny'],
    ['sms:+100', 'deny'],
    ['intent://x#Intent;end', 'deny'],
    ['http://user:pw@evil.example.test/', 'deny'],
    ['http://127.0.0.1:8080@evil.example.test/', 'deny'],
    ['not a url', 'deny'],
    ['', 'deny'],
  ])('%s -> %s', (url, action) => expect(decideNavigation(url, SERVER).action).toBe(action))

  it('hands the normalised URL to the browser', () => {
    expect(decideNavigation('HTTPS://Example.TEST/a b', SERVER)).toEqual({ action: 'external', url: 'https://example.test/a%20b' })
  })
})

describe('decideNavigation, subframes', () => {
  const sub = { frame: 'sub' as const }
  it.each([
    ['http://127.0.0.1:8080/api/files/x.pdf', 'allow'],
    ['about:srcdoc', 'allow'],
    ['about:blank', 'allow'],
    ['blob:http://127.0.0.1:8080/0f0f', 'allow'],
    ['blob:https://evil.example.test/0f0f', 'deny'],
    ['blob:null/0f0f', 'deny'],
    ['https://evil.example.test/', 'deny'],
    ['http://localhost:8080/', 'deny'],
    ['mailto:a@example.test', 'deny'],
    ['data:text/html,x', 'deny'],
    ['javascript:alert(1)', 'deny'],
    ['file:///etc/passwd', 'deny'],
    ['about:blank#x', 'deny'],
    ['about:config', 'deny'],
    ['custom-proto://x', 'deny'],
  ])('%s -> %s', (url, action) => expect(decideNavigation(url, SERVER, sub).action).toBe(action))
})

describe('decideWindowOpen never creates a window', () => {
  it.each([
    ['http://127.0.0.1:8080/app/chat', 'same-view'],
    ['http://127.0.0.1:8080/app', 'same-view'],
    ['http://127.0.0.1:8080/application', 'external'],
    ['http://127.0.0.1:8080/swagger/index.html', 'external'],
    ['http://127.0.0.1:8080/auth/login?next=/app', 'external'],
    ['http://127.0.0.1:8080/', 'external'],
    ['https://example.test/docs', 'external'],
    ['mailto:a@example.test', 'external'],
    ['about:blank', 'none'],
    ['javascript:alert(1)', 'none'],
    ['file:///etc/passwd', 'none'],
    ['data:text/html,x', 'none'],
    ['blob:http://127.0.0.1:8080/x', 'none'],
    ['custom://x', 'none'],
    ['http://127.0.0.1:8080@evil.example.test/app', 'none'],
    ['', 'none'],
  ])('%s -> %s', (url, route) => expect(decideWindowOpen(url, SERVER).route).toBe(route))
})

describe('externalUrl', () => {
  it.each([
    ['https://example.test/x', 'https://example.test/x'],
    ['mailto:a@example.test?subject=x', 'mailto:a@example.test?subject=x'],
    ['http://u@example.test/', undefined],
    ['file:///x', undefined],
    ['javascript:x', undefined],
    ['smb://example.test/share', undefined],
    ['http:///nohost', 'http://nohost/'],
  ])('%s -> %s', (url, out) => expect(externalUrl(url)).toBe(out))
})

describe('permissions', () => {
  const req = (o: Partial<PermissionRequestInfo>): PermissionRequestInfo => ({
    permission: 'media',
    requestingUrl: `${SERVER}/app/chat`,
    topUrl: `${SERVER}/app/chat`,
    mediaTypes: ['audio'],
    userGesture: true,
    ...o,
  })

  it('asks for microphone and camera from the server after a user action', () => {
    expect(decidePermission(req({}), SERVER, {})).toBe('ask')
    expect(decidePermission(req({ mediaTypes: ['audio', 'video'] }), SERVER, {})).toBe('ask')
    expect(decidePermission(req({ mediaTypes: [] }), SERVER, {})).toBe('ask')
  })

  it('uses a remembered choice', () => {
    expect(decidePermission(req({}), SERVER, remember({}, SERVER, 'media', 'allow'))).toBe('allow')
    expect(decidePermission(req({}), SERVER, remember({}, SERVER, 'media', 'deny'))).toBe('deny')
    // A choice for another origin does not count.
    expect(decidePermission(req({}), SERVER, remember({}, LOCALHOST, 'media', 'allow'))).toBe('ask')
  })

  it('refuses without a user action, even with a remembered allow', () => {
    expect(decidePermission(req({ userGesture: false }), SERVER, remember({}, SERVER, 'media', 'allow'))).toBe('deny')
  })

  it('refuses other origins, iframes of other origins and look-alikes', () => {
    const allowed = remember({}, SERVER, 'media', 'allow')
    for (const url of [
      'https://evil.example.test/',
      'http://localhost:8080/app',
      'http://127.0.0.1:8080@evil.example.test/',
      'about:srcdoc',
      'data:text/html,x',
      'blob:http://127.0.0.1:8080/x',
      'file:///x',
    ]) {
      expect(decidePermission(req({ requestingUrl: url }), SERVER, allowed), url).toBe('deny')
      expect(decidePermission(req({ topUrl: url }), SERVER, allowed), url).toBe('deny')
    }
  })

  it('refuses screen capture through a media request', () => {
    expect(decidePermission(req({ mediaTypes: ['audio', 'desktop'] }), SERVER, {})).toBe('deny')
  })

  it('allows clipboard write after a user action only', () => {
    expect(decidePermission(req({ permission: 'clipboard-sanitized-write' }), SERVER, {})).toBe('allow')
    expect(decidePermission(req({ permission: 'clipboard-sanitized-write', userGesture: false }), SERVER, {})).toBe('deny')
    expect(decidePermission(req({ permission: 'clipboard-sanitized-write', requestingUrl: 'https://evil.example.test/' }), SERVER, {})).toBe('deny')
  })

  it.each([
    'clipboard-read',
    'deprecated-sync-clipboard-read',
    'notifications',
    'geolocation',
    'geolocation-approximate',
    'midi',
    'midiSysex',
    'usb',
    'serial',
    'hid',
    'display-capture',
    'fullscreen',
    'pointerLock',
    'keyboardLock',
    'openExternal',
    'fileSystem',
    'idle-detection',
    'local-fonts',
    'window-management',
    'storage-access',
    'top-level-storage-access',
    'persistent-storage',
    'mediaKeySystem',
    'sensors',
    'payment-handler',
    'local-network-access',
    'unknown',
    'MEDIA',
    '',
  ])('denies %s even for the server', (permission) => {
    const store = remember({}, SERVER, 'media', 'allow')
    expect(decidePermission(req({ permission }), SERVER, store)).toBe('deny')
    expect(decidePermissionCheck(permission, SERVER, `${SERVER}/app`, SERVER, store)).toBe(false)
  })

  it('checks pass only for a remembered allow or the clipboard write of the server', () => {
    const top = `${SERVER}/app`
    expect(decidePermissionCheck('media', SERVER, top, SERVER, {})).toBe(false)
    expect(decidePermissionCheck('media', SERVER, top, SERVER, remember({}, SERVER, 'media', 'allow'))).toBe(true)
    expect(decidePermissionCheck('media', 'https://evil.example.test', top, SERVER, remember({}, SERVER, 'media', 'allow'))).toBe(false)
    expect(decidePermissionCheck('clipboard-sanitized-write', SERVER, top, SERVER, {})).toBe(true)
    expect(decidePermissionCheck('clipboard-sanitized-write', SERVER, 'https://evil.example.test/', SERVER, {})).toBe(false)
    expect(decidePermissionCheck('clipboard-sanitized-write', 'null', top, SERVER, {})).toBe(false)
  })

  it('the store is plain data and can be reset', () => {
    let s = remember({}, SERVER, 'media', 'allow')
    s = remember(s, 'https://remote.example.test', 'media', 'deny')
    expect(JSON.parse(JSON.stringify(s))).toEqual(s)
    expect(recall(s, SERVER, 'media')).toBe('allow')
    expect(resetPermissions(s, SERVER)).toEqual({ 'https://remote.example.test': { media: 'deny' } })
    expect(resetPermissions(s)).toEqual({})
    expect(s[SERVER].media).toBe('allow') // not mutated
  })

  it('parsing drops anything malformed', () => {
    expect(parsePermissionStore(null)).toEqual({})
    expect(parsePermissionStore([])).toEqual({})
    expect(parsePermissionStore('x')).toEqual({})
    expect(
      parsePermissionStore({
        [SERVER]: { media: 'allow', geolocation: 'allow' },
        'http://127.0.0.1:8080/': { media: 'allow' },
        'file://': { media: 'allow' },
        __proto__: { media: 'allow' },
        'https://remote.example.test': { media: 'maybe' },
        'https://other.example.test': { media: 'deny' },
      }),
    ).toEqual({ [SERVER]: { media: 'allow' }, 'https://other.example.test': { media: 'deny' } })
  })

  it('recall ignores inherited names', () => {
    expect(recall({}, '__proto__', 'media')).toBeUndefined()
    expect(recall({}, 'constructor', 'media')).toBeUndefined()
  })

  it('a user action counts for five seconds', () => {
    expect(hasRecentGesture(undefined, 1000)).toBe(false)
    expect(hasRecentGesture(1000, 1000)).toBe(true)
    expect(hasRecentGesture(1000, 6000)).toBe(true)
    expect(hasRecentGesture(1000, 6001)).toBe(false)
    expect(hasRecentGesture(2000, 1000)).toBe(false)
  })
})

describe('sanitizeFileName', () => {
  it.each([
    ['report.pdf', 'report.pdf'],
    ['../../.bashrc', 'bashrc'],
    ['..\\..\\Windows\\system32\\evil.dll', 'evil.dll'],
    ['/etc/passwd', 'passwd'],
    ['~/.ssh/authorized_keys', 'authorized_keys'],
    ['.hidden', 'hidden'],
    ['...', 'download'],
    ['', 'download'],
    ['   ', 'download'],
    ['invoice‮fdp.exe', 'invoicefdp.exe'],
    ['a\u0000b.txt', 'ab.txt'],
    ['line\nbreak.txt', 'linebreak.txt'],
    ['zero​width.txt', 'zerowidth.txt'],
    ['con', '_con'],
    ['NUL.txt', '_NUL.txt'],
    ['what?.txt', 'what_.txt'],
    ['a<b>c:"d"|e*.txt', 'a_b_c__d__e_.txt'],
    ['trailing. . .', 'trailing'],
    ['model.gguf', 'model.gguf'],
  ])('%j -> %j', (input, out) => expect(sanitizeFileName(input)).toBe(out))

  it('caps the length and keeps a short extension', () => {
    const s = sanitizeFileName(`${'a'.repeat(500)}.json`)
    expect(s.length).toBe(120)
    expect(s.endsWith('.json')).toBe(true)
  })
})

describe('validateRemoteOrigin', () => {
  it.each([
    ['https://remote.example.test', 'https://remote.example.test', 'remote.example.test'],
    ['https://remote.example.test:8443/app/chat?x=1', 'https://remote.example.test:8443', 'remote.example.test:8443'],
    ['  http://192.168.1.20:8080/  ', 'http://192.168.1.20:8080', '192.168.1.20:8080'],
    ['http://10.0.0.5:8080', 'http://10.0.0.5:8080', '10.0.0.5:8080'],
    ['http://172.16.0.1:8080', 'http://172.16.0.1:8080', '172.16.0.1:8080'],
    ['http://100.100.1.2:8080', 'http://100.100.1.2:8080', '100.100.1.2:8080'],
    ['http://localhost:9090', 'http://localhost:9090', 'localhost:9090'],
    ['http://127.0.0.1:9090', 'http://127.0.0.1:9090', '127.0.0.1:9090'],
    ['http://[::1]:9090', 'http://[::1]:9090', '[::1]:9090'],
    ['http://[fd12::5]:8080', 'http://[fd12::5]:8080', '[fd12::5]:8080'],
    ['http://[fe80::1]:8080', 'http://[fe80::1]:8080', '[fe80::1]:8080'],
  ])('%s is accepted', (input, origin, host) => expect(validateRemoteOrigin(input)).toEqual({ ok: true, origin, host }))

  it.each([
    ['http://remote.example.test:8080', 'http://remote.example.test:8080'],
    ['http://8.8.8.8:8080', 'http://8.8.8.8:8080'],
    ['http://192.0.2.40:8080/x', 'http://192.0.2.40:8080'],
  ])('plain http to %s is accepted (the shell warns)', (input, origin) => expect(validateRemoteOrigin(input)).toMatchObject({ ok: true, origin }))

  it.each([
    ['https://user:pw@remote.example.test', 'credentials'],
    ['http://localhost@evil.example.test', 'credentials'],
    ['file:///etc/passwd', 'scheme'],
    ['javascript:alert(1)', 'scheme'],
    ['ftp://remote.example.test', 'scheme'],
    ['remote.example.test', 'invalid-url'],
    ['', 'invalid-url'],
    ['http://', 'invalid-url'],
    [`https://${'a'.repeat(3000)}.example.test`, 'invalid-url'],
  ])('%s is refused (%s)', (input, reason) => expect(validateRemoteOrigin(input)).toEqual({ ok: false, reason }))

  it('private host check', () => {
    expect(isLocalOrPrivateHost('127.255.0.1')).toBe(true)
    expect(isLocalOrPrivateHost('169.254.1.1')).toBe(true)
    expect(isLocalOrPrivateHost('100.63.0.1')).toBe(false)
    expect(isLocalOrPrivateHost('100.128.0.1')).toBe(false)
    expect(isLocalOrPrivateHost('nas.local')).toBe(false)
    expect(isLocalOrPrivateHost('[fec0::1]')).toBe(false)
  })
})

describe('partitionForOrigin', () => {
  it('is persistent, stable, different per origin and carries no host name', () => {
    const a = partitionForOrigin(SERVER)
    expect(a).toMatch(/^persist:webui-[0-9a-f]{24}$/)
    expect(partitionForOrigin(SERVER)).toBe(a)
    expect(partitionForOrigin(LOCALHOST)).not.toBe(a)
    expect(partitionForOrigin('https://remote.example.test')).not.toContain('example')
  })
})
