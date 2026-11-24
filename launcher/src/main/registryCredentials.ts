// Registry credential rules: which registry, basic or bearer, and the secret.
// No Electron import. Rule metadata lives in launcher.json (configStore.ts);
// the secret lives in the secret store under `cred:<id>` and never leaves it:
// list() reports only whether one is set, and error text never quotes input.
import crypto from 'node:crypto'
import type { SecretBackend, RegistryCredentialKind, RegistryCredentialRule, RegistryCredentialView } from '../shared/types'
import { MAX_ENV_VALUE } from '../shared/envVars'
import { MAX_MATCH, MAX_USERNAME, matchProblem, needsNewSecret, usernameProblem } from '../shared/registryMatch'
import { registryCredentials, withRegistryCredentials, type StoredRegistryCredential } from './configStore'
import type { LauncherConfig } from './config'
import { credSecret, type SetResult } from './secretStore'

export const MAX_RULES = 64
export const CRED_ENV_PREFIX = 'LOCALAI_LAUNCHER_CRED_'

export { MAX_MATCH, MAX_USERNAME, matchProblem, usernameProblem }

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
  set(name: string, value: string): Promise<SetResult>
  /** Where a new value goes: `session` means memory only. */
  backend(): SecretBackend
  delete(name: string): Promise<void>
  /** Drops the in-memory copy of `name` only; a stored value stays. */
  forgetSession(name: string): Promise<void>
}

/** Same advice as the secret storage notice of the Settings screen. */
const NO_STORAGE = 'Your system has no keyring, so this secret cannot be kept. To keep it, turn on "Keep secrets in a file" in Settings, Advanced.'

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
      const bad = usernameProblem(username)
      if (bad) return { ok: false, reason: bad }
    }
    const secret = input.secret
    if (secret !== undefined && (typeof secret !== 'string' || secret === '' || secret.length > MAX_ENV_VALUE || secret.includes('\u0000'))) {
      return { ok: false, reason: 'The secret is not valid.' }
    }
    const existing = input.id !== undefined ? this.rules().find((r) => r.id === input.id) : undefined
    if (input.id !== undefined && !existing) return { ok: false, reason: 'That rule does not exist.' }
    // A new rule needs a secret; an edit without one keeps the stored secret.
    if (!existing && secret === undefined) return { ok: false, reason: 'Enter a secret.' }
    // The stored secret was given for one kind and one registry. A password must
    // not turn into a token, and must not go to another host.
    if (existing && secret === undefined && needsNewSecret(existing, { kind, match })) {
      return { ok: false, reason: 'Enter the secret again: the kind or the registry changed.' }
    }
    const refusal = this.conflict(this.rules(), existing?.id, match)
    if (refusal) return { ok: false, reason: refusal }

    const id = existing?.id ?? crypto.randomBytes(8).toString('hex')
    // The value to put back if the rule cannot be saved after a new secret was stored.
    const previous = existing && secret !== undefined ? this.secrets.get(credSecret(id)) : undefined
    // The secret goes first, so a secret that cannot be kept leaves no rule behind.
    if (secret !== undefined) {
      // Without secure storage the store would keep the value in memory only; a
      // credential that is gone at the next start is refused, not half saved.
      if (this.secrets.backend() === 'session') return { ok: false, reason: NO_STORAGE }
      let stored: SetResult
      try {
        stored = await this.secrets.set(credSecret(id), secret)
      } catch {
        return { ok: false, reason: 'The secret could not be stored.' }
      }
      if (stored.status !== 'stored') {
        // Encryption failed on this write; do not keep the in-memory copy. For an
        // edit, that copy would hide the stored secret until the next start.
        if (!existing) await this.secrets.delete(credSecret(id)).catch(() => undefined)
        else await this.secrets.forgetSession(credSecret(id)).catch(() => undefined)
        return { ok: false, reason: NO_STORAGE }
      }
    }

    const out: { r: SaveResult } = { r: { ok: true, id } }
    try {
      await this.config.update((c) => {
        const rules = registryCredentials(c)
        const current = rules.find((r) => r.id === id)
        const again = this.conflict(rules, current?.id, match, !current)
        if (again) {
          out.r = { ok: false, reason: again }
          return c
        }
        const index = current?.index ?? rules.reduce((m, r) => Math.max(m, r.index), 0) + 1
        const rule: StoredRegistryCredential = { id, match, kind, index }
        if (kind === 'basic') rule.username = username
        return withRegistryCredentials(c, current ? rules.map((r) => (r.id === id ? rule : r)) : [...rules, rule])
      })
    } catch {
      out.r = { ok: false, reason: 'The rule could not be saved.' }
    }
    if (!out.r.ok && secret !== undefined) {
      if (!existing) await this.secrets.delete(credSecret(id)).catch(() => undefined)
      // The rule was not changed, so it gets its old secret back.
      else if (previous !== undefined) await this.secrets.set(credSecret(id), previous).catch(() => undefined)
    }
    return out.r
  }

  /** A plain message when `match` clashes with another rule or the list is full. */
  private conflict(rules: StoredRegistryCredential[], selfId: string | undefined, match: string, isNew = selfId === undefined): string | undefined {
    if (isNew && rules.length >= MAX_RULES) return 'There are too many rules.'
    if (rules.some((r) => r.id !== selfId && matchKey(r.match) === matchKey(match))) return 'A rule for this registry already exists.'
    return undefined
  }

  async remove(id: string): Promise<void> {
    // The secret first: a failure here leaves the rule in place, so the caller can retry.
    await this.secrets.delete(credSecret(id))
    await this.config.update((c) => withRegistryCredentials(c, registryCredentials(c).filter((r) => r.id !== id)))
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
