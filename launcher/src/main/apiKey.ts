// The server's API key. No Electron import.
import crypto from 'node:crypto'

/** Makes a launcher key easy to recognise in a config or a leak report. */
export const API_KEY_PREFIX = 'lai-'
export const API_KEY_BYTES = 32

/**
 * A new key: 32 random bytes from the OS CSPRNG, URL-safe base64 without
 * padding, after the prefix. No comma, so the server's list parser reads it
 * as one key.
 */
export function generateApiKey(random: (n: number) => Buffer = crypto.randomBytes): string {
  const bytes = random(API_KEY_BYTES)
  if (bytes.length !== API_KEY_BYTES) throw new Error('random source returned the wrong length')
  return `${API_KEY_PREFIX}${bytes.toString('base64url')}`
}

/** What the screen shows in place of the key: the prefix (if any) and the last 4 characters. */
export function apiKeyHint(key: string): { prefix: string; last4: string } {
  const prefix = key.startsWith(API_KEY_PREFIX) ? API_KEY_PREFIX : ''
  // A short key (an adopted one) shows no characters at all.
  return { prefix, last4: key.length - prefix.length >= 16 ? key.slice(-4) : '' }
}

/**
 * Allows `max` reveals in `windowMs`, and none within `minGapMs` of the
 * previous one. A held-down button or a script in a compromised renderer
 * cannot read the key in a loop.
 */
export class RevealLimiter {
  private times: number[] = []
  constructor(
    private readonly max = 5,
    private readonly windowMs = 60_000,
    private readonly minGapMs = 1_000,
  ) {}

  take(now: number): boolean {
    this.times = this.times.filter((t) => now - t < this.windowMs && t <= now)
    const last = this.times.at(-1)
    if (last !== undefined && now - last < this.minGapMs) return false
    if (this.times.length >= this.max) return false
    this.times.push(now)
    return true
  }
}
