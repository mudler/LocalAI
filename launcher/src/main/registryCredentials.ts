// Registry credential rules: which registry, basic or bearer, and the secret.
// No Electron import. Rule metadata lives in launcher.json (configStore.ts);
// the secret lives in the secret store under `cred:<id>` and never leaves it:
// list() reports only whether one is set, and error text never quotes input.
import crypto from 'node:crypto'
import type { RegistryCredentialKind, RegistryCredentialRule, RegistryCredentialView } from '../shared/types'
import { MAX_ENV_VALUE } from '../shared/envVars'
import { registryCredentials, withRegistryCredentials, type StoredRegistryCredential } from './configStore'
import type { LauncherConfig } from './config'
import { credSecret } from './secretStore'

export const MAX_MATCH = 255
export const MAX_USERNAME = 256
export const MAX_RULES = 64
export const CRED_ENV_PREFIX = 'LOCALAI_LAUNCHER_CRED_'

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

/** One form of a match for comparison: no scheme, lower case, no trailing slash. */
function matchKey(match: string): string {
  return match.trim().replace(/^https?:\/\//i, '').replace(/\/+$/, '').toLowerCase()
}

/** The part of the config store this class uses. */
export interface ConfigStoreLike {
  get(): LauncherConfig
  update(change: (c: LauncherConfig) => LauncherConfig): Promise<LauncherConfig>
}

/** The part of the secret store this class uses. */
export interface SecretStoreLike {
  has(name: string): boolean
  get(name: string): string | undefined
  set(name: string, value: string): Promise<unknown>
  delete(name: string): Promise<void>
}

export type SaveInput = Omit<RegistryCredentialRule, 'id'> & { id?: string; secret?: string }
export type SaveResult = { ok: true; id: string } | { ok: false; reason: string }

export class RegistryCredentials {
  constructor(
    private readonly config: ConfigStoreLike,
    private readonly secrets: SecretStoreLike,
  ) {}

  private rules(): StoredRegistryCredential[] {
    return registryCredentials(this.config.get())
  }

  list(): RegistryCredentialView[] {
    return this.rules().map((r) => {
      const view: RegistryCredentialView = { id: r.id, match: r.match, kind: r.kind, secretSet: this.secrets.has(credSecret(r.id)) }
      if (r.username !== undefined) view.username = r.username
      return view
    })
  }

  async save(input: SaveInput): Promise<SaveResult> {
    const kind: unknown = input.kind
    if (kind !== 'basic' && kind !== 'bearer') return { ok: false, reason: 'Choose basic or bearer.' }
    const match = typeof input.match === 'string' ? input.match.trim() : ''
    const problem = matchProblem(match)
    if (problem) return { ok: false, reason: problem }
    const username = kind === 'basic' ? (typeof input.username === 'string' ? input.username : '') : ''
    if (kind === 'basic') {
      if (username.trim() === '') return { ok: false, reason: 'Enter a user name.' }
      if (username.length > MAX_USERNAME || /[\u0000-\u001f\u007f:]/.test(username)) return { ok: false, reason: 'The user name is not valid.' }
    }
    const secret = input.secret
    if (secret !== undefined && (typeof secret !== 'string' || secret === '' || secret.length > MAX_ENV_VALUE || secret.includes('\u0000'))) {
      return { ok: false, reason: 'The secret is not valid.' }
    }
    if (input.id !== undefined && !this.rules().some((r) => r.id === input.id)) return { ok: false, reason: 'That rule does not exist.' }

    const out: { r: SaveResult } = { r: { ok: false, reason: 'not saved' } }
    await this.config.update((c) => {
      const rules = registryCredentials(c)
      const existing = input.id !== undefined ? rules.find((r) => r.id === input.id) : undefined
      if (!existing && rules.length >= MAX_RULES) {
        out.r = { ok: false, reason: 'There are too many rules.' }
        return c
      }
      if (rules.some((r) => r.id !== existing?.id && matchKey(r.match) === matchKey(match))) {
        out.r = { ok: false, reason: 'A rule for this registry already exists.' }
        return c
      }
      const id = existing?.id ?? crypto.randomBytes(8).toString('hex')
      const index = existing?.index ?? rules.reduce((m, r) => Math.max(m, r.index), 0) + 1
      const stored: StoredRegistryCredential = { id, match, kind, index }
      if (kind === 'basic') stored.username = username
      const next = existing ? rules.map((r) => (r.id === id ? stored : r)) : [...rules, stored]
      out.r = { ok: true, id }
      return withRegistryCredentials(c, next)
    })
    const result = out.r
    if (!result.ok) return result
    // No new secret on an edit: the stored one stays.
    if (secret !== undefined) {
      try {
        await this.secrets.set(credSecret(result.id), secret)
      } catch {
        return { ok: false, reason: 'The secret could not be stored.' }
      }
    }
    return result
  }

  async remove(id: string): Promise<void> {
    await this.config.update((c) => withRegistryCredentials(c, registryCredentials(c).filter((r) => r.id !== id)))
    await this.secrets.delete(credSecret(id))
  }

  /** The environment variable that carries the secret of rule `id`. Stable, and does not name the registry. */
  secretEnvName(id: string): string {
    const r = this.rules().find((x) => x.id === id)
    if (!r) throw new Error('unknown rule')
    return `${CRED_ENV_PREFIX}${r.index}`
  }

  async secretFor(id: string): Promise<string | undefined> {
    return this.secrets.get(credSecret(id))
  }
}

export type { RegistryCredentialKind }
