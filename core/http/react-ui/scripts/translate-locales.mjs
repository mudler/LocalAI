#!/usr/bin/env node
// Bootstrap non-English locale JSON files from the English source.
//
// Usage:
//   # Fill missing keys in non-English locales by copying the English value
//   # (placeholder — translators / community can refine afterwards).
//   node scripts/translate-locales.mjs --copy
//
//   # Translate via OpenAI (default provider).
//   OPENAI_API_KEY=sk-... node scripts/translate-locales.mjs --translate
//
//   # Translate via Anthropic.
//   ANTHROPIC_API_KEY=sk-ant-... node scripts/translate-locales.mjs --translate --provider=anthropic
//
//   # Dry-run to see what would change.
//   node scripts/translate-locales.mjs --translate --dry-run
//
// Behavior:
//   - Reads public/locales/en/*.json as source of truth.
//   - For each other locale (it, es, de, zh-CN), opens the matching file
//     (or creates it). Walks the source object; for each leaf string:
//       * If the target already has a non-empty translation, leave it.
//       * If --copy mode, fill with the English value.
//       * If --translate mode, send the value to the LLM with the key path
//         and locale name as context.
//   - Writes the updated file with sorted keys, 2-space indent.
//
// The script is idempotent: existing translations are preserved unless
// --overwrite is passed. Run it whenever new keys are added in en/.

import { readFileSync, writeFileSync, readdirSync, existsSync, mkdirSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const __dirname = dirname(fileURLToPath(import.meta.url))
const ROOT = join(__dirname, '..')
const LOCALES_DIR = join(ROOT, 'public', 'locales')
const SOURCE_LOCALE = 'en'
const TARGET_LOCALES = ['it', 'es', 'de', 'zh-CN']

const LANGUAGE_NAMES = {
  it: 'Italian',
  es: 'Spanish',
  de: 'German',
  'zh-CN': 'Simplified Chinese',
}

const argv = process.argv.slice(2)
const args = new Set(argv)
const MODE = args.has('--translate') ? 'translate' : 'copy'
const DRY_RUN = args.has('--dry-run')
const OVERWRITE = args.has('--overwrite')
const providerArg = argv.find(a => a.startsWith('--provider='))?.split('=')[1]
const PROVIDER = providerArg || (process.env.ANTHROPIC_API_KEY && !process.env.OPENAI_API_KEY ? 'anthropic' : 'openai')

function readJson(path) {
  return JSON.parse(readFileSync(path, 'utf8'))
}

function writeJson(path, data) {
  if (DRY_RUN) {
    console.log(`[dry-run] would write ${path}`)
    return
  }
  const dir = dirname(path)
  if (!existsSync(dir)) mkdirSync(dir, { recursive: true })
  writeFileSync(path, JSON.stringify(data, null, 2) + '\n', 'utf8')
}

function listNamespaces() {
  return readdirSync(join(LOCALES_DIR, SOURCE_LOCALE))
    .filter((f) => f.endsWith('.json'))
    .map((f) => f.replace(/\.json$/, ''))
}

async function translateString(value, locale, keyPath) {
  if (MODE === 'copy') return value
  const language = LANGUAGE_NAMES[locale] || locale
  const prompt = `Translate the following UI string from English to ${language}.
Preserve {{interpolation}} placeholders exactly. Preserve trailing punctuation
and ellipses. Do not add quotes around the result. Reply with the translation only.

Key: ${keyPath}
String: ${value}`

  if (PROVIDER === 'anthropic') {
    const apiKey = process.env.ANTHROPIC_API_KEY
    if (!apiKey) throw new Error('ANTHROPIC_API_KEY required for --provider=anthropic')
    const res = await fetch('https://api.anthropic.com/v1/messages', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'x-api-key': apiKey,
        'anthropic-version': '2023-06-01',
      },
      body: JSON.stringify({
        model: process.env.TRANSLATE_MODEL || 'claude-haiku-4-5-20251001',
        max_tokens: 1024,
        system: 'You are a professional UI string translator. Reply with the translation only, no preamble.',
        messages: [{ role: 'user', content: prompt }],
      }),
    })
    if (!res.ok) {
      const body = await res.text()
      throw new Error(`Anthropic API ${res.status}: ${body}`)
    }
    const data = await res.json()
    return (data.content?.[0]?.text || '').trim()
  }

  const apiKey = process.env.OPENAI_API_KEY
  if (!apiKey) throw new Error('OPENAI_API_KEY required for --provider=openai')
  const res = await fetch('https://api.openai.com/v1/chat/completions', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${apiKey}`,
    },
    body: JSON.stringify({
      model: process.env.TRANSLATE_MODEL || 'gpt-4o-mini',
      temperature: 0.2,
      messages: [
        { role: 'system', content: 'You are a professional UI string translator.' },
        { role: 'user', content: prompt },
      ],
    }),
  })
  if (!res.ok) {
    const body = await res.text()
    throw new Error(`OpenAI API ${res.status}: ${body}`)
  }
  const data = await res.json()
  return data.choices[0].message.content.trim()
}

async function walk(source, target, locale, prefix = '') {
  for (const key of Object.keys(source)) {
    const path = prefix ? `${prefix}.${key}` : key
    const sv = source[key]
    if (sv && typeof sv === 'object' && !Array.isArray(sv)) {
      target[key] = target[key] && typeof target[key] === 'object' ? target[key] : {}
      await walk(sv, target[key], locale, path)
    } else if (typeof sv === 'string') {
      const existing = target[key]
      const hasTranslation = typeof existing === 'string' && existing.trim().length > 0
      if (hasTranslation && !OVERWRITE) continue
      try {
        target[key] = await translateString(sv, locale, path)
        process.stdout.write('.')
      } catch (err) {
        process.stdout.write('!')
        console.error(`\n  failed at ${locale}/${path}: ${err.message}`)
        target[key] = existing ?? ''
      }
    }
  }
}

async function main() {
  const namespaces = listNamespaces()
  console.log(`mode=${MODE} provider=${PROVIDER} locales=${TARGET_LOCALES.join(',')} namespaces=${namespaces.join(',')}`)
  for (const ns of namespaces) {
    const sourcePath = join(LOCALES_DIR, SOURCE_LOCALE, `${ns}.json`)
    const source = readJson(sourcePath)
    for (const locale of TARGET_LOCALES) {
      const targetPath = join(LOCALES_DIR, locale, `${ns}.json`)
      const target = existsSync(targetPath) ? readJson(targetPath) : {}
      process.stdout.write(`${locale}/${ns} `)
      await walk(source, target, locale)
      process.stdout.write('\n')
      writeJson(targetPath, target)
    }
  }
  console.log('done.')
}

main().catch((err) => {
  console.error(err)
  process.exit(1)
})
