// The inputs of the WebUI policy fixture (test/fixtures/webui-policy.json).
// webuiPolicyFixture.test.ts runs each case through src/core/webuiPolicy.ts and
// stores the answer next to it. Add a case here, then regenerate the fixture.
import type { PermissionRequestInfo, PermissionStore } from '../src/core/webuiPolicy'

export interface NavigationCase {
  url: string
  origin: string
  frame: 'main' | 'sub'
}

export interface WindowOpenCase {
  url: string
  origin: string
}

export interface PermissionCase {
  request: PermissionRequestInfo
  origin: string
  store: PermissionStore
}

export interface FileNameCase {
  name: string
  fallback?: string
}

const DNS = 'https://ai.example.com'
const LAN = 'http://192.0.2.10:8080'
const LOOP = 'http://127.0.0.1:41873'

const main = (origin: string, url: string): NavigationCase => ({ url, origin, frame: 'main' })
const sub = (origin: string, url: string): NavigationCase => ({ url, origin, frame: 'sub' })

const navigation: NavigationCase[] = [
  // The server origin, written in the forms a browser treats as the same origin.
  main(DNS, 'https://ai.example.com/app/chat?model=a&q=b#bottom'),
  main(DNS, 'https://AI.Example.COM/app'),
  main(DNS, 'https://ai.example.com:443/app'),
  main(LAN, 'http://192.0.2.10:8080/app/models?q=x#top'),
  main(LOOP, 'http://127.0.0.1:41873/app'),
  main(LOOP, 'http://2130706433:41873/app'),
  // The same host on another port or scheme is another origin.
  main(DNS, 'https://ai.example.com:8443/app'),
  main(DNS, 'http://ai.example.com/app'),
  main(LAN, 'http://192.0.2.10/app'),
  main(LAN, 'http://192.0.2.10:8081/app'),
  main(LAN, 'https://192.0.2.10:8080/app'),
  main(LOOP, 'http://127.0.0.1:41874/app'),
  main(LOOP, 'http://localhost:41873/app'),
  // Other hosts open in the system browser.
  main(DNS, 'https://other.example.org/page?x=1'),
  main(DNS, 'http://other.example.org/page'),
  main(DNS, 'https://bücher.example/'),
  main(LAN, 'http://[2001:db8::1]:8080/app'),
  main(LOOP, 'http://[::1]:41873/app'),
  main(DNS, 'mailto:someone@example.com?subject=hi'),
  // Credentials in the URL are refused, on the server host or any other.
  main(DNS, 'https://user:secret@ai.example.com/app'),
  main(DNS, 'https://user@other.example.org/'),
  // Every other scheme is refused in the main frame.
  main(DNS, 'javascript:alert(1)'),
  main(DNS, 'data:text/html,<p>hi</p>'),
  main(DNS, 'file:///etc/hosts'),
  main(DNS, 'blob:https://ai.example.com/0b6c3f0e-1111-4222-8333-944445555666'),
  main(DNS, 'about:blank'),
  main(DNS, 'intent://scan/#Intent;scheme=zxing;package=com.example.scanner;end'),
  main(DNS, 'localai://pair?token=abc'),
  // Text that does not parse as a URL.
  main(DNS, 'http://'),
  main(LAN, 'not a url'),
  // Subframes: only blank, srcdoc, the server's own object URLs and server pages.
  sub(DNS, 'about:blank'),
  sub(DNS, 'about:srcdoc'),
  sub(DNS, 'blob:https://ai.example.com/0b6c3f0e-1111-4222-8333-944445555666'),
  sub(DNS, 'blob:https://other.example.org/0b6c3f0e-1111-4222-8333-944445555666'),
  sub(DNS, 'https://other.example.org/embed'),
  sub(DNS, 'https://ai.example.com/app/preview'),
  sub(LAN, 'blob:http://192.0.2.10:8080/0b6c3f0e-1111-4222-8333-944445555666'),
  sub(LAN, 'javascript:alert(1)'),
  sub(LOOP, 'data:text/html,<p>hi</p>'),
  sub(LOOP, 'mailto:someone@example.com'),
]

const windowOpen: WindowOpenCase[] = [
  { url: 'https://ai.example.com/app', origin: DNS },
  { url: 'https://ai.example.com/app/chat/1', origin: DNS },
  { url: 'https://ai.example.com/apps', origin: DNS },
  { url: 'https://ai.example.com/api/x', origin: DNS },
  { url: 'https://ai.example.com/swagger', origin: DNS },
  { url: 'https://other.example.org/docs', origin: DNS },
  { url: 'javascript:alert(1)', origin: DNS },
  { url: 'mailto:someone@example.com', origin: DNS },
  { url: 'http://192.0.2.10:8080/app/chat/2', origin: LAN },
  { url: 'http://127.0.0.1:41873/api/x', origin: LOOP },
  { url: 'https://user:secret@other.example.org/', origin: DNS },
  { url: 'not a url', origin: LOOP },
]

const page = `${DNS}/app/chat`
const media = (mediaTypes: readonly string[] | undefined, userGesture = true): PermissionRequestInfo =>
  mediaTypes === undefined
    ? { permission: 'media', requestingUrl: page, topUrl: page, userGesture }
    : { permission: 'media', requestingUrl: page, topUrl: page, mediaTypes, userGesture }

const permission: PermissionCase[] = [
  { request: media(['audio']), origin: DNS, store: {} },
  { request: media(['video']), origin: DNS, store: {} },
  { request: media(['audio', 'video']), origin: DNS, store: {} },
  { request: media(['screen']), origin: DNS, store: {} },
  { request: media(['audio', 'screen']), origin: DNS, store: { [DNS]: { media: 'allow' } } },
  { request: media([]), origin: DNS, store: {} },
  { request: media(undefined), origin: DNS, store: {} },
  { request: media(['audio'], false), origin: DNS, store: {} },
  { request: media(['audio'], false), origin: DNS, store: { [DNS]: { media: 'allow' } } },
  { request: media(['audio']), origin: DNS, store: { [DNS]: { media: 'allow' } } },
  { request: media(['audio']), origin: DNS, store: { [DNS]: { media: 'deny' } } },
  { request: media(['audio']), origin: DNS, store: { [LAN]: { media: 'allow' } } },
  {
    request: { permission: 'media', requestingUrl: 'https://other.example.org/embed', topUrl: page, mediaTypes: ['audio'], userGesture: true },
    origin: DNS,
    store: { [DNS]: { media: 'allow' } },
  },
  {
    request: { permission: 'media', requestingUrl: page, topUrl: 'https://other.example.org/', mediaTypes: ['audio'], userGesture: true },
    origin: DNS,
    store: { [DNS]: { media: 'allow' } },
  },
  {
    request: { permission: 'clipboard-sanitized-write', requestingUrl: `${LAN}/app`, topUrl: `${LAN}/app`, userGesture: true },
    origin: LAN,
    store: {},
  },
  {
    request: { permission: 'clipboard-sanitized-write', requestingUrl: `${LAN}/app`, topUrl: `${LAN}/app`, userGesture: false },
    origin: LAN,
    store: {},
  },
  {
    request: { permission: 'geolocation', requestingUrl: `${LOOP}/app`, topUrl: `${LOOP}/app`, userGesture: true },
    origin: LOOP,
    store: {},
  },
  {
    request: { permission: 'notifications', requestingUrl: `${LOOP}/app`, topUrl: `${LOOP}/app`, userGesture: true },
    origin: LOOP,
    store: {},
  },
]

const fileName: FileNameCase[] = [
  { name: 'model.gguf' },
  { name: 'a/b/c.txt' },
  { name: '..\\evil.exe' },
  { name: '.hidden' },
  { name: 'name. ' },
  { name: 'CON' },
  { name: 'lpt1.txt' },
  { name: 'invoice\u202Egpj.exe' },
  { name: 'bell\u0007\u0000tab\u0009.txt' },
  { name: 'zero\u200Bwidth\u2066iso\u2069.txt' },
  { name: 'a<b>:c|d?.txt' },
  { name: 'quote"star*.txt' },
  { name: `${'x'.repeat(200)}.gguf` },
  { name: 'y'.repeat(200) },
  { name: '' },
  { name: '', fallback: 'model.gguf' },
  { name: '   ' },
  { name: '...', fallback: 'file' },
]

export const CASES = { navigation, windowOpen, permission, fileName }
