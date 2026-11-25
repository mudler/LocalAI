// The inputs of the WebUI policy fixture (test/fixtures/webui-policy.json).
// webuiPolicyFixture.test.ts runs each case through src/core/webuiPolicy.ts and
// stores the answer next to it. Add a case here, then regenerate the fixture.
import type { PermissionRequestInfo, PermissionStore } from '../src/core/webuiPolicy'

export interface NavigationCase {
  url: string
  origin: string
  frame: 'main' | 'sub'
  /** Origins an OAuth/OIDC sign-in allows for the main frame (NavigationContext.allowedOrigins). */
  allowedOrigins?: string[]
}

export interface OAuthLoginCase {
  url: string
  origin: string
}

/**
 * One event of an OAuth/OIDC sign-in, as the view reports it to OAuthSignInFlow:
 * navigate (a navigation the page starts), redirect (a server redirect),
 * request (the network check of a frame document), load (a page finished
 * loading, `url` is the view's URL), loadFailed (the main frame failed to
 * load) and reset (the launcher navigates, or the view closes). `at` is the
 * time in milliseconds.
 */
export interface OAuthFlowStep {
  event: 'navigate' | 'redirect' | 'request' | 'load' | 'loadFailed' | 'reset'
  at: number
  url?: string
  frame?: 'main' | 'sub'
}

export interface OAuthFlowCase {
  name: string
  origin: string
  steps: OAuthFlowStep[]
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
const IDP = 'https://idp.example.net'

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
  // Origins a sign-in allows: the main frame only, never a subframe; the
  // credential and scheme rules still apply.
  { ...main(DNS, 'https://idp.example.net/authorize?client_id=a&state=b'), allowedOrigins: [IDP] },
  { ...main(DNS, 'https://IDP.example.net:443/login'), allowedOrigins: [IDP] },
  { ...main(DNS, 'https://idp.example.net:8443/login'), allowedOrigins: [IDP] },
  { ...main(DNS, 'http://idp.example.net/login'), allowedOrigins: [IDP] },
  { ...main(DNS, 'https://other.example.org/page'), allowedOrigins: [IDP] },
  { ...main(DNS, 'https://user:secret@idp.example.net/login'), allowedOrigins: [IDP] },
  { ...main(DNS, 'javascript:alert(1)'), allowedOrigins: [IDP] },
  { ...main(DNS, 'https://sso.example.org/saml'), allowedOrigins: [IDP, 'https://sso.example.org'] },
  { ...main(DNS, 'https://ai.example.com/app'), allowedOrigins: [IDP] },
  { ...main(DNS, 'https://idp.example.net/login'), allowedOrigins: [] },
  { ...sub(DNS, 'https://idp.example.net/login'), allowedOrigins: [IDP] },
  { ...sub(DNS, 'about:srcdoc'), allowedOrigins: [IDP] },
]

// The server routes that start an OAuth/OIDC sign-in, and look-alikes that do not.
const oauthLogin: OAuthLoginCase[] = [
  { url: 'https://ai.example.com/api/auth/oidc/login', origin: DNS },
  { url: 'https://ai.example.com/api/auth/oidc/login?invite_code=abc', origin: DNS },
  { url: 'https://ai.example.com/api/auth/github/login', origin: DNS },
  { url: 'https://AI.example.com:443/api/auth/github/login#x', origin: DNS },
  { url: 'http://192.0.2.10:8080/api/auth/oidc/login', origin: LAN },
  { url: 'https://ai.example.com/api/auth/oidc/callback?code=1&state=2', origin: DNS },
  { url: 'https://ai.example.com/api/auth/login', origin: DNS },
  { url: 'https://ai.example.com/api/auth/oidc/login/', origin: DNS },
  { url: 'https://ai.example.com/API/auth/oidc/login', origin: DNS },
  { url: 'https://ai.example.com/prefix/api/auth/oidc/login', origin: DNS },
  { url: 'https://ai.example.com/api/auth/x/../oidc/login', origin: DNS },
  { url: 'https://idp.example.net/api/auth/oidc/login', origin: DNS },
  { url: 'https://ai.example.com:8443/api/auth/oidc/login', origin: DNS },
  { url: 'https://user@ai.example.com/api/auth/oidc/login', origin: DNS },
  { url: 'javascript:location="/api/auth/oidc/login"', origin: DNS },
  { url: 'not a url', origin: DNS },
]

const IDP_AUTHORIZE = `${IDP}/authorize?client_id=localai&state=s1&redirect_uri=https%3A%2F%2Fai.example.com%2Fapi%2Fauth%2Foidc%2Fcallback`
const CALLBACK = `${DNS}/api/auth/oidc/callback?code=c1&state=s1`
const nav = (at: number, url: string, frame: 'main' | 'sub' = 'main'): OAuthFlowStep => ({ event: 'navigate', at, url, frame })
const redirect = (at: number, url: string, frame: 'main' | 'sub' = 'main'): OAuthFlowStep => ({ event: 'redirect', at, url, frame })
const request = (at: number, url: string, frame: 'main' | 'sub' = 'main'): OAuthFlowStep => ({ event: 'request', at, url, frame })
const load = (at: number, url: string): OAuthFlowStep => ({ event: 'load', at, url })

const oauthFlow: OAuthFlowCase[] = [
  {
    name: 'OIDC sign-in completes inside the view',
    origin: DNS,
    steps: [
      nav(0, `${DNS}/login`),
      load(10, `${DNS}/login`),
      nav(100, `${DNS}/api/auth/oidc/login?invite_code=abc`),
      redirect(200, IDP_AUTHORIZE),
      request(210, IDP_AUTHORIZE),
      load(300, IDP_AUTHORIZE),
      nav(5_000, `${IDP}/login-actions/authenticate?session=1`),
      request(5_010, `${IDP}/login-actions/authenticate?session=1`),
      redirect(5_100, CALLBACK),
      request(5_110, CALLBACK),
      load(5_200, `${DNS}/app`),
      nav(6_000, `${IDP}/account`),
      request(6_010, `${IDP}/account`),
    ],
  },
  {
    name: 'GitHub sign-in through federated hops, at most five origins',
    origin: DNS,
    steps: [
      nav(0, `${DNS}/api/auth/github/login`),
      redirect(10, 'https://github.com/login/oauth/authorize?client_id=a&state=s'),
      redirect(20, 'https://github.com/login?return_to=%2Flogin%2Foauth%2Fauthorize'),
      redirect(30, 'https://sso1.example.org/saml'),
      redirect(40, 'https://sso2.example.org/saml'),
      redirect(50, 'https://sso3.example.org/saml'),
      redirect(60, 'https://sso4.example.org/saml'),
      redirect(70, 'https://sso5.example.org/saml'),
      nav(80, 'https://sso2.example.org/next'),
      nav(90, 'https://links.example.org/help'),
      redirect(100, 'http://github.com/login'),
      redirect(110, `${DNS}/api/auth/github/callback?code=c&state=s`),
      redirect(120, 'https://sso1.example.org/saml'),
    ],
  },
  {
    name: 'a link the IdP page follows adds no origin',
    origin: LAN,
    steps: [
      nav(0, 'http://192.0.2.10:8080/api/auth/oidc/login'),
      redirect(10, 'https://idp.example.net/auth'),
      nav(20, 'https://help.example.org/'),
      request(30, 'https://help.example.org/'),
      nav(40, 'https://idp.example.net/auth?retry=1'),
    ],
  },
  {
    name: 'subframes stay strict during sign-in',
    origin: DNS,
    steps: [
      nav(0, `${DNS}/api/auth/oidc/login`),
      redirect(10, IDP_AUTHORIZE),
      nav(20, `${IDP}/embed`, 'sub'),
      redirect(30, `${IDP}/embed`, 'sub'),
      request(40, `${IDP}/embed`, 'sub'),
      redirect(50, 'https://frames.example.org/x', 'sub'),
      nav(60, 'about:srcdoc', 'sub'),
      request(70, IDP_AUTHORIZE),
    ],
  },
  {
    name: 'a subframe to the login route does not start a sign-in',
    origin: DNS,
    steps: [nav(0, `${DNS}/api/auth/oidc/login`, 'sub'), redirect(10, IDP_AUTHORIZE, 'sub'), redirect(20, IDP_AUTHORIZE)],
  },
  {
    name: 'other schemes and credentials never start a sign-in',
    origin: DNS,
    steps: [
      nav(0, `${DNS}/api/auth/oidc/login`),
      redirect(10, 'javascript:alert(1)'),
      redirect(20, IDP_AUTHORIZE),
      nav(30, `${DNS}/api/auth/oidc/login`),
      redirect(40, 'https://user:pw@idp.example.net/authorize'),
      redirect(50, IDP_AUTHORIZE),
      nav(60, `${DNS}/api/auth/oidc/login`),
      redirect(70, 'localai://callback?code=1'),
      redirect(80, IDP_AUTHORIZE),
    ],
  },
  {
    name: 'the pending state ends after its timeout',
    origin: DNS,
    steps: [nav(0, `${DNS}/api/auth/oidc/login`), redirect(30_001, IDP_AUTHORIZE)],
  },
  {
    name: 'the pending state lasts its full timeout',
    origin: DNS,
    steps: [nav(0, `${DNS}/api/auth/oidc/login`), redirect(30_000, IDP_AUTHORIZE)],
  },
  {
    name: 'the active state ends after its timeout',
    origin: DNS,
    steps: [
      nav(0, `${DNS}/api/auth/oidc/login`),
      redirect(1_000, IDP_AUTHORIZE),
      request(601_000, `${IDP}/login`),
      request(601_001, `${IDP}/login`),
      redirect(601_002, `${IDP}/login`),
    ],
  },
  {
    name: 'a cross-origin link while pending cancels the sign-in',
    origin: DNS,
    steps: [nav(0, `${DNS}/api/auth/oidc/login`), nav(10, 'https://other.example.org/'), redirect(20, IDP_AUTHORIZE)],
  },
  {
    name: 'a login route that renders a page cancels the sign-in',
    origin: DNS,
    steps: [nav(0, `${DNS}/api/auth/oidc/login`), load(10, `${DNS}/api/auth/oidc/login`), redirect(20, IDP_AUTHORIZE)],
  },
  {
    name: 'a login route that fails to load cancels the sign-in',
    origin: DNS,
    steps: [nav(0, `${DNS}/api/auth/github/login`), { event: 'loadFailed', at: 10 }, redirect(20, 'https://github.com/login/oauth/authorize')],
  },
  {
    name: 'the login route sent back to the server cancels the sign-in',
    origin: DNS,
    steps: [nav(0, `${DNS}/api/auth/oidc/login`), redirect(10, `${DNS}/login?error=oidc`), redirect(20, IDP_AUTHORIZE)],
  },
  {
    name: 'a server redirect to the login route starts a sign-in',
    origin: DNS,
    steps: [nav(0, `${DNS}/app/start-sso`), redirect(10, `${DNS}/api/auth/oidc/login`), redirect(20, IDP_AUTHORIZE)],
  },
  {
    name: 'a reset ends an active sign-in',
    origin: DNS,
    steps: [nav(0, `${DNS}/api/auth/oidc/login`), redirect(10, IDP_AUTHORIZE), { event: 'reset', at: 20 }, request(30, IDP_AUTHORIZE)],
  },
  {
    name: 'an IdP page load keeps the sign-in, a failed IdP load too',
    origin: DNS,
    steps: [
      nav(0, `${DNS}/api/auth/oidc/login`),
      redirect(10, IDP_AUTHORIZE),
      load(20, IDP_AUTHORIZE),
      { event: 'loadFailed', at: 30 },
      request(40, IDP_AUTHORIZE),
    ],
  },
  {
    name: 'a login route on another origin does not start a sign-in',
    origin: DNS,
    steps: [nav(0, `${IDP}/api/auth/oidc/login`), redirect(10, 'https://idp2.example.net/authorize')],
  },
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

export const CASES = { navigation, windowOpen, permission, fileName, oauthLogin, oauthFlow }
