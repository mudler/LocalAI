#!/usr/bin/env node
// Completeness gate of the add-on locale bundles.
//
// An add-on page reads its words from JSON bundles named <namespace>.<language>.json
// in a folder `i18n` (addons/<id>/ui/src/i18n, and the shared connector pages in
// addons/internal/connector-ui/src/i18n). English is the source. Every language
// that the web UI ships (the folders of core/http/react-ui/public/locales) must
// have a bundle for each English one, and each bundle must:
//
//   - have every key of English, and no key that English lacks;
//   - have the same placeholders ({{name}} and %s) as English for each key;
//   - have no empty value.
//
// A key that is missing would show English text in a page of another language
// with no error anywhere. There is no baseline: a gap fails the check.
//
//   node addons/internal/i18ncheck/check.mjs
import { existsSync, readFileSync, readdirSync } from 'node:fs'
import { dirname, join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const REPO = join(here, '..', '..', '..')
const SOURCE = 'en'
// Bundles that are copies of a locale file of the web UI. The gaps of those are
// the web UI's own, tracked by core/http/react-ui/i18n-baseline.json.
const MIRRORS = new Set(['studio'])

// Every key path of a bundle, with its string value. Arrays count as leaves.
export function leaves(value, prefix = '', out = new Map()) {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    for (const [key, child] of Object.entries(value)) leaves(child, prefix ? `${prefix}.${key}` : key, out)
  } else {
    out.set(prefix, value)
  }
  return out
}

// The placeholders of a string, sorted, with repeats: {{name}} (any spacing) and %s, %d, %v.
export function placeholders(text) {
  if (typeof text !== 'string') return []
  const found = []
  for (const m of text.matchAll(/\{\{\s*-?\s*([A-Za-z0-9_.]+)\s*\}\}/g)) found.push(`{{${m[1]}}}`)
  for (const m of text.matchAll(/%(?:\d+\$)?[sdvf]/g)) found.push(m[0])
  return found.sort()
}

// The languages of the web UI.
export function languages(repo = REPO) {
  const dir = join(repo, 'core/http/react-ui/public/locales')
  return readdirSync(dir, { withFileTypes: true }).filter((e) => e.isDirectory()).map((e) => e.name).sort()
}

// The i18n folders of the add-on pages and of the shared packages.
export function bundleDirs(repo = REPO) {
  const addons = join(repo, 'addons')
  const out = []
  for (const entry of readdirSync(addons, { withFileTypes: true })) {
    if (!entry.isDirectory() || MIRRORS.has(entry.name)) continue
    const candidates = entry.name === 'internal'
      ? readdirSync(join(addons, 'internal'), { withFileTypes: true }).filter((e) => e.isDirectory()).map((e) => join(addons, 'internal', e.name, 'src', 'i18n'))
      : [join(addons, entry.name, 'ui', 'src', 'i18n')]
    for (const dir of candidates) if (existsSync(dir)) out.push(dir)
  }
  return out.sort()
}

// Returns a list of problems, each a line of text. langs defaults to the web UI languages.
export function check(repo = REPO, langs = languages(repo)) {
  const problems = []
  for (const dir of bundleDirs(repo)) {
    const rel = relative(repo, dir)
    const files = readdirSync(dir).filter((f) => f.endsWith('.json'))
    const namespaces = [...new Set(files.map((f) => f.match(/^(.+)\.[A-Za-z-]+\.json$/)?.[1]).filter(Boolean))].sort()
    for (const ns of namespaces) {
      const enFile = join(dir, `${ns}.${SOURCE}.json`)
      if (!existsSync(enFile)) { problems.push(`${rel}: ${ns}.${SOURCE}.json is missing`); continue }
      const source = leaves(JSON.parse(readFileSync(enFile, 'utf8')))
      for (const [key, value] of source) {
        if (typeof value !== 'string' || value.trim() === '') problems.push(`${rel}/${ns}.${SOURCE}.json: ${key} is not a non-empty string`)
      }
      for (const lang of langs.filter((l) => l !== SOURCE)) {
        const name = `${ns}.${lang}.json`
        const file = join(dir, name)
        if (!existsSync(file)) { problems.push(`${rel}: ${name} is missing`); continue }
        let bundle
        try { bundle = leaves(JSON.parse(readFileSync(file, 'utf8'))) } catch (e) { problems.push(`${rel}/${name}: not valid JSON (${e.message})`); continue }
        for (const [key, value] of source) {
          if (!bundle.has(key)) { problems.push(`${rel}/${name}: missing key ${key}`); continue }
          const got = bundle.get(key)
          if (typeof got !== 'string' || got.trim() === '') { problems.push(`${rel}/${name}: empty value for ${key}`); continue }
          const want = placeholders(value).join(' ')
          const have = placeholders(got).join(' ')
          if (want !== have) problems.push(`${rel}/${name}: placeholders of ${key} are [${have}], English has [${want}]`)
        }
        for (const key of bundle.keys()) if (!source.has(key)) problems.push(`${rel}/${name}: extra key ${key}`)
      }
      // A bundle for a language the web UI does not ship is a mistake in the name.
      for (const f of files) {
        const m = f.match(/^(.+)\.([A-Za-z-]+)\.json$/)
        if (m && m[1] === ns && m[2] !== SOURCE && !langs.includes(m[2])) problems.push(`${rel}/${f}: ${m[2]} is not a language of the web UI`)
      }
    }
  }
  return problems
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  const problems = check()
  if (problems.length) {
    console.error(`${problems.length} add-on locale problem(s):`)
    for (const p of problems) console.error(`  ${p}`)
    process.exit(1)
  }
  console.log(`add-on locale bundles are complete for ${languages().join(', ')}`)
}
