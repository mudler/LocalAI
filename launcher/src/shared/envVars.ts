// Rules for the server's environment variables, shared by the main process
// (which enforces them) and the renderer (which explains them before a save).
// Plain code with no imports, so both builds can use it.

/** A POSIX-style variable name. */
export const ENV_NAME = /^[A-Za-z_][A-Za-z0-9_]*$/
export const MAX_ENV_NAME = 128
/** Bytes of UTF-16 text, as JavaScript counts string length. */
export const MAX_ENV_VALUE = 32 * 1024
export const MAX_ENV_VARS = 200

/**
 * Variables of the launcher's own process that must never reach the server:
 * Electron and AppImage bookkeeping, and loader settings that point into the
 * mounted image. Also refused as user variables.
 */
export const STRIPPED_EXACT: readonly string[] = [
  'ELECTRON_RUN_AS_NODE',
  'APPDIR',
  'APPIMAGE',
  'ARGV0',
  'OWD',
  'LD_LIBRARY_PATH',
  'LD_PRELOAD',
  'NODE_OPTIONS',
  'GSETTINGS_SCHEMA_DIR',
]
export const STRIPPED_PREFIXES: readonly string[] = ['ELECTRON_', 'APPIMAGE_', 'CHROME_']

/**
 * Variables the launcher sets for the server itself, from typed settings. A
 * user value would fight the setting, so it is refused; an inherited value is
 * dropped, so a stray `API_KEY` in the desktop session cannot lock the server.
 *
 * LOCALAI_BASE_PATH: the launcher sets it to the data folder of its settings.
 * Change the folder in Settings instead.
 *
 * PATH: the server gets the PATH of the system, unchanged. A user value would
 * replace the search path of the server and every backend it starts, which
 * breaks them in ways that are hard to see, so PATH is refused here. Change
 * the system PATH instead.
 */
export const CONTROLLED_VARS: readonly string[] = ['LOCALAI_API_KEY', 'API_KEY', 'LOCALAI_ADDRESS', 'ADDRESS', 'PATH', 'LOCALAI_BASE_PATH']

/** Names the server's API key may be under in an old config. */
export const API_KEY_VARS: readonly string[] = ['LOCALAI_API_KEY', 'API_KEY']

/**
 * Windows treats variable names without regard to case: `Path` is `PATH`.
 * With `ignoreCase` the lists match in any case. The lists are upper case.
 */
export function isStrippedName(name: string, ignoreCase = false): boolean {
  const n = ignoreCase ? name.toUpperCase() : name
  return STRIPPED_EXACT.includes(n) || STRIPPED_PREFIXES.some((p) => n.startsWith(p))
}

/** True when the launcher controls `name` (see CONTROLLED_VARS). */
export function isControlledName(name: string, ignoreCase = false): boolean {
  return CONTROLLED_VARS.includes(ignoreCase ? name.toUpperCase() : name)
}

export type EnvNameProblem = 'format' | 'length' | 'controlled'

/** Why a name cannot be a user variable, or undefined when it can. `ignoreCase`: the Windows rule. */
export function envNameProblem(name: string, ignoreCase = false): EnvNameProblem | undefined {
  if (name.length === 0 || name.length > MAX_ENV_NAME) return 'length'
  if (!ENV_NAME.test(name)) return 'format'
  if (isStrippedName(name, ignoreCase) || isControlledName(name, ignoreCase)) return 'controlled'
  return undefined
}
