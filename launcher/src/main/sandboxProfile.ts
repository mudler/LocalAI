// Generates the AppArmor profile that lets the launcher create user
// namespaces, and describes the exact commands that install and remove it.
// Pure: it builds text and data and runs nothing. No Electron import.

export const PROFILE_NAME = 'localai-launcher'
export const PROFILE_PATH = `/etc/apparmor.d/${PROFILE_NAME}`

/**
 * OPEN QUESTION, settled later by a hands-on test with root. There is no
 * default attach pattern on purpose. AppArmor binds a profile to the path of
 * the executed binary. A FUSE-mounted AppImage executes from a random mount
 * directory, so a profile on the .AppImage file may never match.
 *
 * These are the candidates. Nothing picks one; callers pass the pattern.
 */
export const ATTACH_STRATEGIES = [
  {
    id: 'mount-pattern',
    example: '/tmp/.mount_LocalA*/localai-launcher',
    // Matches the random mount directory of the AppImage runtime.
    // Pro: works with the normal AppImage start, no copy, no extra step.
    // Con: broad. The mount directory is user-writable, so any program a
    //   user places at a matching path gets the userns permission, and the
    //   profile is unconfined apart from that. Judge the risk before use.
    // Con: the prefix depends on the runtime and the file name.
    tradeoffs: 'works unchanged, but broad and matchable by any user-made path',
  },
  {
    id: 'stable-install-path',
    example: '/opt/localai-launcher/localai-launcher',
    // The launcher copies or extracts itself to a fixed directory and runs
    // from there.
    // Pro: a narrow, exact path; the profile is easy to audit.
    // Con: the launcher must manage that copy and keep it current, which
    //   fights the self-update design; the install may need root.
    tradeoffs: 'narrow and auditable, but needs a managed copy and updates for it',
  },
  {
    id: 'extract-and-run',
    example: '/home/*/.cache/localai-launcher/squashfs-root/localai-launcher',
    // The user runs with --appimage-extract-and-run (or extracts once), and
    // the profile names the extraction path.
    // Pro: a fixed path with no FUSE.
    // Con: the user must change how they start the launcher; a home path glob
    //   is user-writable, so it has the same trust problem as a mount pattern,
    //   only narrower; extraction directories can vary by runtime version.
    tradeoffs: 'fixed path without FUSE, but changes how users start it',
  },
] as const

export class InvalidAttachPattern extends Error {
  constructor(reason: string) {
    super(`invalid attach pattern: ${reason}`)
    this.name = 'InvalidAttachPattern'
  }
}

// Allowed: letters, digits and . _ - + : ~ = / plus the globs * and ?.
// Everything else is rejected: whitespace, quotes, braces, brackets, commas,
// backslashes, # < > @ $ ^ ; | & ( ), control characters, non-ASCII.
const SAFE = /^[A-Za-z0-9._+:~=/*?-]+$/

/** Throws InvalidAttachPattern. Returns the pattern when it is safe to embed. */
export function validateAttachPattern(p: string): string {
  if (typeof p !== 'string' || p.length === 0) throw new InvalidAttachPattern('empty')
  if (p.length > 512) throw new InvalidAttachPattern('too long')
  if (!p.startsWith('/')) throw new InvalidAttachPattern('not an absolute path')
  if (!SAFE.test(p)) throw new InvalidAttachPattern('contains a character that is not allowed')
  const segments = p.split('/').slice(1)
  if (segments.includes('..')) throw new InvalidAttachPattern('contains ..')
  if (segments.includes('')) throw new InvalidAttachPattern('empty path segment')
  if (/[*?]/.test(segments[0])) throw new InvalidAttachPattern('first path segment must be literal')
  if (!/[A-Za-z0-9._+:~=-]/.test(segments[segments.length - 1])) {
    throw new InvalidAttachPattern('last path segment must contain a literal character')
  }
  return p
}

/** The profile file text. `userns,` is the permission; `flags=(unconfined)` keeps everything else as it was. */
export function generateProfile(attachPattern: string): string {
  const attach = validateAttachPattern(attachPattern)
  return [
    '# Installed by LocalAI Launcher. Lets the launcher create user namespaces',
    '# for the Chromium sandbox. Remove with: apparmor_parser -R ' + PROFILE_PATH,
    'abi <abi/4.0>,',
    'include <tunables/global>',
    '',
    `profile ${PROFILE_NAME} ${attach} flags=(unconfined) {`,
    '  userns,',
    '',
    `  include if exists <local/${PROFILE_NAME}>`,
    '}',
    '',
  ].join('\n')
}

export interface PlanStep {
  description: string
  /** Full argv, starting with the program. Never a shell string. */
  argv: string[]
  /** Text for the program's standard input. */
  input?: string
}

export interface SandboxPlan {
  kind: 'setup' | 'remove'
  profilePath: string
  /** The exact file text, for setup. Show it to the user before running. */
  profileText?: string
  steps: PlanStep[]
  /** The same plan as a script the user can paste into a terminal. */
  terminalText: string
}

const TEE = '/usr/bin/tee'
const PARSER = '/usr/sbin/apparmor_parser'
const RM = '/usr/bin/rm'
const PKEXEC = 'pkexec'

export function buildSetupPlan(attachPattern: string): SandboxPlan {
  const text = generateProfile(attachPattern)
  return {
    kind: 'setup',
    profilePath: PROFILE_PATH,
    profileText: text,
    steps: [
      { description: `Write ${PROFILE_PATH}`, argv: [PKEXEC, TEE, PROFILE_PATH], input: text },
      { description: 'Load the profile', argv: [PKEXEC, PARSER, '-r', PROFILE_PATH] },
    ],
    // The text has no quote, so a quoted here-document delimiter is safe.
    terminalText: [
      `sudo tee ${PROFILE_PATH} >/dev/null <<'EOF'`,
      text.replace(/\n$/, ''),
      'EOF',
      `sudo apparmor_parser -r ${PROFILE_PATH}`,
    ].join('\n'),
  }
}

export function buildRemovePlan(): SandboxPlan {
  return {
    kind: 'remove',
    profilePath: PROFILE_PATH,
    steps: [
      { description: 'Unload the profile', argv: [PKEXEC, PARSER, '-R', PROFILE_PATH] },
      { description: `Delete ${PROFILE_PATH}`, argv: [PKEXEC, RM, '-f', PROFILE_PATH] },
    ],
    terminalText: [`sudo apparmor_parser -R ${PROFILE_PATH}`, `sudo rm -f ${PROFILE_PATH}`].join('\n'),
  }
}
