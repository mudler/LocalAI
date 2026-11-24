// Gives the WebUI view of the local server the API key as the server's own
// sign-in cookie, and takes it away again. No Electron import: index.ts passes
// the session of each origin's partition, so this is tested with fakes.
//
// The cookie is always a session cookie (no expiry). With secure storage the
// key outlives the launcher anyway, so nothing more is done. Without secure
// storage (no keyring) the key lives only in memory for this session, so the
// WebUI partition that got it must not keep it either: its cookies are
// cleared at quit, also any `token` cookie the server set by itself.

export interface CookieDetails {
  url: string
  name: string
  value: string
  path: string
  httpOnly: boolean
  sameSite: 'lax'
}

/** The part of an Electron session the hand-off uses. */
export interface CredentialSession {
  cookies: {
    set(details: CookieDetails): Promise<void>
    remove(url: string, name: string): Promise<void>
  }
  clearStorageData(options: { storages: 'cookies'[] }): Promise<void>
}

export interface WebuiCredentialDeps {
  /** The session of the WebUI partition of an origin. */
  session(origin: string): CredentialSession
  /** OS secure storage works (secretStore.ts `encryptionAvailable`). Asked at each hand-off. */
  secureStorage(): boolean
  /** Outcomes only, never the key. */
  log?(message: string): void
}

/** The cookie the server sets after a key sign-in. */
export const TOKEN_COOKIE = 'token'

/** What is set: the key as a session cookie for the server origin only. */
export function tokenCookie(origin: string, key: string): CookieDetails {
  // No expirationDate: Electron makes it a session cookie that is not kept after quit.
  return { url: origin, name: TOKEN_COOKIE, value: key, path: '/', httpOnly: true, sameSite: 'lax' }
}

export class WebuiCredentials {
  /** Origins whose partition got a key while there was no secure storage. */
  private clearAtQuitOrigins = new Set<string>()

  constructor(private readonly d: WebuiCredentialDeps) {}

  /** Sets the cookie for `origin`, or removes it when there is no key. */
  async give(origin: string, key: string | undefined): Promise<void> {
    const ses = this.d.session(origin)
    if (!key) {
      await ses.cookies.remove(origin, TOKEN_COOKIE)
      return
    }
    await ses.cookies.set(tokenCookie(origin, key))
    if (!this.d.secureStorage()) this.clearAtQuitOrigins.add(origin)
  }

  /** The origins whose cookies clearAtQuit removes. */
  pending(): string[] {
    return [...this.clearAtQuitOrigins]
  }

  /** At quit: clears the cookies of every WebUI partition that got a key without secure storage. */
  async clearAtQuit(): Promise<void> {
    for (const origin of this.clearAtQuitOrigins) {
      try {
        await this.d.session(origin).clearStorageData({ storages: ['cookies'] })
        this.d.log?.('webui: cookies cleared at quit (no secure storage)')
      } catch {
        this.d.log?.('webui: could not clear the cookies at quit')
      }
    }
    this.clearAtQuitOrigins.clear()
  }
}
