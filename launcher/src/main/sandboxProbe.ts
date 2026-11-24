// Decides whether the launcher runs with Chromium's sandbox, from facts about
// the host. The decision is a pure function; the reader collects the facts.
// No Electron import.
//
// Background: Ubuntu 23.10 and later set
// kernel.apparmor_restrict_unprivileged_userns=1. A program with no AppArmor
// profile then cannot create an unprivileged user namespace, and the sandbox
// cannot start. The AppImage launch script adds --no-sandbox in that case.

import fs from 'node:fs'
import { PROFILE_NAME } from './sandboxProfile'

export type SandboxState =
  /** The sandbox is on. */
  | 'sandboxed'
  /** No sandbox, and the host restricts user namespaces. A profile can fix it. */
  | 'unsandboxed-restricted'
  /** No sandbox, and the cause is not the AppArmor restriction. */
  | 'unsandboxed-other'
  /** Not Linux. Nothing to check. */
  | 'not-applicable'

/** Whether an AppArmor profile for the launcher is loaded. `unknown` when it cannot be read. */
export type ProfileStatus = 'loaded' | 'not-loaded' | 'unknown'

export interface SandboxInputs {
  /** `process.platform`. */
  platform: string
  /** Value of the sysctl, or null when the file is missing or unreadable. */
  sysctl: number | null
  /** `--no-sandbox` is in the process argv. */
  noSandboxFlag: boolean
  profile: ProfileStatus
}

/**
 * - Not Linux: not-applicable.
 * - No `--no-sandbox`: the sandbox runs, so sandboxed, whatever the sysctl says.
 * - `--no-sandbox` and sysctl 1: unsandboxed-restricted.
 * - `--no-sandbox` and sysctl 0 or missing: unsandboxed-other (the flag was set
 *   for another reason, for example by the user).
 *
 * `profile` does not change the state. A loaded profile with the flag still set
 * means a restart is due, or the profile does not match the executed path; the
 * caller can show that from `profile`.
 */
export function decideSandboxState(i: SandboxInputs): SandboxState {
  if (i.platform !== 'linux') return 'not-applicable'
  if (!i.noSandboxFlag) return 'sandboxed'
  return i.sysctl === 1 ? 'unsandboxed-restricted' : 'unsandboxed-other'
}

export interface SandboxProbe {
  state: SandboxState
  inputs: SandboxInputs
}

export const SYSCTL_PATH = '/proc/sys/kernel/apparmor_restrict_unprivileged_userns'
export const APPARMOR_PROFILES_PATH = '/sys/kernel/security/apparmor/profiles'

export interface ProbeDeps {
  platform: string
  argv: readonly string[]
  /** Returns the file text. Throws when the file is missing or unreadable. */
  readFile: (path: string) => string
}

const realDeps = (): ProbeDeps => ({
  platform: process.platform,
  argv: process.argv,
  readFile: (p) => fs.readFileSync(p, 'utf8'),
})

/** `0`, `1` or other non-negative integers; anything else counts as unreadable. */
export function parseSysctl(text: string): number | null {
  const t = text.trim()
  return /^\d+$/.test(t) ? Number(t) : null
}

/**
 * The profiles file lists one profile per line: `name (mode)`. It is readable
 * only by root, so an unprivileged read normally fails; that is `unknown`.
 */
export function parseProfiles(text: string, name: string): ProfileStatus {
  const loaded = text.split('\n').some((line) => line === name || line.startsWith(`${name} (`))
  return loaded ? 'loaded' : 'not-loaded'
}

/** Collects the inputs. Never throws. */
export function readSandboxInputs(deps: ProbeDeps = realDeps()): SandboxInputs {
  let sysctl: number | null = null
  let profile: ProfileStatus = 'unknown'
  if (deps.platform === 'linux') {
    try {
      sysctl = parseSysctl(deps.readFile(SYSCTL_PATH))
    } catch {
      sysctl = null
    }
    try {
      profile = parseProfiles(deps.readFile(APPARMOR_PROFILES_PATH), PROFILE_NAME)
    } catch {
      profile = 'unknown'
    }
  }
  return {
    platform: deps.platform,
    sysctl,
    noSandboxFlag: deps.argv.includes('--no-sandbox'),
    profile,
  }
}

export function probeSandbox(deps?: ProbeDeps): SandboxProbe {
  const inputs = readSandboxInputs(deps)
  return { state: decideSandboxState(inputs), inputs }
}
