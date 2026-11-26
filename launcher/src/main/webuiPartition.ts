// The session partition of the WebUI view. Node only (node:crypto), no
// Electron import, so the pure tests can load it.
import { createHash } from 'node:crypto'

/**
 * The persistent session partition of the view for one origin. Cookies,
 * storage and logins of each server stay apart, and apart from the shell.
 * A hash, so the partition name never carries a host name to disk.
 */
export function partitionForOrigin(origin: string): string {
  return `persist:webui-${createHash('sha256').update(origin).digest('hex').slice(0, 24)}`
}
