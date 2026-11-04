// What the proposal card of a Google Drive change shows, from the plain data
// the host hands it. No React, so a node test drives it. Nothing here comes
// from model prose except the names and the text that will be saved, which are
// shown as text.

const ID = /^[A-Za-z0-9_-]{1,128}$/
const str = (v) => (typeof v === 'string' ? v : '')
const id = (v) => (ID.test(str(v)) ? str(v) : '')

// The facts of one proposal: the tool, the files and the text that will be
// saved. `args` are the stored arguments (the full text); `sample` is the short
// preview the add-on made.
export function proposalView(tool, args, sample) {
  const a = args && typeof args === 'object' ? args : {}
  const s = sample && typeof sample === 'object' ? sample : {}
  const content = typeof a.content === 'string' ? a.content : str(s.content)
  const hasContent = typeof a.content === 'string' || typeof s.content === 'string'
  const bytes = Number.isInteger(s.contentBytes) ? s.contentBytes : content.length
  return {
    tool: str(tool),
    fileId: id(a.fileId) || id(s.fileId),
    targetName: str(s.targetName),
    name: str(a.name) || str(s.name),
    parent: id(a.parent) || id(s.parent),
    parentName: str(s.parentName),
    newParent: id(a.newParent) || id(s.newParent),
    newParentName: str(s.newParentName),
    mimeType: str(a.mimeType) || str(s.mimeType),
    hasContent,
    content,
    // The stored text is whole; the preview is cut. Say so only when the card
    // could not get the whole text.
    cut: s.contentCut === true && typeof a.content !== 'string',
    bytes,
  }
}
