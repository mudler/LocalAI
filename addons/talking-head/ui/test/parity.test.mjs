import test from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { hasKey, iconLiterals, iconNames, literalKeys, sourceFiles, spriteNames, text, unresolvedImports } from '../../../internal/host-compat/test/parity.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const src = join(here, '../src')
const files = sourceFiles(src)

test('every literal key the page reads is in the English head locale', () => {
  const en = JSON.parse(readFileSync(join(src, 'i18n/head.en.json'), 'utf8'))
  const keys = literalKeys(text(files))
  assert.ok(keys.size > 40, `found only ${keys.size} literal keys`)
  assert.deepEqual([...keys].filter((k) => !hasKey(en, k)), [])
})

test('every icon the page names is one the SDK allows', async () => {
  const allowed = await iconNames()
  const sprite = spriteNames()
  const used = iconLiterals(text(files))
  assert.ok(used.size > 5, `found only ${used.size} icons`)
  assert.deepEqual([...used].filter((n) => !sprite.has(n) || !allowed.has(n)), [])
})

test('the source imports only its own files, the compat stand-ins and the modules they answer', () => {
  assert.deepEqual(unresolvedImports(src), [])
})

test('the source holds no direct network call', () => {
  const direct = []
  for (const f of files) {
    for (const line of readFileSync(f, 'utf8').split('\n')) {
      if (/\bfetch\(/.test(line) && !/^\s*\/\//.test(line)) direct.push(`${f.slice(src.length)}: ${line.trim()}`)
    }
  }
  assert.deepEqual(direct, [])
})

test('the shared Talk components it bundles import only what both builds resolve', () => {
  // Sheet and Transcript are bundled by this add-on from the Talk source tree,
  // where this build's stand-ins do not apply: their imports must be bare
  // modules or real files.
  const talk = join(here, '../../../talk/ui/src/components/talk')
  for (const name of ['Sheet.jsx', 'Transcript.jsx']) {
    const source = readFileSync(join(talk, name), 'utf8')
    for (const m of source.matchAll(/^\s*import\b[^'"\n;]*?\bfrom\s*['"]([^'"]+)['"]/gm)) {
      const spec = m[1]
      if (!spec.startsWith('.')) { assert.ok(['react', 'react-dom'].includes(spec), `${name}: ${spec}`); continue }
      assert.ok(existsSync(join(talk, spec)), `${name}: ${spec} is not a real file`)
    }
  }
})
