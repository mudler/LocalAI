// A node resolve hook for tests that import add-on source files directly. The
// bundler answers a relative import whose target is not in the add-on's src/
// from the compat directory (see addons/internal/uibuild/uibuild.mjs); this
// hook does the same for node, so a source file runs without a build.
import { existsSync, statSync } from 'node:fs'
import { dirname, join, relative, resolve, sep } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

const compatSrc = resolve(dirname(fileURLToPath(import.meta.url)), '../src')
const tails = ['', '.js', '.jsx', '.mjs']
const find = (base) => tails.map((t) => base + t).find((f) => existsSync(f) && statSync(f).isFile())

export async function resolve_(specifier, context, nextResolve) {
  if (specifier.startsWith('.') && context.parentURL?.startsWith('file:')) {
    const parent = fileURLToPath(context.parentURL)
    const m = /^(.*[\\/]addons[\\/][^\\/]+[\\/]ui[\\/]src)[\\/]/.exec(parent)
    if (m) {
      const target = resolve(dirname(parent), specifier)
      const found = find(target)
      if (found) return nextResolve(pathToFileURL(found).href, context)
      const rel = relative(m[1], target)
      const hit = !rel.startsWith('..') && find(join(compatSrc, rel))
      if (hit) return nextResolve(pathToFileURL(hit).href, context)
    }
  }
  return nextResolve(specifier, context)
}
export { resolve_ as resolve }
