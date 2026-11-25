// The import guard of src/core and src/shared: code there runs in the desktop
// main process, in the renderer and in a client-only shell with no Node and no
// Electron, so it may import only other portable modules. tsconfig.core.json
// type-checks the same folders without Node types.
import fs from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'

const root = path.resolve(__dirname, '..')

const NODE_BUILTINS = new Set([
  'fs',
  'path',
  'os',
  'crypto',
  'http',
  'https',
  'net',
  'child_process',
  'events',
  'url',
  'buffer',
  'stream',
  'util',
  'timers',
])

const SPECIFIERS = [
  /\b(?:import|export)\s+(?:type\s+)?[^'";]*?\bfrom\s*['"]([^'"]+)['"]/g,
  /\bimport\s*['"]([^'"]+)['"]/g,
  /\bimport\s*\(\s*['"]([^'"]+)['"]\s*\)/g,
  /\brequire\s*\(\s*['"]([^'"]+)['"]\s*\)/g,
]

function forbidden(spec: string): boolean {
  if (spec.startsWith('node:')) return true
  if (NODE_BUILTINS.has(spec.split('/')[0])) return true
  // Also electron-updater and any electron/* subpath.
  if (spec.startsWith('electron')) return true
  if (spec === 'semver') return true
  if (/\/(main|preload|renderer)\//.test(spec)) return true
  return /^\.\.\/(main|preload|renderer)(\/|$)/.test(spec)
}

/** The module specifiers of `source` that portable code must not import. */
export function forbiddenImports(source: string): string[] {
  const out: string[] = []
  for (const re of SPECIFIERS) {
    for (const m of source.matchAll(re)) if (forbidden(m[1]) && !out.includes(m[1])) out.push(m[1])
  }
  return out
}

/** The .ts files under the given folders of the launcher, recursively, relative to the launcher root. */
function listTs(dirs: string[]): string[] {
  const out: string[] = []
  const walk = (dir: string) => {
    for (const e of fs.readdirSync(path.join(root, dir), { withFileTypes: true })) {
      const rel = path.join(dir, e.name)
      if (e.isDirectory()) walk(rel)
      else if (e.name.endsWith('.ts')) out.push(rel)
    }
  }
  for (const d of dirs) if (fs.existsSync(path.join(root, d))) walk(d)
  return out
}

const read = (rel: string) => fs.readFileSync(path.join(root, rel), 'utf8')

describe('core import boundary', () => {
  it('flags every forbidden import form', () => {
    expect(forbiddenImports(`import fs from 'node:fs'`)).toEqual(['node:fs'])
    expect(forbiddenImports(`import type os from 'os'`)).toEqual(['os'])
    expect(forbiddenImports(`const c = require('crypto')`)).toEqual(['crypto'])
    expect(forbiddenImports(`export { x } from '../main/core'`)).toEqual(['../main/core'])
    expect(forbiddenImports(`import { app } from 'electron'`)).toEqual(['electron'])
    expect(forbiddenImports(`import semver from 'semver'`)).toEqual(['semver'])
    expect(forbiddenImports(`import { a } from './address'`)).toEqual([])
  })
  it('src/core and src/shared import no Node, Electron or main-process module', () => {
    const files = listTs(['src/core', 'src/shared'])
    expect(files.some((f) => f.endsWith('core/address.ts'))).toBe(true)
    expect(files.map((f) => path.basename(f))).toEqual(
      expect.arrayContaining(['webuiPolicy.ts', 'webuiState.ts', 'notifications.ts', 'remoteMonitor.ts', 'testClassify.ts']),
    )
    expect(Object.fromEntries(files.map((f) => [f, forbiddenImports(read(f))]).filter(([, v]) => v.length))).toEqual({})
  })
})
