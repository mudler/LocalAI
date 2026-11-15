// The UI kit package (packages/ui-kit): another React app imports it with
// React as its only dependency, so it must not reach into the web UI, a
// router or a translation library; its CSS must define every token its rules
// read; and its English defaults must say what the web UI says in English.
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { dirname, join, relative, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import { isCurrent as iconsCurrent } from '../../packages/ui-kit/scripts/gen-icons.mjs'
import { STATUS, STATUS_WORDS } from '../../packages/ui-kit/src/status.js'
import { LOAD_ERROR_LABELS } from '../../packages/ui-kit/src/loadErrorModel.js'

const root = join(dirname(fileURLToPath(import.meta.url)), '../..')
const kit = join(root, 'packages/ui-kit')
const read = (p) => readFileSync(p, 'utf8')
const noComments = (css) => css.replace(/\/\*[\s\S]*?\*\//g, '')

function files(dir, out = []) {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) files(full, out)
    else out.push(full)
  }
  return out
}

test('ui-kit: react is the only dependency', () => {
  const pkg = JSON.parse(read(join(kit, 'package.json')))
  assert.deepEqual(Object.keys(pkg.peerDependencies || {}), ['react'])
  assert.equal(pkg.dependencies, undefined)
})

test('ui-kit: the sources import only react and their own files', () => {
  const IMPORT = /(?:\bfrom\s+|\bimport\s*\(\s*|\bimport\s+)(["'])([^"']+)\1/g
  for (const file of files(join(kit, 'src')).filter((f) => /\.jsx?$/.test(f))) {
    for (const m of read(file).matchAll(IMPORT)) {
      const spec = m[2]
      const name = relative(kit, file)
      if (spec.startsWith('.')) {
        const target = resolve(dirname(file), spec)
        assert.ok(target.startsWith(kit + sep), `${name} imports ${spec}, outside the package`)
      } else {
        assert.ok(spec === 'react' || spec.startsWith('react/'), `${name} imports ${spec}; react is the only dependency`)
      }
    }
  }
})

test('ui-kit: src/icons.js is generated from the vendored sprite', () => {
  assert.ok(iconsCurrent(), 'src/icons.js is out of date: run node packages/ui-kit/scripts/gen-icons.mjs')
})

test('ui-kit: styles.css loads the files in the documented order', () => {
  const imports = [...read(join(kit, 'styles.css')).matchAll(/@import '([^']+)'/g)].map((m) => m[1])
  assert.deepEqual(imports, [
    './vendor/tokens.css', './themes/localai.css', './vendor/motion.css',
    './vendor/components.css', './css/defaults.css', './css/kit.css',
  ])
})

// The value of a custom property in the web UI's theme bridge.
const theme = noComments(read(join(root, 'src/theme.css')))
const themeValue = (name) => theme.match(new RegExp(`(?:^|[\\s;{])${name}:\\s*([^;]+);`))?.[1].trim()

test('ui-kit: each default token has the value of src/theme.css', () => {
  const defaults = noComments(read(join(kit, 'css/defaults.css')))
  const decls = [...defaults.matchAll(/(--[a-z0-9-]+):\s*([^;]+);/g)]
  assert.ok(decls.length > 40, 'defaults.css holds the product tokens')
  for (const [, name, value] of decls) assert.equal(value.trim(), themeValue(name), name)
})

test('ui-kit: the CSS defines every token the kit rules read', () => {
  // Set at run time by a component, or optional with a fallback in the rule.
  const RUNTIME = new Set(['--dk-at', '--dk-cols', '--dk-fill', '--dk-min', '--dk-undo', '--dk-value', '--dk-w',
    '--lai-stroke', '--toast-lift', '--toast-ms', '--page-max'])
  const order = ['vendor/tokens.css', 'themes/localai.css', 'vendor/motion.css', 'vendor/components.css', 'css/defaults.css', 'css/kit.css']
  const css = order.map((f) => noComments(read(join(kit, f)))).join('\n')
  const defined = new Set([...css.matchAll(/(--[a-z0-9-]+)\s*:/g)].map((m) => m[1]))
  const missing = [...new Set([...css.matchAll(/var\((--[a-z0-9-]+)/g)].map((m) => m[1]))]
    .filter((name) => !defined.has(name) && !RUNTIME.has(name))
  assert.deepEqual(missing, [])
})

test('ui-kit: the English words match the English of the web UI', () => {
  const en = JSON.parse(read(join(root, 'public/locales/en/common.json')))
  for (const s of Object.values(STATUS)) assert.equal(STATUS_WORDS[s.label], en.chip[s.label], s.label)
  assert.equal(LOAD_ERROR_LABELS.title, en.ui.loadError.title)
  assert.equal(LOAD_ERROR_LABELS.tryAgain, en.ui.tryAgain)
  assert.equal(LOAD_ERROR_LABELS.nothingChanged, en.ui.loadError.nothingChanged)
  assert.equal(LOAD_ERROR_LABELS.serverSaid('locked'), en.ui.loadError.serverSaid.replace('{{message}}', 'locked'))
})
