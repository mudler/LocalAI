// What the proposal card of a Gmail change shows, from the plain data the host
// hands it. No React, so a node test drives it. Nothing here comes from model
// prose except the addresses, the subject and the text, which are shown as
// text and never as markup.

const ID = /^[A-Za-z0-9_-]{1,256}$/
const str = (v) => (typeof v === 'string' ? v : '')
const id = (v) => (ID.test(str(v)) ? str(v) : '')
const list = (v) => (Array.isArray(v) ? v.filter((x) => typeof x === 'string') : [])
const joined = (v) => (Array.isArray(v) ? list(v).join(', ') : str(v))

// The facts of one proposal. `args` are the stored arguments (the full text of
// a draft); `sample` is the preview the add-on made, which for a send holds
// the draft as it is in Gmail.
export function proposalView(tool, args, sample) {
  const a = args && typeof args === 'object' ? args : {}
  const s = sample && typeof sample === 'object' ? sample : {}
  const storedBody = typeof a.body === 'string'
  const body = storedBody ? a.body : str(s.body)
  return {
    tool: str(tool),
    to: joined(a.to) || joined(s.to),
    cc: joined(a.cc) || joined(s.cc),
    bcc: joined(s.bcc),
    subject: str(a.subject) || str(s.subject),
    body,
    hasBody: storedBody || typeof s.body === 'string',
    bytes: Number.isInteger(s.bodyBytes) ? s.bodyBytes : body.length,
    // The stored text is whole; the preview is cut. Say so only when the card
    // could not get the whole text.
    cut: s.bodyCut === true && !storedBody,
    replyToMessageId: id(a.replyToMessageId) || id(s.replyToMessageId),
    replyToFrom: str(s.replyToFrom),
    replyToSubject: str(s.replyToSubject),
    draftId: id(a.draftId) || id(s.draftId),
    attachments: list(s.attachments),
  }
}
