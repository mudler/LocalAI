// The server build that fits this computer, and the names that depend on the
// operating system. No Electron import.

/** A server build: GOOS and GOARCH, as the release names them. */
export interface PlatformTarget {
  /** `windows` is Node's `win32`. */
  os: 'linux' | 'darwin' | 'windows'
  arch: 'amd64' | 'arm64'
}

/**
 * The server build for Node's platform and arch, or undefined when the
 * release has none. The release has no windows/arm64 build. Windows on Arm
 * runs the x64 launcher under emulation, which reports `x64`.
 */
export function platformTarget(platform: NodeJS.Platform, arch: string): PlatformTarget | undefined {
  const os = platform === 'linux' ? 'linux' : platform === 'darwin' ? 'darwin' : platform === 'win32' ? 'windows' : undefined
  const goarch = arch === 'x64' ? 'amd64' : arch === 'arm64' ? 'arm64' : undefined
  if (!os || !goarch) return undefined
  if (os === 'windows' && goarch !== 'amd64') return undefined
  return { os, arch: goarch }
}

/** The file name suffix of an executable: `.exe` on Windows, none elsewhere. */
export function exeSuffix(t: Pick<PlatformTarget, 'os'>): string {
  return t.os === 'windows' ? '.exe' : ''
}

/** The installed server binary: `local-ai`, or `local-ai.exe` on Windows. */
export function serverBinaryName(t?: Pick<PlatformTarget, 'os'>): string {
  return `local-ai${t ? exeSuffix(t) : ''}`
}

/**
 * The rollback copy of the server binary. On Windows it keeps the `.exe`
 * suffix, so a person can still run it by hand.
 */
export function previousBinaryName(t?: Pick<PlatformTarget, 'os'>): string {
  return `local-ai.previous${t ? exeSuffix(t) : ''}`
}

/** Magic bytes of the executable format that the server build of `os` uses. */
export type ExecutableFormat = 'elf' | 'macho' | 'pe'

export function executableFormat(os: PlatformTarget['os']): ExecutableFormat {
  return os === 'windows' ? 'pe' : os === 'darwin' ? 'macho' : 'elf'
}

/**
 * The small environment of a helper command: PATH, and on Windows the
 * variables a Windows program needs to start at all (SystemRoot for the
 * system DLLs and the network stack, and the temporary folder).
 */
export function minimalEnv(env: NodeJS.ProcessEnv = process.env, platform: NodeJS.Platform = process.platform): Record<string, string> {
  const out: Record<string, string> = { PATH: env.PATH ?? '' }
  if (platform === 'win32') {
    for (const k of ['SystemRoot', 'SystemDrive', 'TEMP', 'TMP', 'USERPROFILE', 'LOCALAPPDATA']) {
      const v = env[k]
      if (v !== undefined) out[k] = v
    }
  }
  return out
}
