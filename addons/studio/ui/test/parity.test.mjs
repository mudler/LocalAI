import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { hasKey, hostUi, iconLiterals, iconNames, literalKeys, sourceFiles, spriteNames, text, unresolvedImports } from '../../../internal/host-compat/test/parity.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const src = join(here, '../src')
const files = sourceFiles(src)
const LANGUAGES = ['en', 'it', 'es', 'de', 'zh-CN', 'id', 'ko', 'pt-BR']

test('the media locale files are byte copies of the host ones', () => {
  for (const lng of LANGUAGES) {
    assert.equal(
      readFileSync(join(src, `i18n/media.${lng}.json`), 'utf8'),
      readFileSync(join(hostUi, `public/locales/${lng}/media.json`), 'utf8'),
      lng,
    )
  }
})

test('every literal key the pages read is in the English media locale', () => {
  const en = JSON.parse(readFileSync(join(src, 'i18n/media.en.json'), 'utf8'))
  const keys = literalKeys(text(files))
  assert.ok(keys.size > 150, `found only ${keys.size} literal keys`)
  const missing = [...keys].filter((k) => !hasKey(en, k))
  assert.deepEqual(missing, [])
})

test('every icon the pages name is one the SDK allows', async () => {
  const allowed = await iconNames()
  const sprite = spriteNames()
  const used = iconLiterals(text(files))
  assert.ok(used.size > 20, `found only ${used.size} icons`)
  // Each is also a real sprite icon, so none draws the neutral glyph.
  assert.deepEqual([...used].filter((n) => !sprite.has(n) || !allowed.has(n)), [])
})

test('the source imports only its own files, the compat stand-ins and the modules they answer', () => {
  assert.deepEqual(unresolvedImports(src), [])
})

test('the source holds no direct network call: the pages go through the stand-in API client', () => {
  const direct = []
  for (const f of files) {
    if (f.endsWith('test.mjs')) continue
    for (const line of readFileSync(f, 'utf8').split('\n')) {
      if (/\bfetch\(/.test(line) && !/^\s*\/\//.test(line)) direct.push(`${f.slice(src.length)}: ${line.trim()}`)
    }
  }
  assert.deepEqual(direct, [])
})
