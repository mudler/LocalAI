// What the chat takes from the result of an add-on tool. The result is a JSON
// document the host broker wrote (see core/services/addons/tools); a proposal
// result only ever contributes its identifier. The approval card loads
// everything it shows from the host, so nothing a model or an add-on said in
// text is a preview.

import { validProposalId } from './slots.js'
import { artifactIds } from './artifactFacts.js'

export const ADDON_TOOL_PREFIX = 'addon__'
const NAME = /^addon__([a-z][a-z0-9-]{0,63})__([a-z][a-z0-9-]{0,63})$/

export function isAddonToolName(name) {
  return typeof name === 'string' && NAME.test(name)
}

function parse(result) {
  if (result && typeof result === 'object') return result
  if (typeof result !== 'string' || result === '' || result.length > 1 << 20) return null
  try {
    const v = JSON.parse(result)
    return v && typeof v === 'object' ? v : null
  } catch {
    return null
  }
}

// The reference a proposal result holds: the proposal identifier and the tool
// name. Anything else in the document is ignored. A result of another kind, a
// tool outside the add-on namespace and an identifier that is not well formed
// give null.
export function proposalReference(name, result) {
  if (!isAddonToolName(name)) return null
  const doc = parse(result)
  if (!doc || doc.kind !== 'proposal' || !doc.proposal || typeof doc.proposal !== 'object') return null
  const id = doc.proposal.id
  if (!validProposalId(id)) return null
  return { proposalId: id, tool: name.split('__')[2] || '' }
}

// The identifiers of the private files a result refers to, or an empty list.
// Only a well formed host identifier counts; the card loads name, size and
// expiry from the host and the download is the host's own route.
export function artifactReferences(name, result) {
  if (!isAddonToolName(name)) return []
  return artifactIds(parse(result))
}

// What the chat keeps in its stored history for a tool result of the add-on
// namespace: the reference of a proposal, or the identifiers of private files,
// and nothing else. A file identifier is opaque, belongs to one person and
// expires on its own, so the stored reference is not a way to the bytes; the
// card shows a file that expired as expired. Read results are shown while
// they stream and are never stored, so a stored conversation holds no rows of
// a database.
export function storedResult(name, result) {
  const ref = proposalReference(name, result)
  if (ref) return JSON.stringify({ kind: 'proposal', proposal: { id: ref.proposalId } })
  const ids = artifactReferences(name, result)
  if (ids.length > 0) return JSON.stringify({ kind: 'artifact', artifacts: ids.map((id) => ({ id })) })
  return null
}
