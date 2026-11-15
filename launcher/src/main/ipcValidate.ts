// Input checks for every IPC handler. No Electron import.
//
// The renderer is treated as untrusted input: each handler states the exact
// shape it takes, and anything else is refused with IpcInputError before the
// handler does any work. No handler takes a file path from the renderer: the
// data folders come from the main-process folder picker only.
import type {
  CopyKeyFormat,
  InstallOptions,
  LauncherSettings,
  LogLevelName,
  LogQueryInput,
  LogSourceName,
  SandboxPlanKind,
  ServerLogLevel,
  ShellInsets,
  ShellScreen,
  UpdateChannelName,
} from '../shared/types'
import { MAX_SHELL_INSET } from '../shared/types'
import { ENV_NAME, MAX_ENV_NAME, MAX_ENV_VALUE } from '../shared/envVars'

export class IpcInputError extends Error {
  constructor(message: string) {
    super(`invalid IPC input: ${message}`)
    this.name = 'IpcInputError'
  }
}

const fail = (m: string): never => {
  throw new IpcInputError(m)
}

const isPlainObject = (v: unknown): v is Record<string, unknown> =>
  typeof v === 'object' && v !== null && !Array.isArray(v) && Object.getPrototypeOf(v) === Object.prototype

/** The handler takes no argument. */
export function noArgs(args: unknown[]): void {
  if (args.length > 0) fail('this call takes no argument')
}

/** At most one argument; returns it (undefined when absent). */
export function oneOptional(args: unknown[]): unknown {
  if (args.length > 1) fail('this call takes at most one argument')
  return args[0]
}

/** Exactly one argument. */
export function one(args: unknown[]): unknown {
  if (args.length !== 1) fail('this call takes exactly one argument')
  return args[0]
}

export function bool(v: unknown, name: string): boolean {
  if (typeof v !== 'boolean') fail(`${name} must be a boolean`)
  return v as boolean
}

export function oneOf<T extends string>(v: unknown, allowed: readonly T[], name: string): T {
  if (typeof v !== 'string' || !(allowed as readonly string[]).includes(v)) fail(`${name} must be one of ${allowed.join(', ')}`)
  return v as T
}

export function int(v: unknown, min: number, max: number, name: string): number {
  if (typeof v !== 'number' || !Number.isInteger(v) || v < min || v > max) fail(`${name} must be an integer from ${min} to ${max}`)
  return v as number
}

const CONTROL = /[\u0000-\u001f\u007f]/

/** A string with a length limit; control characters only when `multiline` (and then no NUL). */
export function text(v: unknown, max: number, name: string, multiline = false): string {
  if (typeof v !== 'string') fail(`${name} must be a string`)
  const s = v as string
  if (s.length > max) fail(`${name} is too long`)
  if (multiline ? s.includes('\u0000') : CONTROL.test(s)) fail(`${name} contains a control character`)
  return s
}

/** Refuses keys outside `allowed`, so a typo or an injected field is not silently ignored. */
function onlyKeys(o: Record<string, unknown>, allowed: readonly string[], name: string): void {
  for (const k of Object.keys(o)) if (!allowed.includes(k)) fail(`${name} has an unknown field ${JSON.stringify(k).slice(0, 40)}`)
}

function optionalObject(v: unknown, allowed: readonly string[], name: string): Record<string, unknown> {
  if (v === undefined) return {}
  if (!isPlainObject(v)) fail(`${name} must be an object`)
  onlyKeys(v as Record<string, unknown>, allowed, name)
  return v as Record<string, unknown>
}

export function installOptions(v: unknown): InstallOptions {
  const o = optionalObject(v, ['allowPrerelease', 'stopFirst'], 'install options')
  const out: InstallOptions = {}
  if (o.allowPrerelease !== undefined) out.allowPrerelease = bool(o.allowPrerelease, 'allowPrerelease')
  if (o.stopFirst !== undefined) out.stopFirst = bool(o.stopFirst, 'stopFirst')
  return out
}

const LOG_SOURCES: readonly LogSourceName[] = ['server', 'launcher', 'updater']
const LOG_LEVELS: readonly LogLevelName[] = ['debug', 'info', 'warn', 'error']
export const MAX_LOG_QUERY = 5000

export function logQuery(v: unknown): LogQueryInput {
  const o = optionalObject(v, ['source', 'minLevel', 'afterSeq', 'limit'], 'log query')
  const out: LogQueryInput = {}
  if (o.source !== undefined) out.source = oneOf(o.source, LOG_SOURCES, 'source')
  if (o.minLevel !== undefined) out.minLevel = oneOf(o.minLevel, LOG_LEVELS, 'minLevel')
  if (o.afterSeq !== undefined) out.afterSeq = int(o.afterSeq, 0, Number.MAX_SAFE_INTEGER, 'afterSeq')
  out.limit = o.limit === undefined ? 1000 : int(o.limit, 1, MAX_LOG_QUERY, 'limit')
  return out
}

export function planKind(v: unknown): SandboxPlanKind {
  return v === undefined ? 'setup' : oneOf(v, ['setup', 'remove'] as const, 'plan kind')
}

/** A model id from the server's own list: a short single-line string. */
export function modelId(v: unknown): string | undefined {
  if (v === undefined) return undefined
  const s = text(v, 512, 'model')
  return s === '' ? undefined : s
}

export function clipboardText(v: unknown): string {
  return text(v, 64 * 1024, 'text', true)
}

// ---- settings ---------------------------------------------------------------

export const SERVER_LOG_LEVELS: readonly ServerLogLevel[] = ['error', 'warn', 'info', 'debug', 'trace']
// No modelsPath or backendsPath: a data folder comes only from the main-process
// folder picker (launcher:settings:pick-folder), never as a path from the renderer.
const SETTINGS_KEYS: readonly (keyof LauncherSettings)[] = [
  'address',
  'autoStartServer',
  'startOnBoot',
  'logLevel',
  'showWelcome',
  'showStatusBar',
  'openWebuiInBrowser',
  'secretsPlaintextFile',
]

/** host:port. host is a name, an IPv4 address or a bracketed IPv6 address; port 1 to 65535. */
export function address(v: unknown): string {
  const s = text(v, 300, 'address')
  const m = /^(\[[0-9A-Fa-f:.]+\]|[A-Za-z0-9.-]*):(\d{1,5})$/.exec(s)
  if (!m) fail('address must look like host:port')
  const port = Number(m![2])
  if (port < 1 || port > 65535) fail('address port must be from 1 to 65535')
  const host = m![1]
  if (host.startsWith('[')) {
    if (!/^\[[0-9A-Fa-f:.]*:[0-9A-Fa-f:.]*\]$/.test(host)) fail('address has a bad IPv6 host')
  } else if (host.length > 253 || host.startsWith('-') || host.startsWith('.') || host.includes('..')) {
    fail('address has a bad host name')
  }
  return s
}

// ---- environment variables and the API key ------------------------------------

/**
 * The name of a user environment variable: the shape only. Whether the
 * launcher allows the name is checked by the core, which answers with a
 * reason the screen can show.
 */
export function envName(v: unknown): string {
  const s = text(v, MAX_ENV_NAME, 'name')
  if (!ENV_NAME.test(s)) fail('name must be a variable name')
  return s
}

/** A value: any text without NUL, up to MAX_ENV_VALUE characters. Never echoed in an error. */
export function envValue(v: unknown): string {
  if (typeof v !== 'string') fail('value must be a string')
  const s = v as string
  if (s.length > MAX_ENV_VALUE) fail('value is too long')
  if (s.includes('\u0000')) fail('value contains NUL')
  return s
}

const COPY_FORMATS: readonly CopyKeyFormat[] = ['key', 'openai', 'anthropic', 'curl']

export function copyFormat(v: unknown): CopyKeyFormat {
  return v === undefined ? 'key' : oneOf(v, COPY_FORMATS, 'format')
}

/** A plan id from getNetworkPlan: 32 hex characters. */
export function planId(v: unknown): string | undefined {
  if (v === undefined) return undefined
  if (typeof v !== 'string' || !/^[0-9a-f]{32}$/.test(v)) fail('planId must be a plan id')
  return v as string
}

/** One or two arguments. */
export function oneOrTwo(args: unknown[]): [unknown, unknown] {
  if (args.length < 1 || args.length > 2) fail('this call takes one or two arguments')
  return [args[0], args[1]]
}

/** Exactly two arguments. */
export function two(args: unknown[]): [unknown, unknown] {
  if (args.length !== 2) fail('this call takes exactly two arguments')
  return [args[0], args[1]]
}

/** A partial settings object: only known keys, each with its own check. */
export function settingsPatch(v: unknown): Partial<LauncherSettings> {
  if (!isPlainObject(v)) fail('settings must be an object')
  const o = v as Record<string, unknown>
  onlyKeys(o, SETTINGS_KEYS, 'settings')
  const out: Partial<LauncherSettings> = {}
  if (o.address !== undefined) out.address = address(o.address)
  if (o.autoStartServer !== undefined) out.autoStartServer = bool(o.autoStartServer, 'autoStartServer')
  if (o.startOnBoot !== undefined) out.startOnBoot = bool(o.startOnBoot, 'startOnBoot')
  if (o.logLevel !== undefined) out.logLevel = oneOf(o.logLevel, SERVER_LOG_LEVELS, 'logLevel')
  if (o.showWelcome !== undefined) out.showWelcome = bool(o.showWelcome, 'showWelcome')
  if (o.showStatusBar !== undefined) out.showStatusBar = bool(o.showStatusBar, 'showStatusBar')
  if (o.openWebuiInBrowser !== undefined) out.openWebuiInBrowser = bool(o.openWebuiInBrowser, 'openWebuiInBrowser')
  if (o.secretsPlaintextFile !== undefined) out.secretsPlaintextFile = bool(o.secretsPlaintextFile, 'secretsPlaintextFile')
  return out
}

// ---- WebUI view -----------------------------------------------------------------

/** The id of a saved server, as the main process made it. Never an address. */
export function serverId(v: unknown, name = 'id'): string {
  const s = text(v, 40, name)
  if (!/^[A-Za-z0-9_-]{1,40}$/.test(s)) fail(`${name} must be a server id`)
  return s
}

/** `local` or a saved server id. */
export function serverTarget(v: unknown): string {
  return serverId(v, 'target')
}

/** The id of a saved server to open, or nothing for "the default". */
export function connectId(v: unknown): string | undefined {
  return v === undefined ? undefined : serverId(v)
}

/** What the user typed as a server address: text only. The main process reduces it to an origin and refuses the rest. */
export function typedAddress(v: unknown): string {
  return text(v, 2048, 'address')
}

export function serverName(v: unknown): string {
  return text(v, 200, 'name')
}

export function testInput(v: unknown): { id: string } | { address: string } {
  if (!isPlainObject(v)) fail('test input must be an object')
  const o = v as Record<string, unknown>
  onlyKeys(o, ['id', 'address'], 'test input')
  if ((o.id === undefined) === (o.address === undefined)) fail('test input needs an id or an address')
  return o.id !== undefined ? { id: serverId(o.id) } : { address: typedAddress(o.address) }
}

export function addServerInput(v: unknown): { name: string; address: string; makeDefault?: boolean; skipTest?: boolean } {
  if (!isPlainObject(v)) fail('server must be an object')
  const o = v as Record<string, unknown>
  onlyKeys(o, ['name', 'address', 'makeDefault', 'skipTest'], 'server')
  const out: { name: string; address: string; makeDefault?: boolean; skipTest?: boolean } = { name: serverName(o.name), address: typedAddress(o.address) }
  if (o.makeDefault !== undefined) out.makeDefault = bool(o.makeDefault, 'makeDefault')
  if (o.skipTest !== undefined) out.skipTest = bool(o.skipTest, 'skipTest')
  return out
}

export function updateServerInput(v: unknown): { id: string; name?: string; address?: string; skipTest?: boolean } {
  if (!isPlainObject(v)) fail('server must be an object')
  const o = v as Record<string, unknown>
  onlyKeys(o, ['id', 'name', 'address', 'skipTest'], 'server')
  const out: { id: string; name?: string; address?: string; skipTest?: boolean } = { id: serverId(o.id) }
  if (o.name !== undefined) out.name = serverName(o.name)
  if (o.address !== undefined) out.address = typedAddress(o.address)
  if (o.skipTest !== undefined) out.skipTest = bool(o.skipTest, 'skipTest')
  return out
}

export function updateChannel(v: unknown): UpdateChannelName {
  return oneOf(v, ['stable', 'rc', 'dev'] as const, 'channel')
}

const SHELL_SCREENS: readonly ShellScreen[] = ['logs', 'settings', 'connect']

/** A shell screen name, or null for "none". */
export function shellScreen(v: unknown): ShellScreen | null {
  if (v === null) return null
  return oneOf(v, SHELL_SCREENS, 'screen')
}

/** The shell's strip heights. Fractional CSS pixels are rounded up so a strip is never cut. */
export function shellInsets(v: unknown): ShellInsets {
  if (!isPlainObject(v)) fail('insets must be an object')
  const o = v as Record<string, unknown>
  onlyKeys(o, ['top', 'bottom'], 'insets')
  const px = (x: unknown, name: string) => {
    if (typeof x !== 'number' || !Number.isFinite(x) || x < 0 || x > MAX_SHELL_INSET) fail(`${name} must be a number from 0 to ${MAX_SHELL_INSET}`)
    return Math.ceil(x as number)
  }
  return { top: px(o.top, 'top'), bottom: px(o.bottom, 'bottom') }
}

const FOLDER_KINDS = ['models', 'backends'] as const

export function folderKind(v: unknown): (typeof FOLDER_KINDS)[number] {
  return oneOf(v, FOLDER_KINDS, 'kind')
}

/** A TCP port the server may use: no privileged ports. */
export function port(v: unknown, name = 'port'): number {
  return int(v, 1024, 65535, name)
}
