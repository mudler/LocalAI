import test from 'node:test'
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { dirname, join, resolve } from 'node:path'
import { build } from '../build.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const hostUi = resolve(here, '../../../../core/http/react-ui')
const require = createRequire(join(hostUi, 'package.json'))

test('build: the bundle is plain JavaScript that names nothing it does not define', async () => {
  const dir = await build()
  const { ESLint } = await import(require.resolve('eslint'))
  const globals = require('globals')
  const eslint = new ESLint({
    cwd: dir,
    overrideConfigFile: true,
    overrideConfig: [{
      files: ['**/*.js'],
      languageOptions: { ecmaVersion: 2024, sourceType: 'module', globals: globals.browser },
      rules: { 'no-undef': 'error', 'no-const-assign': 'error', 'no-dupe-keys': 'error', 'no-redeclare': 'error' },
    }],
  })
  const results = await eslint.lintFiles([join(dir, 'ui/tables.js')])
  const messages = results.flatMap(r => r.messages.map(m => `${m.line}: ${m.ruleId} ${m.message}`))
  assert.deepEqual(messages, [])
})

test('build: the module exports createAddon and imports nothing', async () => {
  const dir = await build()
  const { readFileSync, statSync } = await import('node:fs')
  const js = readFileSync(join(dir, 'ui/tables.js'), 'utf8')
  assert.match(js, /export\s*\{[^}]*createAddon/)
  assert.doesNotMatch(js, /^\s*import\s/m)
  assert.doesNotMatch(js, /import\(/)
  assert.ok(statSync(join(dir, 'ui/icon.png')).size <= 256 * 1024)
  const css = readFileSync(join(dir, 'ui/tables.css'), 'utf8')
  assert.doesNotMatch(css, /url\(|@import|expression\(/)
  const classes = [...css.matchAll(/\.([a-z][a-z0-9_-]*)/gi)].map(m => m[1])
  for (const c of classes) assert.match(c, /^tbl-/, `class ${c} is not namespaced`)
})
