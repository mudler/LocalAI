// The credentials file the launcher generates for the server, from the
// registry credential rules. No Electron import.
//
// The file holds no secret. A rule names an environment variable
// (`password_env`, `bearer_env`) and the launcher puts the secret into that
// variable of the server process. Only these keys are written, because the
// server refuses a file with an unknown key and has no `kind` key: the kind is
// the key that carries the secret.
import fs from 'node:fs'
import path from 'node:path'
import type { RegistryCredentialRule } from '../shared/types'
import { writeFileAtomic } from './secretStore'

/** The server setting that names its credentials file. */
export const CREDENTIALS_FILE_ENV = 'LOCALAI_CREDENTIALS_FILE'
/** The launcher's own file. It is never the `credentials.yaml` of the data folder, which belongs to the user. */
export const CREDENTIALS_FILE_NAME = 'registry-credentials.yaml'
export const CREDENTIALS_FILE_MODE = 0o600

/** A double-quoted YAML string. A JSON string is one, with every special character escaped. */
const q = (s: string): string => JSON.stringify(s)

/**
 * The YAML for `rules`, in order. A rule keeps its place; the server uses the
 * first match it finds, so the order is the user's.
 */
export function renderCredentialsYaml(rules: RegistryCredentialRule[], envName: (id: string) => string): string {
  let out = ''
  for (const r of rules) {
    out += `- match: ${q(r.match)}\n`
    if (r.kind === 'basic') {
      out += `  username: ${q(r.username ?? '')}\n`
      out += `  password_env: ${q(envName(r.id))}\n`
    } else {
      out += `  bearer_env: ${q(envName(r.id))}\n`
    }
  }
  return out
}

/**
 * Writes `<dir>/registry-credentials.yaml` (mode 0600, atomic) and returns its
 * path. With no rules it removes the file and returns undefined, so the server
 * is not pointed at a file that holds nothing.
 */
export async function writeCredentialsFile(dir: string, rules: RegistryCredentialRule[], envName: (id: string) => string): Promise<string | undefined> {
  const file = path.join(dir, CREDENTIALS_FILE_NAME)
  if (rules.length === 0) {
    await fs.promises.rm(file, { force: true })
    return undefined
  }
  await writeFileAtomic(file, renderCredentialsYaml(rules, envName), CREDENTIALS_FILE_MODE)
  return file
}

/** The part of RegistryCredentials this module uses. */
export interface CredentialSource {
  list(): RegistryCredentialRule[]
  secretEnvName(id: string): string
  secretFor(id: string): Promise<string | undefined>
}

export interface PreparedCredentials {
  /** The generated file; undefined when there is nothing to pass or the user set their own. */
  file?: string
  /** Secret variables for the server process, by name. */
  env: Record<string, string>
  /** The user set LOCALAI_CREDENTIALS_FILE themselves: the launcher writes nothing and passes nothing. */
  userOverride: boolean
}

/**
 * Builds the file and the variables for one server start. A rule whose secret
 * is missing is left out of both, so the file never has an entry that cannot
 * work. A user-set LOCALAI_CREDENTIALS_FILE wins and nothing is written.
 */
export async function prepareRegistryCredentials(dir: string, source: CredentialSource, userOverride: boolean): Promise<PreparedCredentials> {
  if (userOverride) {
    // The user's file is used; an older generated file would only confuse.
    await writeCredentialsFile(dir, [], () => '')
    return { env: {}, userOverride: true }
  }
  const env: Record<string, string> = {}
  const usable: RegistryCredentialRule[] = []
  for (const r of source.list()) {
    const secret = await source.secretFor(r.id)
    if (secret === undefined) continue
    env[source.secretEnvName(r.id)] = secret
    usable.push(r)
  }
  const file = await writeCredentialsFile(dir, usable, (id) => source.secretEnvName(id))
  return { ...(file ? { file } : {}), env: file ? env : {}, userOverride: false }
}
