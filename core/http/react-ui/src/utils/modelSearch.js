// Pure helpers of the "Search online" panel on the import page. The panel
// talks to GET /api/models/search; nothing here touches the DOM.
import { formatBytes } from './format.js'

// The providers the server knows, in the order the panel lists them.
export const SEARCH_PROVIDERS = ['huggingface', 'pirateface']

export const SEARCH_SORTS = ['downloads', 'likes', 'updated']

// The query string of a search. `providers` is the list of enabled names; all
// of them on is sent as nothing, so the server's default applies and a new
// provider shows up without a UI change.
export function searchParams({ query, providers, ggufOnly, sort }) {
  const out = { q: String(query || '').trim() }
  const on = SEARCH_PROVIDERS.filter(p => providers?.[p])
  if (on.length > 0 && on.length < SEARCH_PROVIDERS.length) out.providers = on.join(',')
  out.format = ggufOnly === false ? 'any' : 'gguf'
  if (sort && sort !== 'downloads') out.sort = sort
  return out
}

// 1234 -> "1.2k", 3400000 -> "3.4M". Small numbers stay as they are.
export function compactCount(n) {
  if (!Number.isFinite(n) || n <= 0) return ''
  if (n < 1000) return String(Math.round(n))
  const units = [[1e9, 'B'], [1e6, 'M'], [1e3, 'k']]
  for (const [size, suffix] of units) {
    if (n >= size) return `${parseFloat((n / size).toFixed(1))}${suffix}`
  }
  return String(n)
}

// The facts shown under a result, in a fixed order, only those the provider
// gave. Each is { key, value }; the panel turns the key into a label.
export function resultFacts(r) {
  const facts = []
  if (r?.size_bytes > 0) facts.push({ key: 'size', value: formatBytes(r.size_bytes) })
  if (r?.seeders != null && r.provider === 'pirateface') facts.push({ key: 'seeders', value: String(r.seeders) })
  const downloads = compactCount(r?.downloads)
  if (downloads) facts.push({ key: 'downloads', value: downloads })
  const likes = compactCount(r?.likes)
  if (likes) facts.push({ key: 'likes', value: likes })
  if (r?.license) facts.push({ key: 'license', value: r.license })
  if (r?.pipeline_tag) facts.push({ key: 'task', value: r.pipeline_tag })
  return facts
}

// Providers that failed, for the notice above the list.
export function failedProviders(response) {
  return (response?.providers || []).filter(p => p && p.ok === false)
}

// A stable key for a result row: two providers can list the same repository.
export function resultKey(r) {
  return `${r.provider}:${r.id}`
}
