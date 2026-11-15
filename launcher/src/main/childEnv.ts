// Builds the environment of the LocalAI server process. No Electron import.
//
// The launcher runs inside Electron and, when packaged, inside an AppImage.
// Both set variables that must not leak into the server: a library path that
// points into the mounted image, ELECTRON_RUN_AS_NODE (which turns any
// Electron binary into a plain Node), and the AppImage bookkeeping variables.
// The lists live in shared/envVars.ts, because the Settings screen checks
// names against them too.
import { CONTROLLED_VARS, envNameProblem, isStrippedName } from '../shared/envVars'

export function isStrippedVar(name: string): boolean {
  return isStrippedName(name)
}

/**
 * Copy `base` without launcher-only variables and without the variables the
 * launcher controls, then apply `user` (the user's variables from Settings),
 * then `launcher` (what the launcher sets from typed settings, such as the
 * API key). A user variable whose name the launcher strips or controls is
 * skipped: Settings refuses such a name, and an old config may still hold
 * one.
 */
export function buildChildEnv(
  base: NodeJS.ProcessEnv,
  user: Record<string, string> = {},
  launcher: Record<string, string> = {},
): Record<string, string> {
  const env: Record<string, string> = {}
  for (const [k, v] of Object.entries(base)) {
    if (v === undefined || isStrippedVar(k)) continue
    // PATH is the one controlled name the server inherits from the system.
    if (k !== 'PATH' && CONTROLLED_VARS.includes(k)) continue
    env[k] = v
  }
  for (const [k, v] of Object.entries(user)) {
    if (envNameProblem(k) === undefined) env[k] = v
  }
  for (const [k, v] of Object.entries(launcher)) env[k] = v
  return env
}
