// The checks of a registry credential rule, shared by the main process (which
// enforces them, on save and again when launcher.json is read) and the
// renderer (which shows the message early). One set of limits for every layer.

/** The longest `match`: the longest DNS name. */
export const MAX_MATCH = 253
/** The longest user name of a basic rule. */
export const MAX_USERNAME = 128

const HOST = String.raw`(?:[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?|\[[0-9A-Fa-f:.]+\])`
const MATCH = new RegExp(String.raw`^(?:https://)?${HOST}(?::[0-9]{1,5})?(?:/[A-Za-z0-9._~!$&'()*+,;=:%/-]*)?$`, 'i')

/** A plain message when `match` is not accepted, else undefined. Same shape the server takes. */
export function matchProblem(match: string): string | undefined {
  if (typeof match !== 'string' || match.trim() === '') return 'Enter a registry, for example registry.example.'
  if (match.length > MAX_MATCH) return 'The registry is too long.'
  if (/\s/.test(match)) return 'The registry cannot contain spaces.'
  // The server sends a credential over plain http only to a rule that allows it,
  // and the launcher does not write such rules: an http rule would never be used.
  if (/^http:\/\//i.test(match)) return 'Use a registry address without http://; plain http is not supported.'
  if (match.includes('@')) return 'Leave out the user name. Use the user name field.'
  if (match.includes('?') || match.includes('#')) return 'The registry cannot have a query or a fragment.'
  if (!MATCH.test(match)) return 'Use a host with an optional port and path, for example registry.example:5000/team.'
  return undefined
}

/** The host and port of a match, lower case: the part that decides where a secret is sent. */
export function matchHost(match: string): string {
  return match.trim().replace(/^https?:\/\//i, '').split('/')[0].toLowerCase()
}

/**
 * True when an edit from (kind, match) to (kind, match) cannot keep the stored
 * secret: a password must not turn into a token or go to another host.
 */
export function needsNewSecret(before: { kind: string; match: string }, after: { kind: string; match: string }): boolean {
  return before.kind !== after.kind || matchHost(before.match) !== matchHost(after.match)
}

// Characters the server's YAML reader refuses (C0 and C1 controls, DEL, the
// non-characters U+FFFE and U+FFFF) or changes (U+0085 becomes a space). A
// refused character would stop the server at startup. The colon separates the
// user name from the password in basic auth.
const BAD_USERNAME = /[\u0000-\u001f\u007f-\u009f\ufffe\uffff:]/
/** A high surrogate without a low one after it, or a low one without a high one before it. */
const LONE_SURROGATE = /[\ud800-\udbff](?![\udc00-\udfff])|(?<![\ud800-\udbff])[\udc00-\udfff]/

/** A plain message when a basic rule's user name is not accepted, else undefined. */
export function usernameProblem(username: unknown): string | undefined {
  if (typeof username !== 'string' || username.trim() === '') return 'Enter a user name.'
  if (username.length > MAX_USERNAME || BAD_USERNAME.test(username) || LONE_SURROGATE.test(username)) return 'The user name is not valid.'
  return undefined
}
