// Runs the node unit tests of the Tables UI package and prints one JSON line
// per test, the same shape as the host's runner (core/http/react-ui/tests/unit):
//   {"case":"unit: <name>","status":"passed"|"failed"|"skipped"}
import { run } from 'node:test'
import { readdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const names = readdirSync(here).filter(f => f.endsWith('.test.mjs')).sort()
let failed = false
const stream = run({ files: names.map(f => join(here, f)), concurrency: 1 })
stream.on('test:pass', ({ name, skip, todo, nesting }) => {
  if (nesting > 0) return
  console.log(JSON.stringify({ case: `unit: ${name}`, status: skip || todo ? 'skipped' : 'passed' }))
})
stream.on('test:fail', ({ name, nesting, details }) => {
  if (nesting > 0) return
  failed = true
  console.log(JSON.stringify({ case: `unit: ${name}`, status: 'failed', error: String(details && details.error && details.error.message || 'failed').slice(0, 400) }))
})
stream.on('end', () => { process.exit(failed ? 1 : 0) })
stream.resume()
