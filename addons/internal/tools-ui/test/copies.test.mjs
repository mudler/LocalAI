import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

// The add-on builder refuses a bundle that imports a core UI file, so these
// files are copies. They must stay byte-equal to the core originals.
const here = dirname(fileURLToPath(import.meta.url))
const src = join(here, '../src')
const core = resolve(here, '../../../../core/http/react-ui/src')
const read = (p) => readFileSync(p, 'utf8')

test('src/tools.js is a byte copy of the core utils/tools.js', () => {
  assert.equal(read(join(src, 'tools.js')), read(join(core, 'utils/tools.js')))
})

test('src/tools.css is a byte copy of the core components/tools/tools.css', () => {
  assert.equal(read(join(src, 'tools.css')), read(join(core, 'components/tools/tools.css')))
})

test('src/format.js holds formatBytes exactly as the core utils/format.js does', () => {
  const fn = (text) => /export function formatBytes[\s\S]*?\n}\n/.exec(text)?.[0]
  assert.ok(fn(read(join(core, 'utils/format.js'))), 'formatBytes found in core')
  assert.equal(fn(read(join(src, 'format.js'))), fn(read(join(core, 'utils/format.js'))))
})
