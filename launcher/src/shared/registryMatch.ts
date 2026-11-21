// The check of a registry credential's `match`, shared by the main process
// (which enforces it) and the renderer (which shows the message early).
export const MAX_MATCH = 255

const HOST = String.raw`(?:[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?|\[[0-9A-Fa-f:.]+\])`
const MATCH = new RegExp(String.raw`^(?:https?://)?${HOST}(?::[0-9]{1,5})?(?:/[A-Za-z0-9._~!$&'()*+,;=:%/-]*)?$`)

/** A plain message when `match` is not accepted, else undefined. Same shape the server takes. */
export function matchProblem(match: string): string | undefined {
  if (typeof match !== 'string' || match.trim() === '') return 'Enter a registry, for example registry.example.'
  if (match.length > MAX_MATCH) return 'The registry is too long.'
  if (/\s/.test(match)) return 'The registry cannot contain spaces.'
  if (match.includes('@')) return 'Leave out the user name. Use the user name field.'
  if (match.includes('?') || match.includes('#')) return 'The registry cannot have a query or a fragment.'
  if (!MATCH.test(match)) return 'Use a host with an optional port and path, for example registry.example:5000/team.'
  return undefined
}
