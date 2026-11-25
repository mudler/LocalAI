// The result of a server address check, shared by the desktop check and a
// client-only shell. Pure, no Electron or Node import.

export type TlsReason = 'expired' | 'not-yet-valid' | 'self-signed' | 'name-mismatch' | 'untrusted'
export type UnreachableReason = 'timeout' | 'refused' | 'dns' | 'network'

export type TestResult =
  /** It answered and it is LocalAI. `secure` is false for plain http. */
  | { status: 'ok'; version?: string; secure: boolean }
  /** It answered and asks for a sign-in (401, 403 or the WebUI login page). Still reachable. */
  | { status: 'needs-signin' }
  | { status: 'not-localai'; reason: 'answer' | 'redirect' }
  | { status: 'tls'; reason: TlsReason }
  | { status: 'unreachable'; reason: UnreachableReason }
  | { status: 'cancelled' }
