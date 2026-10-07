// Small pure helpers for the chat thread. They read the message shapes useChat
// stores (a string, or a list of typed blocks) and never change them.

export function messageText(content) {
  if (typeof content === 'string') return content
  if (Array.isArray(content)) return content.find(b => b?.type === 'text')?.text || ''
  return ''
}

// The text a message can be edited as, or null when it has none (a message made
// only of attachments, for example).
export function editableMessageText(message) {
  if (typeof message.content === 'string') return message.content
  if (!Array.isArray(message.content)) return null
  const textBlock = message.content.find(block => block?.type === 'text')
  return typeof textBlock?.text === 'string' ? textBlock.text : null
}

// The edited text goes back into the same block, so attachments and the file
// text a user message carries survive an edit.
export function withEditedMessageText(message, text) {
  if (typeof message.content === 'string') return { ...message, content: text }
  const textIndex = message.content.findIndex(block => block?.type === 'text')
  return {
    ...message,
    content: message.content.map((block, index) =>
      index === textIndex ? { ...block, text } : block
    ),
  }
}

// useChat appends a failure to the answer as "Error: ..." after a blank line,
// or as the whole message when nothing was written yet. Only that shape counts
// as an error: an answer that quotes "Error:" mid-sentence is just an answer.
const ERROR_TAIL = /(?:^|\n\n)Error: ([\s\S]*)$/

export function splitError(content) {
  if (typeof content !== 'string') return null
  const m = ERROR_TAIL.exec(content)
  if (!m) return null
  return { text: content.slice(0, m.index).trimEnd(), message: m[1].trim() }
}

export function escapeHtml(text) {
  return String(text).replace(/[&<>"']/g, c => (
    { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]
  ))
}

// Whole seconds as the quiet unit the thread uses ("4 s", "2 min").
export function shortDuration(ms) {
  const s = Math.max(0, Math.round(ms / 1000))
  if (s < 60) return `${s} s`
  return `${Math.round(s / 60)} min`
}

// True for the roles that fold into the one-line activity summary.
export function isActivityRole(role) {
  return role === 'thinking' || role === 'reasoning' || role === 'tool_call' || role === 'tool_result'
}

export function isTyping(el) {
  if (!el) return false
  const tag = el.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || el.isContentEditable
}
