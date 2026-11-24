// Pure rules of the host's file card. An add-on never draws or links a file:
// it names a host artifact identifier and the host loads the record from its
// own authenticated route and draws name, size, expiry and the download. The
// only link the card builds is the host route of that identifier.

// The host's artifact identifiers: "a" and 32 hex digits, opaque and
// generated on the owner. An identifier that does not have this shape is
// never sent anywhere.
const ARTIFACT_ID = /^a[0-9a-f]{32}$/

export function validArtifactId(value) {
  return typeof value === 'string' && ARTIFACT_ID.test(value)
}

export function artifactStatusPath(id) {
  return validArtifactId(id) ? `/api/addons/artifacts/${id}` : ''
}

export function artifactDownloadPath(id) {
  return validArtifactId(id) ? `/api/addons/artifacts/${id}/download` : ''
}

// formatBytes writes a size with a binary unit, to one decimal at most.
export function formatBytes(n) {
  if (!Number.isFinite(n) || n < 0) return ''
  if (n < 1024) return `${n} B`
  const units = ['KiB', 'MiB', 'GiB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
  return `${v >= 10 ? Math.round(v) : Math.round(v * 10) / 10} ${units[i]}`
}

// The facts the card draws from the host record. Only fields the host
// documents are read; a download is offered only for a complete artifact that
// has not expired.
export function artifactFacts(record, now = Date.now()) {
  if (!record || typeof record !== 'object' || !validArtifactId(record.id)) {
    return { id: '', name: '', size: null, state: '', expiresAt: null, expired: false, downloadable: false }
  }
  const expiresAt = Date.parse(record.expiresAt)
  const expired = Number.isFinite(expiresAt) && expiresAt <= now
  const size = Number.isFinite(record.size) && record.size >= 0 ? record.size : null
  const state = typeof record.state === 'string' ? record.state : ''
  return {
    id: record.id,
    name: typeof record.name === 'string' ? record.name.slice(0, 200) : '',
    size,
    state,
    expiresAt: Number.isFinite(expiresAt) ? expiresAt : null,
    expired,
    downloadable: state === 'complete' && !expired,
  }
}

// The identifiers of the files an add-on tool result refers to. The result is
// the JSON document the host broker wrote; only well formed identifiers of a
// result of kind "artifact" count, at most a handful.
export const MAX_ARTIFACT_CARDS = 4
export function artifactIds(doc) {
  if (!doc || typeof doc !== 'object' || doc.kind !== 'artifact' || !Array.isArray(doc.artifacts)) return []
  const out = []
  for (const a of doc.artifacts) {
    if (a && validArtifactId(a.id) && !out.includes(a.id)) out.push(a.id)
    if (out.length >= MAX_ARTIFACT_CARDS) break
  }
  return out
}
