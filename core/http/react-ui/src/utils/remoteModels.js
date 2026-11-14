// Pure helpers of the Explore tab's online sources (Hugging Face, Pirate
// Face). The rows of an online search go through the same table as the
// gallery, so these turn a search result into the fields that table reads.
// Nothing here touches the DOM.
import { compactCount } from './modelSearch.js'
import { fitFor } from './modelLedger.js'

// The online sources, in the order their chips appear. `p2p` puts the small
// tag on the chip.
export const REMOTE_SOURCES = [
  { id: 'huggingface', p2p: false },
  { id: 'pirateface', p2p: true },
]

// "Load more" asks the provider for this many more results each time. The
// server caps one provider at REMOTE_MAX_RESULTS.
export const REMOTE_PAGE_STEP = 20
export const REMOTE_MAX_RESULTS = 50

export function remoteSource(id) {
  return REMOTE_SOURCES.find(s => s.id === id) || null
}

// Rows are keyed by provider and repository, never by name: two providers can
// list one repository, and a row's name is not a gallery name.
export function remoteKey(result) {
  return `${result.provider}:${result.id}`
}

// The provider and repository of a key made by remoteKey, or null.
export function parseRemoteKey(key) {
  const m = /^([a-z]+):([^/\s]+\/[^/\s]+)$/.exec(String(key || ''))
  return m && remoteSource(m[1]) ? { provider: m[1], id: m[2] } : null
}

// The query of GET /api/models/search for one source.
export function remoteSearchParams({ provider, term, limit, ggufOnly }) {
  const out = { q: String(term || '').trim(), providers: provider, limit: limit || REMOTE_PAGE_STEP }
  out.format = ggufOnly === false ? 'any' : 'gguf'
  return out
}

// Whether the list can be asked for more: the provider filled the page it was
// asked for, and the cap is not reached.
export function canLoadMore(results, limit) {
  return results.length >= limit && limit < REMOTE_MAX_RESULTS
}

export function nextLimit(limit) {
  return Math.min(REMOTE_MAX_RESULTS, limit + REMOTE_PAGE_STEP)
}

// The line under a row's name: author, licence and how popular it is. Only the
// facts the provider gave. `labels` has the words: downloads, seeds, gated.
export function remoteSubline(result, labels) {
  const parts = []
  const owner = String(result.id || '').split('/')[0]
  if (owner) parts.push(owner)
  if (result.license) parts.push(result.license)
  const downloads = compactCount(result.downloads)
  if (downloads) parts.push(`${downloads} ${labels.downloads}`)
  if (result.provider === 'pirateface' && result.seeders > 0) parts.push(`${result.seeders} ${labels.seeds}`)
  return parts.join(' · ')
}

// A search result as a row of the Explore table. `name` is the key: that is
// what the table selects, focuses, estimates and installs by. `title` is the
// repository, which is what a person reads.
export function remoteRow(result, { installedName, sourceLabel, labels }) {
  const key = remoteKey(result)
  return {
    name: key,
    title: result.id,
    remote: true,
    provider: result.provider,
    repo: result.id,
    uri: result.uri,
    installed: !!(result.installed || installedName),
    installedName: result.installed_name || installedName || '',
    backend: result.backend || '',
    license: result.license || '',
    has_variants: !!result.has_variants,
    gated: !!result.gated,
    sub: remoteSubline(result, labels),
    tags: (result.tags || []).filter(t => !t.startsWith('license:') && !t.includes(':')).slice(0, 12),
    urls: result.url ? [result.url] : [],
    gallery: sourceLabel,
    pipelineTag: result.pipeline_tag || '',
  }
}

export function basename(path) {
  const i = String(path || '').lastIndexOf('/')
  return i < 0 ? String(path || '') : path.slice(i + 1)
}

// The variant list of ModelDetail, from the file listing of a repository and
// its per-variant estimates. A variant is one quantization; its `model` is its
// first file, which is what the estimate route and the install pick by.
//
// fits is false only for a variant known not to fit; a variant with no estimate
// yet is not marked, so the list does not look worse while sizing.
export function remoteVariantData(files, estimate, { contextSize, budget, ramAvailable, backend, labels }) {
  if (!files) return null
  const est = new Map((estimate?.variants || []).map(v => [v.file, v]))
  const variants = (files.variants || []).map(v => {
    const need = est.get(v.file)?.estimates?.[String(contextSize)]?.vramBytes
    const fit = fitFor(need, budget, ramAvailable)
    return {
      model: v.file,
      // The build is named by its quantization; the file name is long and
      // the pane is narrow. The format column says GGUF and the shard count.
      label: v.quantization,
      backend: backend || files.backend || '',
      quantization: v.shards > 1 ? `GGUF ×${v.shards}` : 'GGUF',
      memory_bytes: v.size_bytes || 0,
      fits: fit ? fit.state !== 'over' : true,
      // Only a verdict that is not "fits" is worth a badge in a narrow list;
      // a fitting build shows no warning.
      fitNote: fit && fit.state !== 'fits' ? labels[fit.state] : '',
      fitTone: fit ? (fit.state === 'fits' ? 'success' : fit.state === 'spill' ? 'warning' : 'error') : '',
      is_base: false,
      recommended: !!v.recommended,
    }
  })
  // No auto_selected: that badge says LocalAI picked the build for this host.
  // Here the recommended build is a default, and carries its own badge.
  return { loading: false, variants, auto_selected: '' }
}

// The file a repository's install uses when nobody chose: the recommended
// variant, else the first.
export function defaultVariantFile(files) {
  const vs = files?.variants || []
  return (vs.find(v => v.recommended) || vs[0] || {}).file || ''
}

// The estimate of one variant in the shape the gallery's inspector reads
// (sizeBytes, sizeDisplay, estimates keyed by context), or null.
export function estimateOfVariant(estimate, file) {
  if (!estimate) return null
  if (!file || estimate.file === file) return estimate
  const v = (estimate.variants || []).find(x => x.file === file)
  return v ? { ...estimate, ...v, file: v.file } : estimate
}

// The body of POST /models/import-uri for a chosen variant. A repository with
// no quantizations (not GGUF) is imported as it is.
export function importBody(row, files, file) {
  const body = { uri: row.uri }
  const v = (files?.variants || []).find(x => x.file === file)
  if (v?.preference) body.preferences = { quantizations: v.preference }
  return body
}

// Why a variant cannot be installed from here, or ''.
export function installBlock(row, files) {
  if (row.provider === 'pirateface' && (files?.gated || row.gated)) return 'gatedP2p'
  return ''
}

// A message for a failed request of the remote routes. A 429 says to wait.
export function remoteErrorKind(err) {
  if (err?.status === 429) return 'rateLimited'
  if (err?.status === 404) return 'notFound'
  return 'failed'
}

// The "Fits in memory" rule for an online row, the one the gallery rows use:
// hide only a row known not to fit the budget. A row whose estimate has not
// come back, or could not be made, stays.
export function remoteFitsVisible(estimate, contextSize, budget, ramAvailable) {
  const vram = estimate?.estimates?.[String(contextSize)]?.vramBytes
  const fit = fitFor(vram, budget, ramAvailable)
  return !fit || fit.state === 'fits'
}
