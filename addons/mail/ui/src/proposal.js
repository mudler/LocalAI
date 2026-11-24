// What the proposal card of a Mail write shows, from the plain data the host
// hands it. No React, so a node test drives it. The text of the mail is what
// the model wrote and is shown as text.

const str = (v) => (typeof v === 'string' ? v : '')
const list = (v) => (Array.isArray(v) ? v.filter((x) => typeof x === 'string') : [])

// The facts of one proposal. `args` are the stored arguments (the full text);
// `sample` is the short preview the add-on made.
export function proposalView(tool, args, sample) {
  const a = args && typeof args === 'object' ? args : {}
  const s = sample && typeof sample === 'object' ? sample : {}
  const body = str(a.body) || str(s.body)
  return {
    tool: str(tool) === 'save-draft' ? 'save-draft' : 'send-message',
    from: str(s.from) || str(a.mailbox),
    to: list(a.to).length ? list(a.to) : list(s.to),
    cc: list(a.cc).length ? list(a.cc) : list(s.cc),
    subject: str(a.subject) || str(s.subject),
    body,
    reply: str(a.inReplyTo) !== '' || str(s.inReplyTo) !== '',
    cut: s.bodyCut === true && str(a.body) === '',
    bytes: Number.isInteger(s.bodyBytes) ? s.bodyBytes : body.length,
  }
}
