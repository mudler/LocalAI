// A small i18next-compatible translator, for add-ons that carry the host's own
// locale files. It reads the same JSON shape (nested keys, `_one`/`_other`
// plural keys, `{{name}}` placeholders) and the same call forms, so page code
// written for react-i18next runs unchanged. No React in this file.

const resources = new Map() // language -> namespace -> object

// registerLocales({ media: { en: {...}, it: {...} }, talk: { en: {...} } })
export function registerLocales(byNamespace) {
  for (const [ns, languages] of Object.entries(byNamespace)) {
    for (const [lng, data] of Object.entries(languages)) {
      if (!resources.has(lng)) resources.set(lng, new Map())
      resources.get(lng).set(ns, data)
    }
  }
}

export function clearLocales() {
  resources.clear()
}

function candidates(language) {
  const lang = String(language || 'en')
  const out = [lang]
  const base = lang.split('-')[0]
  if (base !== lang) out.push(base)
  if (!out.includes('en')) out.push('en')
  return out
}

function walk(data, key) {
  if (!data || typeof data !== 'object') return undefined
  if (Object.prototype.hasOwnProperty.call(data, key)) return data[key]
  let node = data
  for (const part of key.split('.')) {
    if (node === null || typeof node !== 'object' || !Object.prototype.hasOwnProperty.call(node, part)) return undefined
    node = node[part]
  }
  return node
}

function pluralSuffix(language, count) {
  try {
    return new Intl.PluralRules(language).select(count)
  } catch {
    return count === 1 ? 'one' : 'other'
  }
}

function lookup(language, namespaces, key, count) {
  for (const lng of candidates(language)) {
    const byNs = resources.get(lng)
    if (!byNs) continue
    for (const ns of namespaces) {
      const data = byNs.get(ns)
      if (!data) continue
      if (typeof count === 'number') {
        for (const suffix of [pluralSuffix(lng, count), 'other']) {
          const hit = walk(data, `${key}_${suffix}`)
          if (hit !== undefined) return hit
        }
      }
      const hit = walk(data, key)
      if (hit !== undefined) return hit
    }
  }
  return undefined
}

function interpolate(text, vars) {
  return text.replace(/\{\{\s*-?\s*([A-Za-z0-9_.]+)\s*\}\}/g, (match, name) => (
    vars && Object.prototype.hasOwnProperty.call(vars, name) && vars[name] !== undefined ? String(vars[name]) : match
  ))
}

// translator(language, namespaces) returns t(key, defaultOrOptions, options).
export function translator(language, namespaces) {
  const list = (Array.isArray(namespaces) ? namespaces : [namespaces || 'common']).filter(Boolean)
  return function t(key, second, third) {
    const options = typeof second === 'object' && second !== null ? second : (third || {})
    const fallback = typeof second === 'string' ? second : options.defaultValue
    let name = String(key)
    let spaces = list
    const colon = name.indexOf(':')
    if (colon > 0) { spaces = [name.slice(0, colon)]; name = name.slice(colon + 1) }
    const count = typeof options.count === 'number' ? options.count : undefined
    const found = lookup(language, spaces, name, count)
    if (found !== undefined && typeof found === 'object') return options.returnObjects ? found : (fallback ?? name)
    const text = typeof found === 'string' ? found : (typeof fallback === 'string' ? fallback : name)
    return interpolate(text, options)
  }
}
