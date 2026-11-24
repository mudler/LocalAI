import fs from 'node:fs'
import path from 'node:path'
import picomatch from 'picomatch'
import yaml from 'js-yaml'
import { describe, expect, it } from 'vitest'

const root = path.resolve(__dirname, '..')
const cfg = yaml.load(fs.readFileSync(path.join(root, 'electron-builder.yml'), 'utf8')) as { files: string[] }
const pkg = JSON.parse(fs.readFileSync(path.join(root, 'package.json'), 'utf8')) as { main: string }

/** Relative imports of a TypeScript source file, resolved to source files. */
function localImports(file: string): string[] {
  const src = fs.readFileSync(file, 'utf8')
  const out: string[] = []
  for (const m of src.matchAll(/(?:from|import|require)\s*\(?\s*['"](\.{1,2}\/[^'"]+)['"]/g)) {
    const base = path.resolve(path.dirname(file), m[1])
    const hit = [`${base}.ts`, path.join(base, 'index.ts')].find((c) => fs.existsSync(c))
    if (!hit) throw new Error(`cannot resolve ${m[1]} from ${file}`)
    out.push(hit)
  }
  return out
}

/** Every source file reachable from the main and preload entry points. */
function reachable(entries: string[]): string[] {
  const seen = new Set<string>()
  const todo = [...entries]
  while (todo.length) {
    const f = todo.pop()!
    if (seen.has(f)) continue
    seen.add(f)
    todo.push(...localImports(f))
  }
  return [...seen]
}

// src/x/y.ts is compiled to dist/node/x/y.js (tsconfig outDir + rootDir).
const compiled = (src: string) => path.join('dist/node', path.relative(path.join(root, 'src'), src)).replace(/\.ts$/, '.js')

function makeMatcher(globs: string[]) {
  const positive = globs.filter((g) => !g.startsWith('!'))
  const negative = globs.filter((g) => g.startsWith('!')).map((g) => g.slice(1))
  return (rel: string) => positive.some((g) => picomatch(g)(rel)) && !negative.some((g) => picomatch(g, { dot: true })(rel))
}

const sources = reachable([path.join(root, 'src/main/index.ts'), path.join(root, 'src/preload/index.ts')])
const missingFrom = (globs: string[]) => sources.map(compiled).filter((rel) => !makeMatcher(globs)(rel))

describe('electron-builder files list', () => {
  it('package.json main points at the compiled main entry', () => {
    expect(pkg.main).toBe(compiled(path.join(root, 'src/main/index.ts')))
  })

  it('includes every module the main process and the preload import', () => {
    expect(sources.length).toBeGreaterThan(8)
    expect(missingFrom(cfg.files)).toEqual([])
  })

  it('reports a module the list does not cover', () => {
    const narrowed = cfg.files.map((g) => (g === 'dist/node/**/*' ? 'dist/node/main/index.js' : g))
    expect(missingFrom(narrowed)).toContain('dist/node/main/supervisor.js')
  })

  it('includes every module under src/main, src/preload and src/shared, imported or not yet', () => {
    // A module added now and imported later must not break only the packaged build.
    const all = ['main', 'preload', 'shared'].flatMap((dir) =>
      fs
        .readdirSync(path.join(root, 'src', dir))
        .filter((f) => f.endsWith('.ts') && !f.endsWith('.d.ts'))
        .map((f) => path.join(root, 'src', dir, f)),
    )
    expect(all.length).toBeGreaterThanOrEqual(sources.length)
    const inc = makeMatcher(cfg.files)
    expect(all.map(compiled).filter((rel) => !inc(rel))).toEqual([])
  })

  it('reaches the modules added by the later lanes from the entry points', () => {
    const rel = sources.map((s) => path.relative(root, s))
    for (const m of ['webuiView', 'webuiPolicy', 'webuiState', 'windowLayout', 'sandboxProbe', 'sandboxSetup', 'hostTools', 'selfUpdate', 'updaterStatus']) {
      expect(rel).toContain(`src/main/${m}.ts`)
    }
  })

  it('ships the renderer and the tray icon', () => {
    const inc = makeMatcher(cfg.files)
    expect(inc('dist/renderer/index.html')).toBe(true)
    expect(inc('build/icon.png')).toBe(true)
    expect(inc('src/main/index.ts')).toBe(false)
  })
})
