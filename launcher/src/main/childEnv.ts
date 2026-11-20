// Builds the environment of the LocalAI server process. No Electron import.
//
// The launcher runs inside Electron and, when packaged, inside an AppImage.
// Both set variables that must not leak into the server: a library path that
// points into the mounted image, ELECTRON_RUN_AS_NODE (which turns any
// Electron binary into a plain Node), and the AppImage bookkeeping variables.
// The lists live in shared/envVars.ts, because the Settings screen checks
// names against them too.
import { envNameProblem, isControlledName, isStrippedName } from '../shared/envVars'

export function isStrippedVar(name: string, platform: NodeJS.Platform = process.platform): boolean {
  return isStrippedName(name, platform === 'win32')
}

/**
 * Copy `base` without launcher-only variables and without the variables the
 * launcher controls, then apply `user` (the user's variables from Settings),
 * then `launcher` (what the launcher sets from typed settings, such as the
 * API key). A user variable whose name the launcher strips or controls is
 * skipped: Settings refuses such a name, and an old config may still hold
 * one.
 *
 * On Windows names have no case: `Path` is PATH and `api_key` is API_KEY.
 * The rules then match in any case, and a later layer replaces a name of an
 * earlier one whatever its case, so the server never gets two spellings of
 * one variable.
 */
export function buildChildEnv(
  base: NodeJS.ProcessEnv,
  user: Record<string, string> = {},
  launcher: Record<string, string> = {},
  platform: NodeJS.Platform = process.platform,
): Record<string, string> {
  const win = platform === 'win32'
  const env: Record<string, string> = {}
  const set = (k: string, v: string) => {
    if (win) {
      const upper = k.toUpperCase()
      for (const old of Object.keys(env)) if (old !== k && old.toUpperCase() === upper) delete env[old]
    }
    env[k] = v
  }
  for (const [k, v] of Object.entries(base)) {
    if (v === undefined || isStrippedVar(k, platform)) continue
    // PATH is the one controlled name the server inherits from the system.
    if ((win ? k.toUpperCase() : k) !== 'PATH' && isControlledName(k, win)) continue
    set(k, v)
  }
  for (const [k, v] of Object.entries(user)) {
    if (envNameProblem(k, win) === undefined) set(k, v)
  }
  for (const [k, v] of Object.entries(launcher)) set(k, v)
  return env
}
