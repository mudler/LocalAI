// Runs the node unit tests and prints one JSON line per test:
//   {"case":"unit: <name>","status":"passed"|"failed"|"skipped"}
// A test that is skipped or marked todo is reported as skipped, so the case
// inventory of scripts/addons/suites.json rejects it. No dependencies.
//
// Arguments name the test files to run (for example addons.test.mjs); without
// any, every *.test.mjs file runs. Each suite of suites.json names its own, so
// one suite's cases never appear in another's inventory.
import { run } from 'node:test'
import { readdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const only = process.argv.slice(2)
const names = readdirSync(here).filter(f => f.endsWith('.test.mjs')).sort()
for (const name of only) {
  if (!names.includes(name)) {
    console.log(JSON.stringify({ case: `unit: missing test file ${name}`, status: 'failed' }))
    process.exit(1)
  }
}
const files = names.filter(f => only.length === 0 || only.includes(f)).map(f => join(here, f))
let failed = false
const stream = run({ files, concurrency: 1 })
stream.on('test:pass', ({ name, skip, todo, nesting }) => {
  if (nesting > 0) return
  const status = skip || todo ? 'skipped' : 'passed'
  console.log(JSON.stringify({ case: `unit: ${name}`, status }))
})
stream.on('test:fail', ({ name, nesting, details }) => {
  if (nesting > 0) return
  failed = true
  console.log(JSON.stringify({ case: `unit: ${name}`, status: 'failed', error: String(details?.error?.message || '').slice(0, 300) }))
})
stream.on('test:stderr', ({ message }) => process.stderr.write(message))
stream.on('end', () => { process.exitCode = failed ? 1 : 0 })
stream.resume()
