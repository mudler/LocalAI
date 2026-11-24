import test from 'node:test'
import assert from 'node:assert/strict'
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { check, placeholders } from '../check.mjs'

// A repo with two languages and one add-on bundle, written to a temporary folder.
function repoWith(bundles) {
  const repo = mkdtempSync(join(tmpdir(), 'i18ncheck-'))
  for (const l of ['en', 'it']) mkdirSync(join(repo, 'core/http/react-ui/public/locales', l), { recursive: true })
  const dir = join(repo, 'addons/demo/ui/src/i18n')
  mkdirSync(dir, { recursive: true })
  for (const [name, data] of Object.entries(bundles)) writeFileSync(join(dir, name), JSON.stringify(data))
  return repo
}

const EN = { title: 'Demo', hello: 'Hello {{name}}', rows_one: '{{count}} row', rows_other: '{{count}} rows', nested: { a: 'Save %s' } }
const IT = { title: 'Demo', hello: 'Ciao {{name}}', rows_one: '{{count}} riga', rows_other: '{{count}} righe', nested: { a: 'Salva %s' } }
const run = (it) => { const repo = repoWith({ 'demo.en.json': EN, ...(it ? { 'demo.it.json': it } : {}) }); try { return check(repo, ['en', 'it']) } finally { rmSync(repo, { recursive: true, force: true }) } }

test('i18n gate: a complete bundle passes', () => {
  assert.deepEqual(run(IT), [])
})

test('i18n gate: a missing language file, a missing key and an extra key fail', () => {
  assert.match(run(null).join('\n'), /demo\.it\.json is missing/)
  const { hello, ...gap } = IT
  assert.match(run(gap).join('\n'), /missing key hello/)
  assert.match(run({ ...IT, more: 'x' }).join('\n'), /extra key more/)
})

test('i18n gate: a changed or lost placeholder fails, in nested keys too', () => {
  assert.match(run({ ...IT, hello: 'Ciao {{nome}}' }).join('\n'), /placeholders of hello/)
  assert.match(run({ ...IT, rows_other: 'righe' }).join('\n'), /placeholders of rows_other/)
  assert.match(run({ ...IT, nested: { a: 'Salva' } }).join('\n'), /placeholders of nested\.a/)
})

test('i18n gate: an empty value fails, and spacing inside braces does not count as a change', () => {
  assert.match(run({ ...IT, title: ' ' }).join('\n'), /empty value for title/)
  assert.deepEqual(run({ ...IT, hello: 'Ciao {{ name }}' }), [])
  assert.deepEqual(placeholders('{{b}} %s {{a}} {{a}}'), ['%s', '{{a}}', '{{a}}', '{{b}}'])
})

test('i18n gate: the add-on bundles of this repository are complete', () => {
  assert.deepEqual(check(), [])
})
