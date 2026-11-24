// Pure rules of the server screens: the live message under the address field,
// the suggested name, the text of a test result and the rows of the switcher.
// No React, no bridge. Tested in test/serversUi.test.ts.
import type { ServerRowView, TestResultView } from '../shared/types'
import { S } from './strings'

export type HintKind = 'fix' | 'note' | 'error' | 'warn' | 'ok'

export interface AddressHint {
  kind: HintKind
  /** A bold lead sentence (the insecure warning). */
  title?: string
  message: string
  /** One click that fixes the text: the new text for the field. */
  action?: { label: string; apply: string }
  /** The address is a saved server: offer to edit it. */
  editId?: string
}

const SCHEME = /^[a-z][a-z0-9+.-]*:\/\//i

function isLoopback(host: string): boolean {
  const h = host.replace(/^\[|\]$/g, '').toLowerCase()
  return h === 'localhost' || h === '::1' || /^127(\.\d{1,3}){3}$/.test(h)
}

/** What the live message says about the text in the address field. */
export function addressHint(raw: string, servers: ServerRowView[], editingId?: string): AddressHint | undefined {
  const text = raw.trim()
  if (text === '') return undefined
  if (/\s/.test(text)) return { kind: 'error', message: S.hint.space }
  if (!SCHEME.test(text)) {
    // "user:pw@host" and "host:8080" have no scheme either; offer https for a plain host name.
    const plain = /^[A-Za-z0-9.[\]:-]+(\/.*)?$/.test(text) && !text.includes('@')
    return { kind: 'fix', message: S.hint.fixScheme, ...(plain ? { action: { label: S.hint.useHttps(text.replace(/\/.*$/, '')), apply: `https://${text.replace(/\/.*$/, '')}` } } : {}) }
  }
  let u: URL
  try {
    u = new URL(text)
  } catch {
    return { kind: 'error', message: S.hint.invalid }
  }
  if (u.protocol !== 'http:' && u.protocol !== 'https:') return { kind: 'fix', message: S.hint.fixScheme }
  if (u.hostname === '') return { kind: 'error', message: S.hint.invalid }
  if (u.username !== '' || u.password !== '') return { kind: 'error', message: S.hint.credentials }
  if ((u.pathname !== '/' && u.pathname !== '') || u.search !== '' || u.hash !== '') {
    return { kind: 'note', message: S.hint.useOrigin(u.origin), action: { label: S.hint.useIt, apply: u.origin } }
  }
  const dup = servers.find((r) => r.kind === 'remote' && r.origin === u.origin && r.id !== editingId)
  if (dup) return { kind: 'note', message: S.hint.duplicate(dup.name), editId: dup.id }
  if (u.protocol === 'http:') {
    if (isLoopback(u.hostname)) return { kind: 'note', message: S.hint.local }
    return {
      kind: 'warn',
      title: S.hint.insecureTitle,
      message: S.hint.insecureBody,
      action: { label: S.hint.useHttpsInstead, apply: `https://${u.host}` },
    }
  }
  return { kind: 'ok', message: S.hint.ok }
}

/** The text can go to Test or Connect: no error, no fix needed. */
export function addressUsable(hint: AddressHint | undefined, raw: string): boolean {
  return raw.trim() !== '' && !!hint && (hint.kind === 'ok' || hint.kind === 'warn' || hint.kind === 'note') && !hint.editId
}

/** "office-gpu.example.com" -> "Office gpu". The name is only a suggestion. */
export function suggestName(address: string): string {
  let host = address.trim()
  try {
    host = new URL(SCHEME.test(host) ? host : `https://${host}`).hostname
  } catch {
    return ''
  }
  const ip = /^\d{1,3}(\.\d{1,3}){3}$/.test(host) || host.startsWith('[')
  const label = ip ? host : (host.split('.')[0] ?? host)
  const words = label.replace(/[-_]+/g, ' ').trim()
  return (words.charAt(0).toUpperCase() + words.slice(1)).slice(0, 60)
}

export interface TestCardView {
  tone: 'ok' | 'error' | 'warn'
  icon: string
  title: string
  body: string
  next?: string
  actions: ('again' | 'details' | 'edit')[]
}

/** The card under the form after Test: what happened, why, and one next step. */
export function testCard(r: TestResultView, host: string): TestCardView | undefined {
  switch (r.status) {
    case 'ok':
      return { tone: 'ok', icon: 'check-circle', title: S.testResult.okTitle(r.version), body: S.testResult.okBody(host, r.secure), actions: ['again'] }
    case 'needs-signin':
      return { tone: 'warn', icon: 'alert-circle', title: S.testResult.signinTitle, body: S.testResult.signinBody, next: S.testResult.signinNext, actions: [] }
    case 'not-localai':
      return {
        tone: 'error',
        icon: 'alert-circle',
        title: S.testResult.notLocalTitle,
        body: r.reason === 'redirect' ? S.testResult.notLocalRedirect : S.testResult.notLocalBody,
        next: S.testResult.notLocalNext,
        actions: ['edit'],
      }
    case 'tls':
      return { tone: 'error', icon: 'lock', title: S.testResult.tlsTitle, body: `${S.testResult.tls[r.reason]} ${S.testResult.tlsNoSkip}`, next: S.testResult.tlsNext, actions: ['again', 'details'] }
    case 'unreachable':
      return { tone: 'error', icon: 'alert-circle', title: S.testResult.unreachableTitle, body: S.testResult.unreachable[r.reason], next: S.testResult.unreachableNext, actions: ['again'] }
    case 'cancelled':
      return undefined
  }
}

/** The dot colour of a status. Colour is never the only cue: the text is always there. */
export function statusTone(s: ServerRowView['status'], insecure: boolean): 'ok' | 'warn' | 'error' | 'idle' | 'accent' {
  switch (s) {
    case 'running':
    case 'connected':
      return insecure ? 'warn' : 'ok'
    case 'needs-signin':
    case 'problem':
      return 'warn'
    case 'unreachable':
      return 'error'
    case 'checking':
      return 'accent'
    default:
      return insecure ? 'warn' : 'idle'
  }
}

/** The second line of a switcher row. */
export function switcherLine(row: ServerRowView, lastCheck?: (ts: number) => string): string {
  if (row.kind === 'local') return row.status === 'running' ? S.switcher.thisRunning(row.host) : S.switcher.thisNotRunning
  if (row.insecure && row.status !== 'unreachable') return S.switcher.remoteInsecure(row.host)
  switch (row.status) {
    case 'unreachable':
      return S.switcher.unreachableAt(row.checkedAt && lastCheck ? lastCheck(row.checkedAt) : '')
    case 'needs-signin':
      return S.switcher.remoteNeedsSignin(row.host)
    case 'not-tested':
    case 'checking':
      return S.switcher.remoteNotTested(row.host)
    default:
      return S.switcher.remoteConnected(row.host)
  }
}
