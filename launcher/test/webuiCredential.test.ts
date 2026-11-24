import { describe, expect, it } from 'vitest'
import { tokenCookie, WebuiCredentials, type CookieDetails, type CredentialSession } from '../src/main/webuiCredential'

const LOCAL = 'http://127.0.0.1:8080'
const KEY = 'lai-test-key-0123456789abcdef'

/** One fake session per origin: the cookies it holds and the clears it got. */
function fakeSessions() {
  const jars = new Map<string, { cookies: CookieDetails[]; clears: number }>()
  const jar = (origin: string) => {
    if (!jars.has(origin)) jars.set(origin, { cookies: [], clears: 0 })
    return jars.get(origin)!
  }
  const session = (origin: string): CredentialSession => ({
    cookies: {
      set: async (d) => void (jar(origin).cookies = [...jar(origin).cookies.filter((c) => c.name !== d.name), d]),
      remove: async (_url, name) => void (jar(origin).cookies = jar(origin).cookies.filter((c) => c.name !== name)),
    },
    clearStorageData: async (o) => {
      expect(o).toEqual({ storages: ['cookies'] })
      jar(origin).cookies = []
      jar(origin).clears++
    },
  })
  return { session, jar }
}

describe('WebUI token cookie', () => {
  it('is always a session cookie for the server origin only (no expiry)', () => {
    const c = tokenCookie(LOCAL, KEY)
    expect(c).toEqual({ url: LOCAL, name: 'token', value: KEY, path: '/', httpOnly: true, sameSite: 'lax' })
    expect('expirationDate' in c).toBe(false)
  })

  it('with secure storage: sets the cookie as today and clears nothing at quit', async () => {
    const f = fakeSessions()
    const logs: string[] = []
    const cr = new WebuiCredentials({ session: f.session, secureStorage: () => true, log: (m) => logs.push(m) })
    await cr.give(LOCAL, KEY)
    expect(f.jar(LOCAL).cookies.map((c) => c.name)).toEqual(['token'])
    expect(cr.pending()).toEqual([])
    await cr.clearAtQuit()
    expect(f.jar(LOCAL).clears).toBe(0)
    expect(f.jar(LOCAL).cookies).toHaveLength(1)
    expect(logs).toEqual([])
  })

  it('without secure storage: a session cookie, and the partition cookies are cleared at quit', async () => {
    const f = fakeSessions()
    const logs: string[] = []
    const cr = new WebuiCredentials({ session: f.session, secureStorage: () => false, log: (m) => logs.push(m) })
    await cr.give(LOCAL, KEY)
    expect(f.jar(LOCAL).cookies[0]).not.toHaveProperty('expirationDate')
    // A cookie the server set by itself in the same partition goes too.
    await f.session(LOCAL).cookies.set({ url: LOCAL, name: 'other', value: 'x', path: '/', httpOnly: true, sameSite: 'lax' })
    expect(cr.pending()).toEqual([LOCAL])
    await cr.clearAtQuit()
    expect(f.jar(LOCAL).clears).toBe(1)
    expect(f.jar(LOCAL).cookies).toEqual([])
    expect(cr.pending()).toEqual([])
    expect(logs.join('\n')).not.toContain(KEY)
  })

  it('no key removes the cookie; a failed clear at quit is logged, not thrown', async () => {
    const f = fakeSessions()
    const logs: string[] = []
    let broken = false
    const cr = new WebuiCredentials({
      session: (o) => (broken ? { ...f.session(o), clearStorageData: async () => Promise.reject(new Error('gone')) } : f.session(o)),
      secureStorage: () => false,
      log: (m) => logs.push(m),
    })
    await cr.give(LOCAL, KEY)
    await cr.give(LOCAL, undefined)
    expect(f.jar(LOCAL).cookies).toEqual([])
    broken = true
    await expect(cr.clearAtQuit()).resolves.toBeUndefined()
    expect(logs).toEqual(['webui: could not clear the cookies at quit'])
  })
})
