// SPDX-License-Identifier: MIT
// Repository UI gate entrypoint, using its standard case-result format.
import { run } from 'node:test'
import { fileURLToPath } from 'node:url'
let failed = false
const stream = run({ files: [fileURLToPath(new URL('./state.test.mjs', import.meta.url))] })
stream.on('test:pass', ({ name, skip, todo, nesting }) => {
  if (nesting === 0) console.log(JSON.stringify({ case: `unit: ${name}`, status: skip || todo ? 'skipped' : 'passed' }))
})
stream.on('test:fail', ({ name, nesting, details }) => {
  failed = true
  if (nesting === 0) console.log(JSON.stringify({ case: `unit: ${name}`, status: 'failed', error: String(details?.error?.message || 'failed').slice(0, 400) }))
})
stream.on('end', () => { process.exit(failed ? 1 : 0) })
stream.resume()
