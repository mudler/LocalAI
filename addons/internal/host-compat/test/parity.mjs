// Checks shared by the add-ons that keep the host page code: the words, the
// icons and the imports of the source stay in step with the host.
import { readFileSync, readdirSync, statSync, existsSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
export const hostUi = resolve(here, '../../../../core/http/react-ui')
export const compatSrc = resolve(here, '../src')

export function sourceFiles(dir) {
  const out = []
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) out.push(...sourceFiles(p))
    else if (/\.(jsx?|mjs)$/.test(name)) out.push(p)
  }
  return out
}

export const text = (files) => files.map((f) => readFileSync(f, 'utf8')).join('\n')

// The literal keys a source passes to t(): t('a.b'), t("a.b", ...), t(`a.${x}`) is skipped.
export function literalKeys(source) {
  return new Set([...source.matchAll(/\bt\(\s*(['"])([A-Za-z0-9_.:]+)\1/g)].map((m) => m[2]))
}

// A key exists in a locale object when the path resolves, as a value or as a
// plural pair (key_one / key_other).
export function hasKey(data, key) {
  const walk = (name) => name.split('.').reduce((node, part) => (node && typeof node === 'object' ? node[part] : undefined), data)
  return walk(key) !== undefined || walk(`${key}_other`) !== undefined || walk(`${key}_one`) !== undefined
}

export async function iconNames() {
  const { ICON_NAMES } = await import(pathToFileURL(join(hostUi, 'src/addons/sdk.js')).href)
  return new Set(ICON_NAMES)
}

// The icon ids of the host sprite.
export function spriteNames() {
  const sprite = readFileSync(join(hostUi, 'src/vendor/ui-kit/icons/sprite.svg'), 'utf8')
  return new Set([...sprite.matchAll(/id="dk-icon-([a-z0-9-]+)"/g)].map((m) => m[1]))
}

// The icon names a source draws: the literals inside <Icon .../>, the `icon`
// props and fields, and the TYPE_ICON table.
export function iconLiterals(source) {
  const out = new Set()
  const add = (chunk) => { for (const m of chunk.matchAll(/(['"])([a-z][a-z0-9]*(?:-[a-z0-9]+)*)\1/g)) out.add(m[2]) }
  for (const m of source.matchAll(/<Icon\b[^>]*?\/?>/g)) add(m[0].replace(/\b(?:className|style|title|aria-[a-z]+)=("[^"]*"|\{[^}]*\})/g, '').replace(/[=!]==?\s*(['"])[^'"]*\1/g, ''))
  for (const m of source.matchAll(/\bicon\s*[:=]\s*\{?\s*(['"][a-z0-9-]+['"])/g)) add(m[1])
  const table = /TYPE_ICON\s*=\s*\{([^}]*)\}/.exec(source)
  if (table) add(table[1].replace(/\b[a-z]+\s*:/g, ''))
  return out
}

// Every import of a source file: a relative one must be a file of the add-on or
// of the compat directory; a bare one must be a module the compat directory
// answers. Returns the imports that are neither.
export function unresolvedImports(srcRoot) {
  const bare = new Set(['react', 'react/jsx-runtime', 'react-dom', 'react-router-dom', 'react-i18next'])
  const tails = ['', '.js', '.jsx', '.mjs', '.json', '.css', '/index.js', '/index.jsx']
  const exists = (base) => tails.some((t) => existsSync(base + t) && statSync(base + t).isFile())
  const bad = []
  for (const file of sourceFiles(srcRoot)) {
    const source = readFileSync(file, 'utf8')
    const specs = [
      ...source.matchAll(/^\s*(?:import|export)\b[^'"\n;]*?\bfrom\s*['"]([^'"]+)['"]/gm),
      ...source.matchAll(/^\s*import\s*['"]([^'"]+)['"]/gm),
      ...source.matchAll(/\bimport\(\s*['"]([^'"]+)['"]\s*\)/g),
    ]
    for (const m of specs) {
      const spec = m[1]
      if (spec.startsWith('.')) {
        const target = resolve(dirname(file), spec)
        const rel = target.slice(srcRoot.length)
        if (!exists(target) && !exists(join(compatSrc, rel))) bad.push(`${file.slice(srcRoot.length)}: ${spec}`)
      } else if (!bare.has(spec)) {
        bad.push(`${file.slice(srcRoot.length)}: ${spec}`)
      }
    }
  }
  return bad
}
